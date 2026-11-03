#!/bin/sh
# ddsonic: the one consumer of tools/classify-change.sh (docs/ddsonic/
# acceptance.md, "Proportional validation"). make validate (tools/validate.sh)
# and CI run it; it starts the classifier that sits next to it in its
# supported way, with a clean environment, in the current repository, and
# passes on its arguments:
#
#   sh tools/ci-classify.sh [classifier arguments]
#
# It prints the classifier's output with two guarantees: the first line is
# exactly docs, test, code or full, and the second starts with "# ". The
# class is the classifier's first line only when that line is exactly one of
# the four words and the classifier exited 0; anything else (no classifier,
# no output, another word, extra text, a failure) is full, with the reason
# on the second line. It always exits 0. Callers still compare the first
# line exactly.

dir=$(dirname -- "$0")
out=$(env -i PATH=/usr/local/bin:/usr/bin:/bin LC_ALL=C HOME=/nonexistent \
	/bin/bash --noprofile --norc -p "$dir/classify-change.sh" "$@")
status=$?

first=$(printf '%s\n' "$out" | sed -n 1p)
meta=$(printf '%s\n' "$out" | sed -n 2p)
case $status:$first in
0:docs | 0:test | 0:code | 0:full) ;;
*)
	echo full
	if [ ! -f "$dir/classify-change.sh" ]; then
		echo "# fail-closed: no tools/classify-change.sh"
	else
		echo "# fail-closed: the classifier gave no class (exit $status)"
	fi
	exit 0
	;;
esac
echo "$first"
case $meta in
'# '*) echo "$meta" ;;
*) echo "# no description line" ;;
esac
printf '%s\n' "$out" | sed -n '3,$p'
exit 0
