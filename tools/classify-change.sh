#!/usr/bin/env bash
# ddsonic: classifies a change for proportional validation
# (docs/ddsonic/acceptance.md, "Proportional validation"). It prints the
# change's class on its first line, exactly one of docs, test, code or full;
# a second line starting with "# " says what it compared (the base, the head
# and where the rules came from); then one line per changed path: class,
# status, path, the reason. It exits 0 once it has printed a class. Anything
# it cannot decide safely is full, with the reason "fail-closed: ...". A
# consumer must treat any first line other than exactly docs, test or code
# as full.
#
#   tools/classify-change.sh [--base REF] [--head REF] [--rules-from REF]
#   tools/classify-change.sh [--rules-from REF | --rules FILE] --paths PATH...
#   tools/classify-change.sh --rules FILE [--base REF] [--head REF]   (dev mode)
#
# --base   the trusted commit the change is measured from; the diff starts at
#          its merge base with the head. The rules are read from this same
#          commit, so a change cannot loosen the rules that classify it.
#          Without --base, main is fetched from the project's GitHub
#          repository into refs/ddsonic/classify-base (the only network access
#          and the only write) and used; an unexpected origin, a repository
#          config that rewrites URLs or includes other files, or a failed
#          fetch is full.
# --head   the commit to classify. Without --head, the working tree: HEAD, the
#          uncommitted changes to tracked files and the untracked files.
# --rules-from  must name the same commit as --base (it exists for clarity);
#          any other commit is full. With --paths, the commit to read the rules
#          from.
# --rules FILE  dev mode: rules from a file, reported as such on the "# "
#          line. make validate and CI never use it.
# --paths  last: everything after it is a path to classify by the rules alone
#          (one line each, no overall class); an option after it is full.
#
# It reads the repository and changes nothing in it but that one ref, and
# treats every path, rule and ref as data: rules are read line by line, never
# sourced, refs are checked and resolved to commit IDs before use, and paths
# are never passed to a command.

# A clean start: none of the caller's environment (BASHOPTS, SHELLOPTS,
# BASH_ENV, IFS, GIT_* and the rest) reaches the classification. The script
# runs itself again under env -i with a fixed environment, unless it already
# is that run; an inherited marker alone does not count, as the environment
# must also hold nothing else.
clean_env() {
	local v
	[ "${DDSONIC_CLASSIFY_CLEAN:-}" = 1 ] || return 1
	for v in $(compgen -e); do
		case $v in DDSONIC_CLASSIFY_CLEAN | PATH | LC_ALL | HOME | PWD | OLDPWD | SHLVL | _) ;; *) return 1 ;; esac
	done
}
if ! clean_env; then
	if [ ! -x /usr/bin/env ] || [ ! -x /bin/bash ]; then
		printf 'full\n# fail-closed\nfull\t-\t-\tfail-closed: no /usr/bin/env or /bin/bash for a clean start\n'
		exit 0
	fi
	exec /usr/bin/env -i DDSONIC_CLASSIFY_CLEAN=1 PATH=/usr/local/bin:/usr/bin:/bin LC_ALL=C HOME=/nonexistent \
		/bin/bash --noprofile --norc -- "$0" "$@"
fi
# Then the shell's own state, whatever this bash started with: default
# options, the default IFS, no inherited functions.
set +o allexport +o noglob +o errexit
set -u
shopt -u nocasematch nocaseglob extglob globstar nullglob failglob dotglob expand_aliases
IFS=$' \t\n'
unset -v CDPATH GLOBIGNORE BASH_ENV ENV
for v in $(compgen -e); do
	case $v in GIT_*) unset "$v" ;; esac
done
export LC_ALL=C PATH=/usr/local/bin:/usr/bin:/bin
# Git reads no outside configuration (the user's and the system's are
# dropped), runs no hooks and allows no ext:: or file:// transport.
export GIT_TERMINAL_PROMPT=0 GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null
git() {
	command git -c core.quotepath=false -c core.fsmonitor=false -c diff.external= \
		-c core.hooksPath=/dev/null -c protocol.ext.allow=never -c protocol.file.allow=never "$@"
}

tmp=$(mktemp -d) || {
	printf 'full\n# fail-closed\nfull\t-\t-\tfail-closed: no temporary directory\n'
	exit 0
}
trap 'rm -rf "$tmp"' EXIT

