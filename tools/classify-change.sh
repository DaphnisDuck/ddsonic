#!/bin/bash -p
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
# How to run it: bash in privileged mode (-p), which takes no functions,
# BASH_ENV, SHELLOPTS or BASHOPTS from the environment, and with a clean
# environment. The supported invocations are exactly:
#
#   env -i PATH=/usr/local/bin:/usr/bin:/bin LC_ALL=C HOME=/nonexistent \
#       /bin/bash --noprofile --norc -p tools/classify-change.sh ARGS
#   tools/classify-change.sh ARGS      (its #!/bin/bash -p line; best with env -i)
#   /bin/bash -p tools/classify-change.sh ARGS
#
# make validate and CI use the first, through tools/ci-classify.sh. It
# is also the only one immune to an exported FUNCNAME or
# BASH_EXECUTION_STRING: bash -p imports those, and the script then takes
# the start for an embedded one and prints no class (full, never a
# downgrade), exiting with the status of the refused return (2).
# Started any other way (plain "bash script", or sourced), it classifies
# nothing: it prints no class, which a consumer takes as full, and says why
# on stderr. A caller that controls the environment of a plain bash can still
# make that bash print anything before the script's first line (BASH_ENV);
# that is why only the invocations above are supported.
#
# --base   the trusted commit the change is measured from; the diff starts at
#          its merge base with the head. The rules are read from this same
#          commit, so a change cannot loosen the rules that classify it.
#          Without --base, main is fetched from the project's GitHub
#          repository into a ref of this run's own (the only network access;
#          the ref is removed after use) and used; a repository config with a
#          key a plain clone does not have, an unexpected origin, or a failed
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
# It reads the repository and changes nothing in it (but that passing ref), and
# treats every path, rule and ref as data: rules are read line by line, never
# sourced, refs are checked and resolved to commit IDs before use, and paths
# are never passed to a command.

# The start uses only bash keywords ([[ ]], if), which no function can
# replace, until privileged mode is confirmed: without -p, an exported
# function could stand in for any command. It refuses to run inside another
# shell: sourced or dotted (a caller's frame, a function, or -c, -s or an
# interactive shell, whatever $0 says). Then it only returns: if return is
# not allowed there, the if ends and the classifier below is skipped; it
# never exits or replaces the caller.
if [[ ${#BASH_SOURCE[@]} -ne 1 || -v BASH_EXECUTION_STRING || -v FUNCNAME || $- == *[ics]* || ${BASH_SOURCE[0]} != "$0" ]]; then
	printf 'classify-change.sh: run it, do not source it\n' >&2
	return 1 >&2
elif [[ $- != *p* ]]; then
	# Nothing on stdout: a function standing in for printf or exit can only
	# write to stderr here (and if exit returns, nothing follows but the final
	# fi); no class line means full to every consumer.
	printf 'classify-change.sh: fail-closed: not started in privileged mode; run it directly, or: env -i PATH=/usr/local/bin:/usr/bin:/bin LC_ALL=C HOME=/nonexistent /bin/bash --noprofile --norc -p tools/classify-change.sh ...\n' >&2
	exit 0 >&2
else

# A second layer: the shell's options and IFS as bash starts them, and no
# environment from the caller but what is set here.
set +o allexport +o noglob +o errexit
set -u
shopt -u nocasematch nocaseglob extglob globstar nullglob failglob dotglob expand_aliases
IFS=$' \t\n'
for v in $(compgen -e); do
	case $v in PATH | LC_ALL | HOME) ;; *) unset -v "$v" 2>/dev/null ;; esac
done
export LC_ALL=C PATH=/usr/local/bin:/usr/bin:/bin HOME=/nonexistent
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
	'' | -* | *..* | *[!ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789._/@^~-]*) fail "refused a ref: not a plain name or commit ID" ;;
	esac
	id=$(git rev-parse --verify --quiet --end-of-options "$2^{commit}") || fail "a ref that is not a commit"
	case $id in *[!0123456789abcdef]* | '') fail "an odd commit ID" ;; esac
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
	*) fail "an unknown argument" ;;
	esac
done
if [ -n "$paths_mode" ]; then
	for p in "$@"; do
		case $p in --*) fail "an option after --paths: options come before it" ;; esac
	done
	[ -z "$base$head" ] || fail "--paths takes no --base or --head"
