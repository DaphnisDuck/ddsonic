#!/usr/bin/env bash
# ddsonic: classifies a change for proportional validation
# (docs/ddsonic/acceptance.md, "Proportional validation"). It prints the
# change's class on its first line, exactly one of docs, test, code or full,
# then one line per changed path: class, status, path, the reason. It always
# exits 0 once it has printed a class. Anything it cannot decide safely is
# full, with the reason "fail-closed: ...". A consumer must treat any first
# line other than exactly docs, test or code as full.
#
#   tools/classify-change.sh [--base REF] [--head REF] [--rules-from REF | --rules FILE]
#   tools/classify-change.sh --paths [--rules-from REF | --rules FILE] PATH...
#
# --base   the commit the change is measured from; the diff starts at its
#          merge base with the head. Without --base, origin/main is fetched
#          now (the only network access) and used; a failed fetch is full.
# --head   the commit to classify. Without --head, the working tree: HEAD, the
#          uncommitted changes to tracked files and the untracked files.
# --rules-from  read tools/validation-classes.txt from that commit, as CI and
#          make validate do with the base, so that a change cannot loosen the
#          rules that classify it. Without it, --rules or the working tree's.
# --paths  classify the given paths by the rules alone (one line each, no
#          overall class); for the tests.
#
# It reads the repository and changes nothing in it, and treats every path,
# rule and ref as data: rules are read line by line, never sourced, and refs
# are checked and resolved to commit IDs before use.
set -u
export LC_ALL=C
export PATH=/usr/local/bin:/usr/bin:/bin
# Only the repository it runs in, read with no outside configuration: git's
# environment overrides (GIT_DIR, GIT_WORK_TREE, GIT_CONFIG_*, GIT_EXTERNAL_DIFF
# and the rest) and the user's and system configuration are dropped.
for v in $(compgen -e); do
	case $v in GIT_*) unset "$v" ;; esac
done
export GIT_TERMINAL_PROMPT=0 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null
git() { command git -c core.quotepath=false -c core.fsmonitor=false -c diff.external= "$@"; }

tmp=$(mktemp -d) || {
	printf 'full\nfull\t-\t-\tfail-closed: no temporary directory\n'
	exit 0
}
trap 'rm -rf "$tmp"' EXIT

# fail ends the classification as full, with its reason.
fail() {
	printf 'full\nfull\t-\t-\tfail-closed: %s\n' "$1"
	exit 0
}

rank() {
	case $1 in docs) echo 0 ;; test) echo 1 ;; code) echo 2 ;; *) echo 3 ;; esac
}

# resolve sets the variable named $1 to the commit ID of the ref $2, or
# fails. A ref that starts with a dash, holds a range or an odd character is
# refused before git sees it. (It runs in this shell, never in a $(...), so
# that fail ends the script.)
resolve() {
	local id
	case $2 in
	'' | -* | *..* | *[!A-Za-z0-9._/@^~-]*) fail "refused ref '$2'" ;;
	esac
	id=$(git rev-parse --verify --quiet --end-of-options "$2^{commit}") || fail "not a commit: '$2'"
	case $id in *[!0-9a-f]* | '') fail "odd commit id for '$2'" ;; esac
	printf -v "$1" '%s' "$id"
}

base='' head='' rules_from='' rules_file='' paths_mode=''
rid='' bid='' hid='' # set by resolve
while [ $# -gt 0 ]; do
	case $1 in
	--base) [ $# -ge 2 ] || fail "--base needs a value"; base=$2; shift 2 ;;
	--head) [ $# -ge 2 ] || fail "--head needs a value"; head=$2; shift 2 ;;
	--rules-from) [ $# -ge 2 ] || fail "--rules-from needs a value"; rules_from=$2; shift 2 ;;
	--rules) [ $# -ge 2 ] || fail "--rules needs a value"; rules_file=$2; shift 2 ;;
	--paths) paths_mode=1; shift; break ;;
	*) fail "unknown argument '$1'" ;;
	esac
done