# fail ends the classification as full, with its reason.
fail() {
	printf 'full\n# fail-closed\nfull\t-\t-\tfail-closed: %s\n' "$1"
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
if [ -n "$paths_mode" ]; then
	for p in "$@"; do
		case $p in --*) fail "an option after --paths ('$p'): options come before it" ;; esac
	done
	[ -z "$base$head" ] || fail "--paths takes no --base or --head"
fi
[ -z "$rules_from" ] || [ -z "$rules_file" ] || fail "--rules-from and --rules together"

top=$(git rev-parse --show-toplevel 2>/dev/null) || fail "not in a git work tree"
cd "$top" || fail "cannot enter the work tree"

# safe_path refuses a path with a control character, or one that is not
# valid UTF-8 (iconv decides; without iconv, every path is refused).
safe_path() {
	case $1 in '' | *[[:cntrl:]]*) fail "a path with a control character or none" ;; esac
	printf '%s' "$1" | iconv -f UTF-8 -t UTF-8 >/dev/null 2>&1 || fail "a path that is not valid UTF-8 (or no iconv)"
}

# load_rules reads the rules (from $tmp/rules) into classes and patterns.
classes=() patterns=()
load_rules() {
	local line cls pat
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
}

# rules_from_commit reads the rules of commit $1.
rules_from_commit() {
	git cat-file blob "$1:tools/validation-classes.txt" >"$tmp/rules" 2>/dev/null || fail "no tools/validation-classes.txt in $1"
	rules_src="$1:tools/validation-classes.txt"
}

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

rules_src=''
if [ -n "$paths_mode" ]; then
	if [ -n "$rules_from" ]; then
		resolve rid "$rules_from"
		rules_from_commit "$rid"
	elif [ -n "$rules_file" ]; then
		cat -- "$rules_file" >"$tmp/rules" 2>/dev/null || fail "cannot read the rules file"
		rules_src="file:$rules_file (dev mode)"
	else
		cat tools/validation-classes.txt >"$tmp/rules" 2>/dev/null || fail "cannot read tools/validation-classes.txt"
		rules_src="file:tools/validation-classes.txt (dev mode)"
	fi
	load_rules
	printf '# rules=%s\n' "$rules_src"
	for p in "$@"; do
		safe_path "$p"
		r=$(class_of "$p")
		printf '%s\t%s\n' "$p" "$r"
	done
	exit 0
fi

[ "$(git rev-parse --is-shallow-repository 2>/dev/null)" = false ] || fail "a shallow or unreadable repository: the merge base cannot be trusted"

# The base: given, or main fetched now from the project's repository. The
# fetch trusts nothing in the repository's own config that could redirect or
# extend it: origin must be the project's GitHub URL, and the config may
# rewrite no URL and include no other file.
if [ -z "$base" ]; then
	url=$(git config --local --get remote.origin.url 2>/dev/null) || fail "no origin remote to fetch main from"
	case $url in
	git@github.com:DaphnisDuck/ddsonic.git | https://github.com/DaphnisDuck/ddsonic | https://github.com/DaphnisDuck/ddsonic.git) ;;
	*) fail "origin is not the project's GitHub repository ('$url')" ;;
	esac
	if git config --local --name-only --get-regexp '^(url\.|include\.|includeif\.|core\.sshcommand$|credential\.)' >/dev/null 2>&1; then
		fail "the repository config rewrites URLs, includes other files or sets ssh or credential commands"
	fi
	git -c core.sshCommand=ssh -c credential.helper= fetch --quiet --no-tags --no-write-fetch-head -- "$url" "+refs/heads/main:refs/ddsonic/classify-base" 2>/dev/null ||
		fail "cannot fetch main from $url"
	base=refs/ddsonic/classify-base
fi
resolve bid "$base"
if [ -n "$head" ]; then
	resolve hid "$head"
else
	resolve hid HEAD
fi

# The rules come from the base itself, unless a dev-mode file is given. A
# --rules-from that is not the base is refused: one trusted commit decides
# both what the change is measured from and the rules that classify it.
if [ -n "$rules_file" ]; then
	cat -- "$rules_file" >"$tmp/rules" 2>/dev/null || fail "cannot read the rules file"
	rules_src="file:$rules_file (dev mode)"
else
	if [ -n "$rules_from" ]; then
		resolve rid "$rules_from"
		[ "$rid" = "$bid" ] || fail "--rules-from $rid is not the base $bid"
	fi
	rules_from_commit "$bid"
fi
load_rules

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
headdesc=$hid
[ -n "$head" ] || headdesc="$hid+worktree"
printf '# base=%s head=%s rules=%s\n' "$bid" "$headdesc" "$rules_src"
printf '%s\n' "${out[@]}"
