#!/bin/sh
# handrail bootstrap, run from SessionStart by both plugins.
#
# Install-on-miss: a PATH binary (brew, go install) is the user's and is never
# touched; otherwise the pinned release artifact is downloaded, verified against
# the release's checksums.txt, and installed under the XDG data dir. Sync runs
# once, right after a fresh install, so the plugin works with zero manual steps.
#
# Invoked as `sh bootstrap.sh` because exec-bit survival through the plugin
# cache is undocumented upstream (docs/plugin-verification.md).

set -eu

# The version this plugin pins; equal to the plugin's own version, since the
# manifest version is the cache key that ships a new bootstrap. Bump this and
# the version in both manifests, .claude-plugin/ and .codex-plugin/, together.
PIN=0.2.0
REPO=svyatov/handrail

# A binary the user's package manager owns wins, always.
if command -v handrail >/dev/null 2>&1; then
	exit 0
fi

bindir=${XDG_DATA_HOME:-$HOME/.local/share}/handrail/bin
bin=$bindir/handrail

if [ -x "$bin" ] && [ "$("$bin" version 2>/dev/null | head -n 1)" = "handrail $PIN" ]; then
	exit 0
fi

die() {
	echo "handrail bootstrap: $1" >&2
	exit 1
}

case $(uname -s) in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) die "unsupported operating system $(uname -s)" ;;
esac

case $(uname -m) in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) die "unsupported architecture $(uname -m)" ;;
esac

command -v curl >/dev/null 2>&1 || die "curl is required to install the binary"

digest() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
	else
		die "no sha256 tool found (sha256sum or shasum)"
	fi
}

archive=handrail_${PIN}_${os}_${arch}.tar.gz
base=https://github.com/$REPO/releases/download/v$PIN

# Everything lands in a temp dir first, so a failure anywhere above the final
# rename leaves nothing half-installed. The staging name carries the pid, so two
# sessions starting at once cannot rename each other's half-written copy.
staged=$bin.$$
tmp=$(mktemp -d)
trap 'rm -rf "$tmp" "$staged"' EXIT

# A stalled download must not eat the hook's whole timeout budget.
curl -fsSL --retry 2 --max-time 45 -o "$tmp/$archive" "$base/$archive" ||
	die "cannot download $base/$archive"
curl -fsSL --retry 2 --max-time 45 -o "$tmp/checksums.txt" "$base/checksums.txt" ||
	die "cannot download $base/checksums.txt"

want=$(awk -v f="$archive" '$2 == f || $2 == "*" f {print $1; exit}' "$tmp/checksums.txt")
[ -n "$want" ] || die "$archive is not listed in the release's checksums.txt"

got=$(digest "$tmp/$archive")
[ "$got" = "$want" ] || die "checksum mismatch for $archive: expected $want, got $got"

tar -xzf "$tmp/$archive" -C "$tmp" handrail || die "cannot extract $archive"

mkdir -p "$bindir"
# Rename within the target directory, so the swap is atomic on any Unix.
cp "$tmp/handrail" "$staged"
chmod +x "$staged"
mv "$staged" "$bin"

echo "handrail $PIN installed at $bin"
# Sync is the last step and reports its own errors. Failing it would fail the
# hook, which reads as a broken session start over a binary that installed fine.
"$bin" sync || echo "handrail: sync failed; run \"$bin\" sync once the cause is fixed" >&2