top=$(git rev-parse --show-toplevel 2>/dev/null) || fail "not in a git work tree"
cd "$top" || fail "cannot enter the work tree"

# The rules: from a commit (the base, in CI and make validate), a file, or
# the working tree's copy.
if [ -n "$rules_from" ]; then
	resolve rid "$rules_from"
	git cat-file blob "$rid:tools/validation-classes.txt" >"$tmp/rules" 2>/dev/null || fail "no tools/validation-classes.txt in $rid"
elif [ -n "$rules_file" ]; then
	cat -- "$rules_file" >"$tmp/rules" 2>/dev/null || fail "cannot read the rules file"
else
	cat tools/validation-classes.txt >"$tmp/rules" 2>/dev/null || fail "cannot read tools/validation-classes.txt"
fi
classes=() patterns=()
while IFS= read -r line || [ -n "$line" ]; do
	case $line in '' | '#'*) continue ;; esac
	case $line in
	'docs '* | 'test '* | 'code '* | 'full '*) ;;
	*) fail "malformed rule '$line'" ;;
	esac
	cls=${line%% *} pat=${line#* }
	case $pat in '' | *' '* | *[[:cntrl:]]*) fail "malformed rule '$line'" ;; esac
	classes+=("$cls") patterns+=("$pat")
done <"$tmp/rules"
[ ${#classes[@]} -gt 0 ] || fail "no rules"

# class_of prints a path's class and the rule that decided it.
class_of() {
	local i
	for i in "${!patterns[@]}"; do
		# shellcheck disable=SC2053 # the pattern is a glob, matched as one
		if [[ $1 == ${patterns[$i]} ]]; then
			printf '%s\trule %s %s\n' "${classes[$i]}" "${classes[$i]}" "${patterns[$i]}"
			return
		fi
	done
	printf 'full\tno rule matches\n'
}

if [ -n "$paths_mode" ]; then
	for p in "$@"; do
		r=$(class_of "$p")
		printf '%s\t%s\n' "$p" "$r"
	done
	exit 0
fi

[ "$(git rev-parse --is-shallow-repository 2>/dev/null)" = false ] || fail "a shallow or unreadable repository: the merge base cannot be trusted"

if [ -z "$base" ]; then
	git fetch --quiet --no-tags origin main 2>/dev/null || fail "cannot fetch origin main"
	base=FETCH_HEAD
fi
resolve bid "$base"
if [ -n "$head" ]; then
	resolve hid "$head"
else
	resolve hid HEAD
fi
mb=$(git merge-base "$bid" "$hid" 2>/dev/null) || fail "no merge base of $bid and $hid"
case $mb in *[!0-9a-f]* | '') fail "odd merge base" ;; esac

if [ -n "$head" ]; then
	git diff --raw -z -M --no-ext-diff --no-textconv "$mb" "$hid" -- >"$tmp/raw" 2>/dev/null || fail "git diff failed"
	git diff --numstat -z -M --no-ext-diff --no-textconv "$mb" "$hid" -- >"$tmp/num" 2>/dev/null || fail "git diff --numstat failed"
	: >"$tmp/untracked"
else
	git diff --raw -z -M --no-ext-diff --no-textconv "$mb" -- >"$tmp/raw" 2>/dev/null || fail "git diff failed"
	git diff --numstat -z -M --no-ext-diff --no-textconv "$mb" -- >"$tmp/num" 2>/dev/null || fail "git diff --numstat failed"
	git ls-files -z -o --exclude-standard >"$tmp/untracked" 2>/dev/null || fail "git ls-files failed"
fi

worst=0 n=0
out=()
# note records a path's class and reason and keeps the strongest class.
note() { # class status path reason
	local r
	r=$(rank "$1")
	[ "$r" -gt "$worst" ] && worst=$r
	out+=("$(printf '%s\t%s\t%s\t%s' "$1" "$2" "$3" "$4")")
	n=$((n + 1))
}
safe_path() {
	case $1 in '' | *[[:cntrl:]]*) fail "a path with a control character or none" ;; esac
}
by_rules() { # status path
	local r
	safe_path "$2"
	r=$(class_of "$2")
	note "${r%%$'\t'*}" "$1" "$2" "${r#*$'\t'}"
}