fi
[ -z "$rules_from" ] || [ -z "$rules_file" ] || fail "--rules-from and --rules together"

top=$(git rev-parse --show-toplevel 2>/dev/null) || fail "not in a git work tree"
cd "$top" || fail "cannot enter the work tree"

# c0 matches a C0 control character or DEL, byte by byte (no locale class).
c0=$'\x01'-$'\x1f'$'\x7f'

# odd_text is true when its argument holds a code point no path or rule
# should: one that can move a terminal's cursor, break a line, reorder or
# hide text, or is not a character at all. Each is matched on its UTF-8
# bytes, so the check is the same in every locale:
#   C1 controls               U+0080-U+009F   c2 80-9f   (NEL U+0085, CSI U+009B)
#   soft hyphen               U+00AD          c2 ad
#   Arabic letter mark        U+061C          d8 9c
#   Mongolian vowel separator U+180E          e1 a0 8e
#   zero-width and marks      U+200B-U+200F   e2 80 8b-8f
#   line, paragraph separator U+2028-U+2029   e2 80 a8-a9
#   bidirectional embeddings  U+202A-U+202E   e2 80 aa-ae
#   word joiner, invisible operators, bidirectional isolates, deprecated
#     format characters       U+2060-U+206F   e2 81 a0-af
#   non-characters            U+FDD0-U+FDEF   ef b7 90-af
#   byte order mark           U+FEFF          ef bb bf
#   interlinear annotation, U+FFFE, U+FFFF   U+FFF9-U+FFFF   ef bf b9-bf
#   tags                      U+E0000-U+E007F f3 a0 80-81 ..
#   plane-end non-characters  U+nFFFE, U+nFFFF  f0-f4 (8f|9f|af|bf) bf (be|bf)
# Letters beyond ASCII (é, ß, 日本語, Cyrillic, supplementary planes such as
# U+1F600) are allowed. Known and accepted: variation selectors, the
# combining grapheme joiner, Hangul fillers and the like can make two names
# look alike, but move, break or reorder nothing.
odd_text() {
	case $1 in
	*$'\xc2'[$'\x80'-$'\x9f']* | *$'\xc2\xad'* | *$'\xd8\x9c'* | *$'\xe1\xa0\x8e'* | \
		*$'\xe2\x80'[$'\x8b'-$'\x8f']* | *$'\xe2\x80'[$'\xa8'-$'\xae']* | *$'\xe2\x81'[$'\xa0'-$'\xaf']* | \
		*$'\xef\xb7'[$'\x90'-$'\xaf']* | *$'\xef\xbb\xbf'* | *$'\xef\xbf'[$'\xb9'-$'\xbf']* | \
		*$'\xf3\xa0'[$'\x80'$'\x81']* | *[$'\xf0'-$'\xf4'][$'\x8f'$'\x9f'$'\xaf'$'\xbf']$'\xbf'[$'\xbe'$'\xbf']*)
		return 0
		;;
	esac
	return 1
}

# not_canonical is true when its argument holds bytes that are never valid
# UTF-8 as a lead byte, or a lead byte with a second byte outside its range:
#   C0, C1          overlong two-byte forms
#   F5-FF           beyond U+10FFFF, and the old five- and six-byte forms
#   E0 80-9F        overlong three-byte forms
#   ED A0-BF        encoded surrogates
#   F0 80-8F        overlong four-byte forms
#   F4 90-BF        beyond U+10FFFF
# None of these bytes can be a continuation byte, so valid text never
# matches. glibc's iconv accepts F4 90.., F5-F7, F8-FB and FC-FD forms, so
# these checks do not rely on it; iconv still catches what is structural
# (a lone continuation byte, a truncated sequence, a lead byte before ASCII).
not_canonical() {
	case $1 in
	*[$'\xc0'$'\xc1'$'\xf5'-$'\xff']* | *$'\xe0'[$'\x80'-$'\x9f']* | *$'\xed'[$'\xa0'-$'\xbf']* | \
		*$'\xf0'[$'\x80'-$'\x8f']* | *$'\xf4'[$'\x90'-$'\xbf']*)
		return 0
		;;
	esac
	return 1
}

