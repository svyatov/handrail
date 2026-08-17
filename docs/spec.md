# handrail v1 specification

handrail is a cross-harness guardrail manager: users declare rules once, in one neutral format, and handrail enforces them through each harness's native hook or permission mechanisms. The engine is a performance-critical Go CLI; a thin plugin per harness is the primary UX. v1 harnesses: Claude Code and Codex CLI.

Vocabulary is defined in [CONTEXT.md](../CONTEXT.md); every capitalized term below (Rule, Tier, Adapter, Advisor, Analyzer, Sync, ...) means what the glossary says. Rationale lives in [docs/adr/](adr/); this spec states the outcome and links the ADR. Research evidence lives in `docs/research/` on the `research/*` branches.

## 1. Event model

ADR: [0002](adr/0002-claude-code-event-vocabulary.md).

Canonical event names are Claude Code's, verbatim. The v1 set is the six-event core; the model is versioned, so wider rings can be added without renaming.

| Event | Fires |
|---|---|
| `PreToolUse` | Before a tool call. Can block. |
| `PostToolUse` | After a tool call succeeds. |
| `UserPromptSubmit` | When the user submits a prompt. |
| `SessionStart` | Session begins or resumes. |
| `SessionEnd` | Session terminates. |
| `Stop` | The agent finishes responding. |

### Canonical payload

- Common envelope: `event`, `harness`, `session_id`, `cwd`, `tool_kind`, `tool_name` (raw harness tool name).
- `tool_kind`, assigned by each Adapter: `shell`, `file_edit`, `file_read`, `mcp`, `other`.
- Per-kind normalized fields: `command` (shell); `path` and `content` (file_edit); `path` (file_read); `server` and `tool` (mcp); `prompt` (UserPromptSubmit). `content` is the new content being written (added by ADR [0008](adr/0008-hookify-importer-mapping.md)).
- The raw harness payload rides along untouched under `raw.*`. Conditions cannot address it in v1; it exists so normalization never loses information.
- `transcript_path` is optional, present only where the harness provides it.

### Outcomes

Exactly three. `allow` is the absence of a match, not an Action. `warn` proceeds but injects the rule's message into the agent's context (degrading to a user-visible message where injection is unavailable). `block` denies with the rule's message as the reason. No input rewrite in v1.

## 2. Rule format

ADR: [0003](adr/0003-rule-file-format.md).

A Rule is one markdown file, `<rule-name>.md`. Identity is the filename; there is no `name:` field. Matcher in YAML frontmatter, message as the markdown body:

```markdown
---
event: PreToolUse
kind: shell
action: block
conditions:
  - field: command
    matches: rm\s+-rf
  - any:
      - field: command
        contains: " /"
      - field: command
        starts_with: rm -rf /
---
Never delete recursively from the filesystem root. If you need to clean a
build directory, name it explicitly and stay inside the repo.
```

