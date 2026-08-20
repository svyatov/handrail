#!/bin/sh
# Print one version's CHANGELOG.md section, for `goreleaser --release-notes`.
# GoReleaser's default body is the commit list, which addresses the maintainer
# and says something different about the release than the changelog does. This
# makes the changelog entry the release body, so there is only one description.
#
# With no argument it reads the version bootstrap.sh pins, which is how
# `task release-check` fails a release whose changelog section is missing before
# the tag rather than after it.
#
# Run from the repo root, by `task release-check` with no argument and by
# .github/workflows/release.yml with the tag's version. Both CHANGELOG.md and
# scripts/bootstrap.sh are read relative to the working directory.

set -eu

version=${1:-$(sed -n 's/^PIN=//p' scripts/bootstrap.sh)}
[ -n "$version" ] || {
	echo "scripts/bootstrap.sh has no PIN line" >&2
	exit 1
}

notes=$(awk -v heading="## [$version]" '
	index($0, heading) == 1 { found = 1; next }
	# The link definition block at the end of the file belongs to no section, so
	# the oldest release stops there as every other one stops at the next heading.
	found && (/^## / || /^\[[^]]+\]: /) { exit }
	found { print }
' CHANGELOG.md)

# An empty body would publish a release nobody can read, and the cause is always
# a changelog nobody updated for this tag.
printf '%s' "$notes" | grep -q '[^[:space:]]' || {
	echo "CHANGELOG.md has no '## [$version]' section, or the section is empty" >&2
	exit 1
}

printf '%s\n' "$notes"