# text_problem prints why its argument may not be a path or a rule pattern,
# or nothing: empty, a C0 control or DEL, not valid UTF-8 (the byte checks,
# then iconv; without iconv, everything is refused), or an odd_text code
# point. Its argument is never printed.
text_problem() {
	case $1 in '' | *[$c0]*) echo "a control character or nothing" && return ;; esac
	if not_canonical "$1" || ! printf '%s' "$1" | iconv -f UTF-8 -t UTF-8 >/dev/null 2>&1; then
		echo "not valid UTF-8 (or no iconv)" && return
	fi
	! odd_text "$1" || echo "an invisible, control or non-character code point"
}

# safe_path refuses a path with a text_problem. No refused path is printed.
safe_path() {
	local why
	why=$(text_problem "$1")
	case $why in
	'') ;;
	'a control character or nothing') fail "a path with a control character or none" ;;
	'not valid UTF-8'*) fail "a path that is not valid UTF-8 (or no iconv)" ;;
	*) fail "a path with an invisible, control or non-character code point" ;;
	esac
}

# load_rules reads the rules (from $tmp/rules) into classes, patterns and
# their line numbers. Messages name a rule by its line number and class,
# never by its text.
classes=() patterns=() lines=()
load_rules() {
	local line cls pat n=0
	while IFS= read -r line || [ -n "$line" ]; do
		n=$((n + 1))
		case $line in '' | '#'*) continue ;; esac
		case $line in
		'docs '* | 'test '* | 'code '* | 'full '*) ;;
		*) fail "a malformed rule at line $n" ;;
		esac
		cls=${line%% *} pat=${line#* }
		case $pat in *' '*) fail "a malformed rule at line $n" ;; esac
		[ -z "$(text_problem "$pat")" ] || fail "a malformed rule at line $n"
		classes+=("$cls") patterns+=("$pat") lines+=("$n")
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
			printf '%s\trule %s (%s)\n' "${classes[$i]}" "${lines[$i]}" "${classes[$i]}"
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
# fetch trusts nothing in the repository's own config that could redirect,
# extend or weaken it: the config may hold only the keys a plain clone has
# (an allow-list), origin must be set once to the project's GitHub URL, and
# the result goes to a ref of this run's own, created by this fetch, which
# is read once and removed.
if [ -z "$base" ]; then
	git config --local --list -z >"$tmp/config" 2>/dev/null || fail "cannot read the repository config"
	url='' origins=0
	while IFS= read -r -d '' entry; do
		key=${entry%%$'\n'*} val=${entry#*$'\n'}
		case $key in
		core.repositoryformatversion | core.filemode | core.bare | core.logallrefupdates | core.ignorecase | core.precomposeunicode) ;;
		remote.origin.url) origins=$((origins + 1)) url=$val ;;
		remote.*.url | remote.*.fetch) ;; # other remotes: never fetched; their URL and refspec only
		branch.*.remote | branch.*.merge) ;;
		*)
			# The key's section and variable only: a subsection can be a URL
			# with credentials in it (url.<base>.insteadOf, http.<url>.*).
			case $key in *.*.*) shown="${key%%.*}.*.${key##*.}" ;; *) shown=$key ;; esac
			fail "the repository config sets $shown, outside what the classifier trusts for a fetch"
			;;
		esac
	done <"$tmp/config"
	[ "$origins" = 1 ] || fail "remote.origin.url is set $origins times"
	case $url in
	git@github.com:DaphnisDuck/ddsonic.git | https://github.com/DaphnisDuck/ddsonic | https://github.com/DaphnisDuck/ddsonic.git) ;;
	*) fail "remote.origin.url is not one of the project's GitHub URLs" ;;
	esac
	fresh=refs/ddsonic/classify-base-$$-$RANDOM
	git update-ref -d "$fresh" 2>/dev/null
	! git show-ref --verify --quiet "$fresh" || fail "cannot clear $fresh"
	git -c core.sshCommand=ssh -c credential.helper= fetch --quiet --no-tags --no-write-fetch-head -- "$url" "+refs/heads/main:$fresh" 2>/dev/null ||
		fail "cannot fetch main from the project's repository"
	resolve base "$fresh"
	git update-ref -d "$fresh" 2>/dev/null
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
case $mb in *[!0123456789abcdef]* | '') fail "odd merge base" ;; esac

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
	*) fail "an unknown status in the git diff output" ;;
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

fi # privileged mode
