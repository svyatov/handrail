#!/bin/sh
# The bootstrap pins the version it installs, and each plugin manifest carries
# that version as its cache key: the manifest version is what ships a new
# bootstrap. All three move together, or a plugin installs the wrong binary.
#
# Run from the repo root, by `task release-check`.

set -eu

pin=$(sed -n 's/^PIN=//p' scripts/bootstrap.sh)
[ -n "$pin" ] || {
	echo "scripts/bootstrap.sh has no PIN line" >&2
	exit 1
}

status=0
for dir in .claude-plugin .codex-plugin; do
	version=$(sed -n 's/^ *"version": "\(.*\)",*$/\1/p' "$dir/plugin.json")
	# Every manifest is reported, so one run names every file to bump.
	[ "$version" = "$pin" ] || {
		echo "$dir/plugin.json is $version, bootstrap pins $pin" >&2
		status=1
	}
done

exit $status