- **Matcher**: explicit `event:` (six-event core names), optional `kind:` (omitted = all kinds).
- **Conditions**: implicit-AND list; an entry may be a one-level `any:` group for OR; no deeper nesting. Each condition names one canonical payload field with the operator as the key.
- **Operators**: `matches` (RE2), `contains`, `equals`, `starts_with`, `ends_with`, `glob` (supports `**`); a uniform `not_` prefix negates any. RE2 means no lookarounds or backreferences. Case-sensitive by default; regex opts in with inline `(?i)`; string operators have no case flag in v1.
- **Glob dialect**: Go's `path.Match` plus `**`. `*` and `?` never cross `/`; `[...]` is a character class, negated by a leading `^` (`!` is an ordinary member), and `\` makes the next character literal. `**` skips directories only when it is its own path segment: leading or after a `/`. `**/` matches zero directories too, so `**/*.env` covers a file at the repo root; a trailing `**` matches every depth below. Anywhere else, `**` reads as `*`. Patterns are anchored: a glob matches the whole field, not a substring of it.
- **Absent fields**: a condition against a field the event does not carry never matches, in either polarity. `not_ends_with` on `path` says nothing about a shell command that has no path, so it does not fire there. Narrowing by event and `kind:` is how a rule reaches the calls it means to.
- **Actions**: `warn` (default) and `block`. No `ask` in v1.
- **Message**: static markdown, no templating; literal `{{` stays literal.
- **Housekeeping**: `enabled:` defaults to true; no `pattern:` shorthand; no format `version:` field; no sugar aliases.
- **Multiple matches**: every message is delivered labeled with its rule name; one block makes the outcome block.
- **Strictness**: strict at sync (bad YAML, unknown fields, zero or multiple operator keys, invalid regex, unknown event: sync fails with errors). Loud fail-open at event time (an unparseable rule is skipped and the failure is injected into the agent's context, naming the rule). Silent failure is never allowed.

## 3. Tiers, precedence, trust

ADR: [0004](adr/0004-rule-tiers-and-precedence.md).

| Tier | Location | Notes |
|---|---|---|
| Global | `$XDG_CONFIG_HOME/handrail/` (default `~/.config/handrail/`) | XDG on every Unix platform including macOS. |
| Project-shared | `.handrail/` at the repo root | Committed. Gated by trust. |
| Project-personal | `.handrail/local/` | Sync appends the ignore line to `.git/info/exclude` when missing. |

- Project tiers are discovered at the repo root only: `git rev-parse --show-toplevel` from the event's cwd, or the cwd itself outside a repo. No walk-up, no monorepo nesting in v1.
- Precedence is most specific wins: Global < Project-shared < Project-personal. Not a security boundary.
- Scanning is recursive; identity = basename; a duplicate basename within a tier is a sync error; `local/` is excluded from the shared scan.
- A same-named rule in a higher tier **shadows** the lower one wholesale; no field merging. Disabling an inherited rule is a stub shadow containing only `enabled: false`; a disabled rule is exempt from matcher validation, present fields still validate strictly.
- **Merging happens in the engine at event time.** Sync writes exactly one stable hook entry per event into each harness's user-level config and nothing into project config. Sync is per-machine: once synced, every repo with a `.handrail/` directory is enforced.
- **Trust**: an engine-side, path-once registry (XDG state dir) gates the Project-shared tier. In an untrusted repo the shared tier is skipped and a one-line notice pointing at `handrail trust` is injected. Global and Project-personal are never gated.
- Message ordering on multi-match: tier order (Global, Project-shared, Project-personal), then alphabetical within a tier.
- Claude Code cloud sessions read neither user settings nor a local binary: unenforced in v1.

## 4. Architecture

Decided in [the event-model ticket](https://github.com/svyatov/handrail/issues/7); ADR [0002](adr/0002-claude-code-event-vocabulary.md).

One Go binary; the Adapters live in one internal package, `harness`, one value per harness. Both v1 harnesses read the same Claude-shaped hook config and speak the same payload and decision protocol, since Codex's hooks engine is Claude-compatible by design, so they differ by a handful of facts (config path, blockable events, quirks) and a package apiece would be two copies of one Adapter. Sync writes each harness's native hook config: one entry per canonical event, invoking `handrail hook <harness> <event>` with the harness named explicitly. No runtime harness auto-detection. That single invocation evaluates all matching rules across all tiers.

### Capability matrix

Hardcoded in Go per Adapter, next to its translation knowledge. Per-event: exists, can block. Global flags: context injection, transcript access, fail-open on hook error, fail-open on timeout. The v1 matrix:

| Capability | Claude Code (`claude`) | Codex CLI (`codex`) |
|---|---|---|
| Six core events | All six | All six |
| Block on PreToolUse | Yes (exit 2 / deny) | Yes (exit 2 / deny) |
| Block on UserPromptSubmit | Yes | Partial: cannot fail closed before model requests (upstream #33630) |
| Block on Stop | Yes | Yes |
| Context injection | Yes | Yes (`additionalContext`) |
| Transcript access | Yes (`transcript_path`, written asynchronously, tail may lag) | Yes (`transcript_path`) |
| Fail-open on hook error | Yes (documented) | Yes |
| Tool names on the wire | `Bash`, `Edit`/`Write`, `Read`, `mcp__<server>__<tool>` | The same, plus `apply_patch` for every file edit, whose whole edit rides in `tool_input.command` |
| Config sync writes | `~/.claude/settings.json` | `~/.codex/hooks.json`, or `$CODEX_HOME/hooks.json` where that is set |
| Known bypasses | `disableAllHooks`, cloud sessions | an enterprise `allow_managed_hooks_only` requirement. `--dangerously-bypass-hook-trust` skips the trust review rather than the hooks, and `codex exec --ignore-rules` bypasses execpolicy, which costs a promoted rule (section 5) its backstop but leaves the hook path intact |

Codex's hash-based hook trust prompts once per entry change; the stable one-entry-per-event shape (section 3) keeps that to a single approval.

### Degradation

When a harness lacks a capability a rule needs: downgrade to the strongest available action (`block`, then `warn`, then skip), never silently. Reported per harness at sync time and reprintable via `doctor`; the runtime hot path stays silent. Fail-open quirks are reported in the same place.

## 5. Advisor

ADR: [0005](adr/0005-advisor-recommend-only.md).

Recommend-only. The Advisor emits the exact native entry; the authoring skill (or a human running `handrail advise`) applies it on consent; the applied entry becomes the user's own harness config, untracked by handrail. The rule always stays active: the hook path delivers the message, the native entry is the fail-closed backstop.

- **Eligibility**: block rules only, and only provably-safe translations. `starts_with`/`equals` on `command` compiles to `Bash(prefix*)` denies and Codex execpolicy `prefix_rule(decision="forbidden")`; `glob`/`equals` on `path` compiles to `Read`/`Edit` denies. `any:` groups only when every branch independently translates. `matches` (RE2) never translates. Coarse mechanisms (Codex sandbox modes, `approval_policy`) are out of scope.
- ADR 0005 also lists domain conditions compiling to `WebFetch(domain:)` denies. The v1 payload defines no domain or URL field, so this row is dormant: it activates if a canonical URL field ever lands. Recorded here so implementation does not hunt for a field that does not exist.
- All translation knowledge lives in the Go CLI per adapter, kept current by binary releases. The skill contains zero harness-specific pattern knowledge and only relays.
- Advice surfaces at authoring time and via standalone audit, never as a sync nag. Promoting a project-tier rule states the scope widening explicitly. Codex advice carries the `--ignore-rules` bypass caveat.

## 6. CLI surface

ADR: [0006](adr/0006-cli-and-plugin-surface.md). Nine commands; harness identifiers are the harness's binary name (`claude`, `codex`).

| Command | Does |
|---|---|
| `sync` | Validate via `check`'s logic, detect installed harnesses (`~/.claude`, `~/.codex`; `--harness` to scope), write each native config, print the annotated effective ruleset and degradation report. |
| `hook <harness> <event>` | The entrypoint sync installs. Payload JSON on stdin, exit codes per the harness protocol. Visible in help, documented as not for humans. |
| `check` | Strict-validate all tiers; print the annotated effective ruleset (tier, shadowing, disabling per rule). The authoring-time surface: adding a rule needs no re-sync. |
| `test <event> [--kind] [--field key=value]... [--stdin]` | Dry-run: matched rules with tier and action, then the final outcome. Exit 2 on block. |
| `trust` | Grant the current repo's Project-shared tier (path-once registry). |
| `advise [rule-name] [--harness]` | The Advisor. No argument audits every rule in every tier; a rule name scopes to one. |
| `import hookify [path]` | One-shot upstream-hookify converter (section 9). |
| `doctor` | Offline-only: binary location and version; per harness, hook entries present, current, pointing at an existing executable; the current repo's tier discovery, trust state, `local/` exclusion line; rule validity summary. |
| `version` | Version, commit, date (GoReleaser-injected). |

- **`--json`** on `advise`, `check`, `test`:
  - `advise`: array of `{rule, tier, harness, mechanism, entry, location, scope_widening, caveats}`; `entry` is the exact native text to paste; `scope_widening` is null when tier and entry scope match.
  - `test`: `{outcome, matched: [{rule, tier, action, message}]}`.
  - `check`: `{rules: [{rule, tier, event, kind, action, enabled, shadowed_by, path}], errors: [{path, message}]}`.
- **Exit codes**: 0 success; 1 usage, internal, or validation error; 2 reserved for blocked (`test`, and `hook` per harness protocol).
- Deliberately absent: `init`, `add`, `list`, `enable`/`disable`, per-file `validate`, CLI `analyze` (analysis needs an LLM).

## 7. Plugins

ADRs: [0006](adr/0006-cli-and-plugin-surface.md), [0007](adr/0007-analyzer-design.md).

Both v1 harnesses get a native plugin with full parity: an add skill, an analyze skill, and a SessionStart bootstrap hook. Skills are authored once and referenced by both plugin manifests. The Codex plugin uses the handrail repo as its own marketplace (`.codex-plugin/plugin.json`, `hooks/` directory for the bootstrap); its one caveat is Codex's one-time trust review for non-managed plugin hooks.

- **`/handrail:add`**: describe a guardrail in words; the skill writes the rule file, runs `handrail check`, relays `handrail advise --json`.
- **`/handrail:analyze`** (the Analyzer): on demand only; no automatic session-end analysis, no nudge hook. Scope is the current session's transcript via `transcript_path`. It receives `handrail check --json` and proposes **new rules only**, skipping covered behaviors. Each proposal is a complete rule draft with rationale and transcript evidence. Nothing is written without per-rule approval; on approval the skill writes the file, validates with `check`, replays the motivating event through `test` to prove the rule matches its own incident, and relays `advise --json`. Suggested tier: Project-personal by default, Global when clearly project-agnostic; Project-shared stays a manual act. Signal categories (non-exhaustive): explicit user corrections, repeated instructions, manual reverts and interventions, near-miss dangerous actions. Claude Code writes the transcript asynchronously, so the Analyzer tolerates a slightly stale tail. Harnesses without transcript access lack the feature; the fallback is describing the incident to `/handrail:add`.
- **Bootstrap**: the plugin pins a binary version. On miss or pin mismatch, SessionStart downloads the matching GoReleaser artifact for the OS/arch from GitHub Releases, verifies sha256 against the release's `checksums.txt`, installs to `$XDG_DATA_HOME/handrail/bin/handrail` (PATH untouched; sync writes absolute paths into hook entries), and after a fresh install runs `handrail sync` once. A PATH-installed binary (brew, `go install`) is respected and never overwritten. Exec-bit survival and per-arch selection in plugin installs are undocumented upstream: verify empirically during implementation.

## 8. Importer

ADR: [0008](adr/0008-hookify-importer-mapping.md).

`handrail import hookify [path]` converts upstream hookify's `.claude/hookify.*.local.md` files into Project-personal rules (`.handrail/local/`). Upstream semantics were verified against the plugin's engine source.

| Upstream | handrail |
|---|---|
| `event: bash` | `PreToolUse` + `kind: shell` |
| `event: file` | `PreToolUse` + `kind: file_edit` |
| `event: prompt` | `UserPromptSubmit` |
| `event: stop` | `Stop` |
| `all` (default) | Inferred from condition fields; skipped when fields span more than one event |
| `command` | `command` |
| `file_path` | `path` |
| `new_text`, `content` | `content` |
| `user_prompt` | `prompt` |
| `regex_match` | `matches`, with `(?i)` prepended (upstream compiles IGNORECASE), RE2-validated |
| string operators | Verbatim (upstream is case-sensitive already) |
| `pattern:` shorthand | Expanded using upstream's own field inference |

Inexpressible constructs are skipped with a per-rule report naming the reason and the original path; nothing broken is ever written: `old_text`, `transcript`, `reason`, non-RE2 patterns, unmappable `tool_matcher` values, ambiguous `all` rules. The converted filename comes from the upstream `name:` field; an existing target is never overwritten (re-runs are safe); originals are untouched; `enabled: false` survives.

## 9. Repo layout, toolchain, distribution

Decided in [the Go stack research](https://github.com/svyatov/handrail/issues/5); findings on `research/go-cli-stack`.

- Go 1.26; module `github.com/svyatov/handrail`; root `main.go` + `internal/` packages; zero third-party runtime dependencies.
- CLI parsing: stdlib `flag`, one `FlagSet` per subcommand, manual dispatch.
- Cold start: minimal import graph, no work in `init()`, `CGO_ENABLED=0`, `-ldflags "-s -w"`.
- Lint: golangci-lint v2, a curated set rather than `default: all`, configured in `.golangci.yml`. Two linters turn a rule stated in this section into a build failure: `depguard` allows only `$gostd` and this module outside `_test.go` (plus `github.com/rogpeppe/go-internal` inside it), which is the zero-runtime-dependency line above; `forbidigo` forbids `os.Exit` everywhere but `main.go`, which keeps `run()` returning an exit code and therefore testable. Formatters: `gofumpt` and `goimports`.
- Tests: testscript over the compiled binary is the primary seam, `testdata/script/*.txtar`; table-driven subtests under `internal/` only for what a process boundary cannot produce ([ADR 0009](adr/0009-testing-strategy.md)). Commands: `make test` (`go test -race -shuffle=on ./...`) and `make cover`.
- CI: `.github/workflows/ci.yml` on push to `main` and every pull request. The test job runs on ubuntu and macos and fails under 95% statement coverage; the lint job runs on ubuntu. The release build (`CGO_ENABLED=0`, `-ldflags "-s -w"`) runs there too, so a broken one fails on the pull request rather than on the tag.
- Release: GoReleaser v2 (`version: 2`, `homebrew_casks` pipe, `checksums.txt`) on tag push via GitHub Actions; goarch `[amd64, arm64]`, goos `[darwin, linux]` (Windows is a non-goal, section 11).
- Distribution: GitHub Releases + `go install` + Homebrew tap (`svyatov/homebrew-tap`). Install script, winget, scoop, nfpm: only if demand appears.

## 10. Performance budget

No daemon; a plain fast binary (settled during charting). No primary source publishes cold-start numbers for Go CLI stacks, so the budget is structural plus one acceptance bar:

- Structural: the section 9 constraints (stdlib-only runtime, minimal import graph, no `init()` work).
- Acceptance bar: a no-match `handrail hook` invocation (read stdin, discover tiers, parse rules, evaluate, exit) completes in under 50 ms cold on typical developer hardware; single-digit milliseconds expected. Implementation adds a benchmark to verify this before v1 ships. The 50 ms number is an assembly-time call, not a researched constant; tighten it if the benchmark says so.

## 11. Non-goals (v1)

Named so their absence reads as decided, not forgotten:

- Windows (Unix-only v1)
- Harnesses beyond Claude Code and Codex CLI (roadmap, section 12)
- Rule packs or sharing beyond a single repo
- Replay/simulation testing beyond `handrail test`
- Monorepo nesting (walk-up discovery deferred)
- Cloud-session enforcement
- `ask` action, input rewrite, templating, `raw.*` conditions, per-file `validate`, CLI `analyze`
- Sync-managed native entries (the Advisor never owns harness config)

## 12. Roadmap

- **Gemini CLI is the first fast-follow.** Paper check performed at spec assembly, against the 2026-08-15 landscape research: all six core events map onto Gemini's own vocabulary (`PreToolUse` to `BeforeTool`, `PostToolUse` to `AfterTool`, `UserPromptSubmit` to `BeforeAgent`, `Stop` to `AfterAgent`, `SessionStart`/`SessionEnd` verbatim); `BeforeTool` blocks via `decision: deny` or exit 2; hooks are on by default; sync's one-user-level-entry-per-event model fits `~/.gemini/settings.json`; the stdin envelope (`session_id`, `transcript_path`, `cwd`) covers the canonical payload; transcript access enables Analyzer parity. **Conclusion: the Adapter interface accommodates Gemini CLI with no interface changes.** The only new work is adapter-internal (event name map, tool-kind classification, settings.json writer, capability matrix row).
- The Claude-wire family next (Qwen Code, Factory Droid, OpenHands, Augment, Crush, Copilot CLI via aliases): near-zero translation from the primary adapter.
- The JS/TS-shim adapter shape (opencode, Amp, pi): a design ticket of its own when reached; shell hooks do not exist there.
