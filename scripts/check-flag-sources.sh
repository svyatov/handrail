#!/bin/sh
# Every flag table entry in internal/shell names the upstream man page or
# --help text it came from, in a comment on the line above (CONTRIBUTING.md),
# so review can check each flag against its source. An entry that aliases a
# shared table, as "mise x": mise does, is exempt; the shared table is not.
#
# Run from the repo root, by `task lint`.

set -eu

awk '
FNR == 1 { prev = ""; table = 0 }
/^var (wrappers|programs) = map/ { table = 1 }
table && /^}/ { table = 0 }
(table && /^\t"[^"]+": *\{/) || /^var [a-z]+ = &grammar\{/ {
	if (prev !~ /^\t?\/\//) {
		printf "%s:%d: flag table entry with no source comment above it\n", FILENAME, FNR
		bad = 1
	}
}
{ prev = $0 }
END { exit bad }
' internal/shell/*.go
