#!/bin/sh
# ddsonic: make validate (docs/ddsonic/acceptance.md, "Proportional
# validation"). It classifies the working tree against main, fetched now from
# the project's repository (tools/change-class.sh, with no arguments), runs the
# profile for that class, classifies again, and prints the class line to
# paste into a review request:
#
#   docs  make validate-docs: preflight, gofmt, git diff --check against the
#         base, and the root package's tests with the citation check strict
#   test  make validate-test: preflight, vet, staticcheck, the whole suite
#         under the race detector, shellcheck
#   code  make ci-core
#   full  make ci
#
# PROFILE (the environment; make validate PROFILE=...) may name a higher
# profile, never a lower one: a lower one, or anything but the four names,
# is an error and nothing runs. If the second classification is higher than
# the profile that ran (the tree changed meanwhile), it fails.

dir=$(dirname -- "$0")
make=${MAKE:-make}

rank() {
	case $1 in
	docs) echo 1 ;;
	test) echo 2 ;;
	code) echo 3 ;;
	*) echo 4 ;;
	esac
}

# classify prints the class, exactly one of the four words, then the
# description line, then the classifier's path lines.
classify() {
	out=$(sh "$dir/change-class.sh")
	class=$(printf '%s\n' "$out" | sed -n 1p)
	case $class in docs | test | code | full) ;; *) class=full ;; esac
	meta=$(printf '%s\n' "$out" | sed -n 2p)
}

classify
case ${PROFILE:-} in
'') profile=$class ;;
docs | test | code | full)
	if [ "$(rank "$PROFILE")" -lt "$(rank "$class")" ]; then
		echo "validate: PROFILE=$PROFILE is below the change's class, $class: nothing ran" >&2
		exit 2
	fi
	profile=$PROFILE
	;;
*)
	echo "validate: PROFILE must be docs, test, code or full: nothing ran" >&2
	exit 2
	;;
esac
printf '%s\n' "$out" | sed -n '3,$p'
line="class=$class profile=$profile ${meta#\# }"
echo "validate: $line"

base=$(printf '%s\n' "$meta" | sed -n 's/^# base=\([0-9a-f]\{40,64\}\) .*/\1/p')
# Every profile runs the citation check strictly, against this base
# (citations_ddsonic_test.go): it never skips for want of one. Without a base
# (a fail-closed full), the test falls back to origin/main's merge base and
# fails if there is none.
case $profile in
docs)
	[ -n "$base" ] || { echo "validate: no base for the docs profile" >&2 && exit 2; }
	"$make" validate-docs VALIDATE_BASE="$base" || exit
	;;
test) DDSONIC_CITATION_BASE=$base DDSONIC_CITATION_STRICT=1 "$make" validate-test || exit ;;
code) DDSONIC_CITATION_BASE=$base DDSONIC_CITATION_STRICT=1 "$make" ci-core || exit ;;
*) DDSONIC_CITATION_BASE=$base DDSONIC_CITATION_STRICT=1 "$make" ci || exit ;;
esac

first=$class
classify
if [ "$(rank "$class")" -gt "$(rank "$profile")" ]; then
	echo "validate: FAILED: the class rose from $first to $class while the $profile profile ran; run make validate again" >&2
	exit 1
fi
echo "validate: PASSED $line"
