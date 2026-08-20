# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/2.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
handrail is pre-1.0, so a MINOR bump may break you and a PATCH bump will not.
The public surface is the command set, the rule file format, and the exit codes,
all of which [`docs/spec.md`](docs/spec.md) states.

## [Unreleased]

### Added

- Every release archive now ships an SPDX SBOM beside it, named
  `<archive>.sbom.json`, listing what went into that binary.
- Every published archive, SBOM, and `checksums.txt` now carries a GitHub
  build-provenance attestation. Verify a download with
  `gh attestation verify <file> --repo svyatov/handrail`, which checks the file
  itself rather than the commit the signed tag covers.

### Changed

- The release body on GitHub is now this file's section for that version,
  instead of the commit list. A tag whose section is missing fails the release
  before anything is published, and `task release-check` catches it a step
  earlier, on the pull request.

## [0.1.0] - 2026-08-20

First release. One rule file, enforced in Claude Code and in Codex CLI through
each harness's own hook mechanism.

### Added

- Rules as markdown files: the matcher in the frontmatter, the message the agent
  reads in the body.
- Three tiers, most specific first: project-personal `.handrail/local/`,
  project-shared `.handrail/`, and global `~/.config/handrail/`. A rule replaces
  a lower one of the same filename outright, and a stub carrying
  `enabled: false` switches an inherited rule off.
- `handrail check` validates every tier and prints the effective ruleset,
  annotated with tier, shadowing, and disabling.
- `handrail test <event>` dry-runs one synthetic event against the rules and
  exits 2 when the outcome is block.
- `handrail sync` writes handrail's hook entries into every detected harness.
  Where a harness cannot do what a rule asks, it substitutes the strongest
  action that harness supports and reports the substitution rather than
  degrading the rule silently.
- `handrail advise` reports which rules also translate into a native harness
  entry, such as a Claude Code permission deny.
- `handrail trust` grants a repo's committed `.handrail/` rules permission to
  take effect.
- `handrail import hookify` converts hookify rule files into personal rules and
  reports anything the format cannot express.
- `handrail doctor` diagnoses the install offline.
- `handrail version` prints the version, commit, and build date.
- `handrail hook` is the entry point the harnesses call. Sync installs it.
- Plugins for Claude Code and Codex CLI from the repo's own marketplace,
  carrying the `add` skill. The plugin downloads the pinned binary on the next
  session start, verifies its checksum, and syncs once.
- Prebuilt binaries for macOS and Linux on amd64 and arm64, and a Homebrew cask
  at `svyatov/tap/handrail`. There is no Windows build.

## [0.1.0-rc.1] - 2026-08-18

Prerelease that proved the release pipeline end to end: the GoReleaser build,
the checksum manifest, and the tap cask. The Claude Code and Codex CLI plugins
landed after it, in 0.1.0.

[unreleased]: https://github.com/svyatov/handrail/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/svyatov/handrail/compare/v0.1.0-rc.1...v0.1.0
[0.1.0-rc.1]: https://github.com/svyatov/handrail/releases/tag/v0.1.0-rc.1