# The raw diff: ":oldmode newmode oldsha newsha status" then one path, or two
# for a rename or copy.
exec 3<"$tmp/raw"
while IFS= read -r -d '' meta <&3; do
	case $meta in :*) ;; *) fail "unexpected git diff output" ;; esac
	read -r om nm _ _ st <<<"${meta#:}"
	IFS= read -r -d '' p1 <&3 || fail "truncated git diff output"
	p2=
	case $st in
	R* | C*) IFS= read -r -d '' p2 <&3 || fail "truncated git diff output" ;;
	esac
	safe_path "$p1"
	[ -z "$p2" ] || safe_path "$p2"
	for m in "$om" "$nm"; do
		case $m in
		120000) note full "$st" "$p1" "a symlink"; continue 2 ;;
		160000) note full "$st" "$p1" "a submodule"; continue 2 ;;
		000000 | 100644) ;;
		100755) [ "$om" = "$nm" ] || { note full "$st" "$p1" "a mode change ($om to $nm)"; continue 2; } ;;
		*) note full "$st" "$p1" "an unusual mode ($m)"; continue 2 ;;
		esac
	done
	if [ "$om" != 000000 ] && [ "$nm" != 000000 ] && [ "$om" != "$nm" ]; then
		note full "$st" "$p1" "a mode change ($om to $nm)"
		continue
	fi
	if [ "$nm" = 100755 ] && [ "$om" = 000000 ]; then
		note full "$st" "$p1" "a new executable file"
		continue
	fi
	case $st in
	A | M) by_rules "$st" "$p1" ;;
	D)
		case $p1 in *_test.go) note full D "$p1" "a deleted test" ;; *) by_rules D "$p1" ;; esac
		;;
	R* | C*)
		case $st$p1 in R*_test.go) note full "$st" "$p1 -> $p2" "a renamed test"; continue ;; esac
		r1=$(class_of "$p1") r2=$(class_of "$p2")
		c1=${r1%%$'\t'*} c2=${r2%%$'\t'*}
		if [ "$c1" != "$c2" ]; then
			note full "$st" "$p1 -> $p2" "a rename across classes ($c1 to $c2)"
		else
			note "$c1" "$st" "$p1 -> $p2" "both ${r1#*$'\t'}"
		fi
		;;
	T) note full T "$p1" "a type change" ;;
	*) fail "unknown status '$st'" ;;
	esac
done
exec 3<&-

# Binary files, from the numstat: "-<TAB>-<TAB>path", or for a rename
# "-<TAB>-<TAB>" followed by the two paths.
exec 3<"$tmp/num"
while IFS= read -r -d '' rec <&3; do
	added=${rec%%$'\t'*}
	rest=${rec#*$'\t'}
	path=${rest#*$'\t'}
	if [ -z "$path" ]; then
		IFS= read -r -d '' _ <&3 || fail "truncated numstat output"
		IFS= read -r -d '' path <&3 || fail "truncated numstat output"
	fi
	safe_path "$path"
	[ "$added" = - ] && note full B "$path" "a binary file"
done
exec 3<&-

# Untracked files, in the working-tree mode: added, by their path, unless
# they are not plain text files.
exec 3<"$tmp/untracked"
while IFS= read -r -d '' u <&3; do
	safe_path "$u"
	if [ -L "$u" ] || [ ! -f "$u" ]; then
		note full '?' "$u" "an untracked file that is not a regular file"
	elif [ "$(tr -d '\000' <"$u" | wc -c)" != "$(wc -c <"$u")" ]; then
		note full '?' "$u" "an untracked binary file"
	else
		by_rules '?' "$u"
	fi
done
exec 3<&-

[ "$n" -gt 0 ] || fail "an empty diff"
case $worst in 0) echo docs ;; 1) echo test ;; 2) echo code ;; *) echo full ;; esac
printf '%s\n' "${out[@]}"
