# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/2.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
handrail is pre-1.0, so a MINOR bump may break you and a PATCH bump will not.
The public surface is the command set, the rule file format, and the exit codes,
all of which [`docs/spec.md`](docs/spec.md) states.

## [Unreleased]

## [0.3.0] - 2026-09-26

handrail v2. A rule now reads the input an agent actually produces, and what
handrail cannot read is a value a rule can match. Rule files and scripts written
for 0.2.0 can behave differently: every such change is marked **Breaking**.
Run `handrail check` and `handrail sync` after upgrading.

### Added

- `ask`, an Action between `warn` and `block`, on `PreToolUse`. Codex cannot
  ask, so there it degrades to `block` with a reason that says the rule wanted
  approval.
- `trial: true` evaluates a rule and delivers nothing. `handrail mode
  enforce|trial|off`, with `handrail on` and `handrail off`, suspends
  enforcement for this project or, with `--global`, machine-wide, and every
  `SessionStart` in a suspended project says so.
- The Decision log. `handrail log on` records one line per matched or unreadable
  evaluation in this project, `handrail log` reads it back, and
  `handrail check --stats` summarises it per rule.
- `examples:` on a rule, `match:` and `no_match:` calls that `check`, `sync`,
  `doctor` and every `SessionStart` run.
- The `unreadable` field names what handrail could not read, so a rule can fail
  closed on it.
- The `SubagentStart` and `SubagentStop` events, and `response` on `Stop` and
  `SubagentStop`.
- `tool` holds every name the harness answers to for a call. `kind: agent` and
  `kind: network` come with `agent_type`, `agent_prompt`, `model`, `url`,
  `domain`, `network_grant` and `unsandboxed`.
- A shell call yields a `file_read` or `file_edit` payload for each file it
  redirects to or a listed program names, so a `path` rule on `.env` covers
  `cat .env` and `> .env`.
- Every file of a multi-file Codex `apply_patch` is its own payload, with
  `removed_content`, `writes_empty` and `deletes`.
- Each event tells the human which rules fired. `agent_only: true` keeps a
  coaching `warn` out of the human's channel.
- `handrail survey` prints the repository's signals and instruction files as
  JSON, and the `/handrail:survey` skill proposes rules from them.
  `/handrail:analyze` reads the Decision log where it is on.
- `handrail doctor` fails when handrail's hooks would not run, and reports the
  enforcement state, the log grant and the Examples.
- `handrail test` shows per payload the Candidates, what was unreadable and the
  human line, and takes `--stdin` and `--harness codex`.

### Changed

- **Breaking**: multi-valued matching. A `command` condition reads the command
  as a shell program and asks its question of every command the program runs,
  through Wrappers such as `sudo` and `env` and through literal nested shells,
  so `starts_with: rm -rf /` fires on `cd /tmp && rm -rf /`. The terms on one
  field bind to one Candidate, and a `not_` term fires when some Candidate fails
  it, so `not_starts_with: git` fires on `git status; rm -rf /`. A rule written
  as an allowlist over `command` now fires on calls it used to pass.
- **Breaking**: the Project-shared tier is add-only against Global. A committed
  rule named like one of your Global rules is dropped and your Global rule
  stands. `check` and `sync` name both files.
- **Breaking**: supply demotion. A `.handrail/local/` that the git index tracks,
  or that is reached through a symlink, is read as Project-shared: it needs
  `handrail trust` and follows the add-only rule.
- **Breaking**: new validation errors. A rule 0.2.0 accepted can now fail
  `check` and `sync`, and be skipped at event time, for a condition on a field
  its event never carries; `kind:` on an event other than `PreToolUse` and
  `PostToolUse`; a `path` glob or `equals` value that lexical cleaning would
  change; `ask` on any event but `PreToolUse`; `block` on `PostToolUse`,
  `SessionStart`, `SessionEnd` or `SubagentStart`; `agent_only: true` on a
  `block` or `ask`, on a `warn` on `Stop`, `SubagentStop` or `SessionEnd`, or in
  the Project-shared tier; `trial: true` on a disabled rule; or an invalid
  Example.
- **Breaking**: `handrail test` exits 3 when the outcome is `ask`, and still 2
  on `block`.
- **Breaking**: one hook entry per event, for all eight events, each with no
  matcher and no `if`. `sync` writes them even when a rule is invalid, then
  exits 1, and `doctor` fails on a handrail entry narrowed by a matcher or an
  `if`, so an entry you narrowed by hand now reports as broken.
- `block` on `Stop` and `SubagentStop` continues the agent once, with the rule's
  message as its next instruction, and never loops.
- handrail never blocks a call for its own failure. A missing Global tier, a
  non-string JSON value and a broken rule file set `unreadable` and are
  reported to the human and the agent.
- `handrail import hookify` imports a stop rule as a `Stop` rule on `response`.
- A command is parsed with `mvdan.cc/sh/v3/syntax`, handrail's one third-party
  runtime dependency.

### Removed

- **Breaking**: `handrail advise` and the Advisor. handrail recommends no native
  permission entries: the hook is its one enforcement path.

## [0.2.0] - 2026-08-20

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

[unreleased]: https://github.com/svyatov/handrail/compare/v0.3.0...HEAD
[0.3.0]: https://github.com/svyatov/handrail/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/svyatov/handrail/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/svyatov/handrail/compare/v0.1.0-rc.1...v0.1.0
[0.1.0-rc.1]: https://github.com/svyatov/handrail/releases/tag/v0.1.0-rc.1
