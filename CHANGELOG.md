# Changelog

All notable changes to this project are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/2.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).
handrail is pre-1.0, so a MINOR bump may break you and a PATCH bump will not.
The public surface is the command set, the rule file format, and the exit codes,
all of which [`docs/spec.md`](docs/spec.md) states.
A command, rule field, or exit code is removed only after at least one release
lists it under Deprecated, and in that release handrail prints a notice naming
its replacement and the earliest version that removes it. The removal of
`handrail advise` in 0.3.0 predates this policy and had no deprecation release.

## [Unreleased]

## [0.4.1] - 2026-09-27

### Changed

- `/handrail:survey`, `/handrail:add` and `/handrail:analyze` recommend an
  answer to each question they ask about a rule and say in one sentence why
  that answer fits.

### Fixed

- `/handrail:survey` replays a `shell` rule from a payload file, since a replay
  command naming the payload tripped the rule it tested. Its Grade 2 question
  fits the four options `AskUserQuestion` takes, and a root-anchored
  `.gitignore` line such as `/dist/` is lifted as `dist/`.

## [0.4.0] - 2026-09-27

### Added

- `handrail --version` and `handrail -v` print what `handrail version` prints.
- `handrail help`, `handrail --help` and `handrail -h` print the usage on
  stdout and exit 0. `handrail help <command>` prints that command's own usage.
- `/handrail:add` offers a pair of rules that block an agent writing a secret
  with a known prefix (AWS, GitHub, Anthropic, Slack, private key headers) into a
  file or a command. handrail still ships no rules: the pair is written only on
  request.

### Changed

- **Breaking:** `-h` or `--help` on any command prints its usage on stdout and
  exits 0, where it printed on stderr and exited 1. `hook` and `import` answer
  with their own usage in place of an empty flag list.

## [0.3.1] - 2026-09-27

### Fixed

- The Homebrew cask removes the quarantine attribute in a `postflight_steps`
  block, so `brew` no longer warns that `postflight` is deprecated.

## [0.3.0] - 2026-09-27

The v2 specification. A rule now reads the input an agent actually produces, and what
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
- `removed_content`, `writes_empty` and `deletes` on file edits.
- Each event tells the human which rules fired. `agent_only: true` keeps a
  coaching `warn` out of the human's channel.
- `handrail survey` prints the repository's signals and instruction files as
  JSON, and the `/handrail:survey` skill proposes rules from them.
  `/handrail:analyze` reads the Decision log where it is on.
- `handrail doctor` fails when handrail's hooks would not run, and reports the
  enforcement state, the log grant and the Examples.
- `handrail test` shows per payload the Candidates, what was unreadable and the
  human line.

### Changed

- **Breaking:** multi-valued matching. A `command` condition reads the command
  as a shell program and asks its question of every command the program runs,
  through Wrappers such as `sudo` and `env` and through literal nested shells,
  so `starts_with: rm -rf /` fires on `cd /tmp && rm -rf /`. The terms on one
  field bind to one Candidate, and a `not_` term fires when some Candidate fails
  it, so `not_starts_with: git` fires on `git status; rm -rf /`. A rule written
  as an allowlist over `command` now fires on calls it used to pass.
- **Breaking:** a shell call yields a `file_read` or `file_edit` payload for
  each file it redirects to or a listed program names, so a `path` rule on
  `.env` now fires on `cat .env` and `> .env` as well as on the file tools.
- **Breaking:** every file of a multi-file Codex `apply_patch` is its own
  payload, so a `path` or `content` rule now reaches the files after the first.
- **Breaking:** the Project-shared tier is add-only against Global. A committed
  rule named like one of your Global rules is dropped and your Global rule
  stands. `check` and `sync` name both files.
- **Breaking:** supply demotion. A `.handrail/local/` that the git index tracks,
  or that is reached through a symlink, is read as Project-shared: it needs
  `handrail trust` and follows the add-only rule.
- **Breaking:** new validation errors. A rule 0.2.0 accepted can now fail
  `check` and `sync`, and be skipped at event time, for a condition on a field
  its event never carries; `kind:` on an event other than `PreToolUse` and
  `PostToolUse`; a `path` glob or `equals` value that lexical cleaning would
  change; or `block` on `PostToolUse`, `SessionStart` or `SessionEnd`, which
  0.2.0 degraded to `warn`.
- **Breaking:** `handrail test` exits 3 when the outcome is `ask`, and still 2
  on `block`.
- **Breaking:** `doctor` fails on a handrail hook entry narrowed by a matcher or
  an `if`, so an entry you narrowed by hand now reports as broken. `sync` still
  writes one entry per event with neither, now for eight events, and writes
  them even when a rule is invalid, then exits 1.
- **Breaking:** `tool` is on every call, where 0.2.0 carried it on MCP calls
  alone, so a `tool` condition with no `kind: mcp` now reaches every tool. The
  spawn tools, `WebFetch`, `WebSearch` and `Monitor` left `kind: other` for
  `agent`, `network` and `shell`, so a `kind: other` rule no longer fires on
  them.
- **Breaking:** `command`, `path`, `content` and `removed_content` are read by
  key presence on any tool, MCP tools included, so a tool handrail does not
  classify, such as one a harness adds after a release, now carries them. A
  kind-less rule on those fields now fires on such a call, and a call that
  carries one of their keys with a non-string value, such as an MCP tool's
  `content` blocks, sets `unreadable`. An unclassified call keeps `kind: other`
  and yields no file payloads, and a file edit's `command` is still read as
  `apply_patch`'s envelope alone. A `file_read` call now reads `path` from
  `notebook_path` when it has no `file_path`.
- `block` on `Stop` and `SubagentStop` continues the agent once, with the rule's
  message as its next instruction, and never loops.
- A failure of handrail's own now sets `unreadable`, so a rule can fail closed
  on it: a missing Global tier, a non-string JSON value and a broken rule file
  are reported to the human and the agent. handrail itself still never blocks
  a call for its own failure.
- `handrail import hookify` imports a stop rule as a `Stop` rule on `response`.
- A command is parsed with `mvdan.cc/sh/v3/syntax`, handrail's one third-party
  runtime dependency.

### Removed

- **Breaking:** `handrail advise` and the Advisor. handrail recommends no native
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

[unreleased]: https://github.com/svyatov/handrail/compare/v0.4.1...HEAD
[0.4.1]: https://github.com/svyatov/handrail/compare/v0.4.0...v0.4.1
[0.4.0]: https://github.com/svyatov/handrail/compare/v0.3.1...v0.4.0
[0.3.1]: https://github.com/svyatov/handrail/compare/v0.3.0...v0.3.1
[0.3.0]: https://github.com/svyatov/handrail/compare/v0.2.0...v0.3.0
[0.2.0]: https://github.com/svyatov/handrail/compare/v0.1.0...v0.2.0
[0.1.0]: https://github.com/svyatov/handrail/compare/v0.1.0-rc.1...v0.1.0
[0.1.0-rc.1]: https://github.com/svyatov/handrail/releases/tag/v0.1.0-rc.1
