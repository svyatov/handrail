#!/bin/sh
# The skills are shell snippets an agent runs verbatim, so every path in them is
# a path the code decides: where bootstrap installs the binary, and where each
# harness keeps its state. Nothing executes a skill's copy, so a move or a
# rename breaks skill invocation silently, at the one moment nobody is looking.
# This is the grep that turns that into a failing lint.
#
# Run from the repo root, by `task lint`.

set -eu

status=0
# Every mismatch is reported, so one run names every file to edit.
mismatch() {
	echo "$1" >&2
	status=1
}

# The binary's install directory. bootstrap.sh is the one that creates it, so it
# is the source of truth and the skills quote it.
bindir=$(sed -n 's/^bindir=//p' scripts/bootstrap.sh)
[ -n "$bindir" ] || {
	echo "scripts/bootstrap.sh has no bindir line" >&2
	exit 1
}
for skill in skills/add/SKILL.md skills/analyze/SKILL.md; do
	grep -qF "$bindir/handrail" "$skill" ||
		mismatch "$skill does not spell the install directory as bootstrap.sh does: $bindir"
done

# Each harness's user-level directory and the variable that relocates it. The
# analyze skill reads transcripts out of both, so a harness added to the adapter
# table without a line there is a harness analyze cannot see.
adapters=internal/harness/harness.go
pairs=$(sed -n 's/.*dir: "\([^"]*\)", homeEnv: "\([^"]*\)".*/\1 \2/p' "$adapters")
names=$(sed -n 's/.*Name: "\([a-z]*\)",.*/\1/p' "$adapters" | grep -c .)
found=$(echo "$pairs" | grep -c .)
# A reformat that splits an adapter's fields across lines would leave this
# reading fewer pairs than there are adapters, and pass by finding nothing.
[ "$found" -eq "$names" ] || {
	echo "$adapters has $names adapters but $found dir/homeEnv pairs on one line: fix this script's sed" >&2
	exit 1
}
# Collected rather than reported inline, because a pipeline's loop body runs in
# a subshell and could not raise status from there.
missing=$(echo "$pairs" | while read -r dir env; do
	grep -qF "\${$env:-\$HOME/$dir}" skills/analyze/SKILL.md ||
		echo "skills/analyze/SKILL.md does not read \${$env:-\$HOME/$dir}, which $adapters declares"
done)
[ -z "$missing" ] || mismatch "$missing"

exit $status
