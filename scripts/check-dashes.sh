#!/bin/sh
# No tracked file carries an em dash or an en dash: prose takes a comma, a
# colon or a rewritten sentence, and a range a plain hyphen. Checked here so
# review does not have to. The two characters are built from their UTF-8 bytes
# so this file does not match itself.
#
# Run from the repo root, by `mise run lint`.

set -eu

em=$(printf '\342\200\224')
en=$(printf '\342\200\223')
if git grep -n -e "$em" -e "$en"; then
	echo 'em or en dash above: use a comma, a colon, or a hyphen for a range' >&2
	exit 1
fi
