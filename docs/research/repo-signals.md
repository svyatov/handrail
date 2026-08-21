# Research: which signals in a repo actually imply a guardrail

Resolves issue [#95](https://github.com/svyatov/handrail/issues/95) (T17), part of [#85](https://github.com/svyatov/handrail/issues/85). Snapshot date: 2026-08-21.

Scope: what a *repository* can tell a rule-proposal pass that a *transcript* cannot, and which of those signals survive being written out as a real handrail Rule. D3 replaced rule-pack distribution with repo-aware proposal; F17 established that no surveyed tool does it. This file establishes what there is to propose.

**Method.** Every rule in section 2 was written into `.handrail/local/` of a scratch repo and validated with `handrail check` against a binary built from `main` at `dd28375`, then replayed through `handrail test` against its motivating payload and at least one near-miss. Fifty-odd replays across the set, all as expected; the three known leaks are recorded as leaks rather than hidden. Nothing below is a rule that only exists on paper. The corpora in section 3 were read as raw source from their own repositories via the GitHub API, not from READMEs or blog summaries, by parallel subagents dispatched one per corpus.

## The bar

A signal passes when reading the repo fixes **every literal in the matcher**. "The repo uses ESLint" does not pass on its own; "`eslint.config.js` exists, `package.json` runs `eslint` on `src/`, and the source tree is `src/**/*.ts`" does, because those three facts fill in the two conditions of a real rule and leave nothing to guess. A signal whose implied rule needs a field the payload does not carry, or state the engine does not keep, does not pass no matter how real the signal is.

The bar matters because of the asymmetry the ticket names: a proposal the user rejects costs more trust than a proposal never made. The failure mode is not missing a rule, it is offering a plausible-sounding one that cannot be written, or that fires on the wrong things once it is.

## 1. What a repository reliably yields

Each row is a signal, how it is read, and whether it survived section 2. "Name-only" means the probe is a `stat`, never a file read: lockfiles in particular are read by name and never by content (section 5).

| # | Signal | Read by | Verdict |
|---|---|---|---|
| 1 | Package manifest and which lockfile sits beside it | name-only stat of 16 candidates | passes |
| 2 | Generated or vendored trees named in `.gitignore` | `.gitignore`, 241 B here | passes |
| 3 | Configured linter and the source layout it covers | name-only stat + `package.json` scripts | passes |
| 4 | Task wrapper and what its targets expand to | `Taskfile.yml` / `Makefile` / `justfile` / `package.json` | passes |
| 5 | Git hooks actually installed | name-only stat of `.husky/`, `.pre-commit-config.yaml`, `lefthook.yml` | passes |
| 6 | Test file naming convention and framework | one targeted glob + manifest devDeps | passes |
| 7 | CI workflows exist | glob `.github/workflows/*.y*ml` | passes |
| 8 | Every `uses:` in those workflows is SHA-pinned | reading the workflow files | passes |
| 9 | `.env` present or gitignored | name-only stat + `.gitignore` | passes |
| 10 | Key material extensions present or gitignored | targeted globs (`**/*.pem`, `**/id_rsa`, ...) | passes |
| 11 | Release is triggered by a tag push, and the package is publishable | release workflow `on:` block + manifest | passes |
| 12 | `.handrail/` exists | name-only stat | passes |
| 13 | Workspace manifest | name-only stat of `pnpm-workspace.yaml`, `go.work`, `turbo.json` | passes |
| 14 | A constraint stated in repo prose | `CLAUDE.md`, `AGENTS.md`, `CONTRIBUTING.md` | passes |
| 15 | Terraform state in the tree | targeted glob `**/*.tfstate` | passes |
| 16 | `.mcp.json` naming a production server | reading `.mcp.json` | passes |
| 17 | Migrations directory | targeted glob | passes at `warn` only |
| 18 | Protected branch and the default branch's name | `gh api .../branches/<b>/protection` | **rejected** |
| 19 | Formatter configured | name-only stat | **rejected** |
| 20 | The project's own check command, as a precondition for committing | manifest scripts | **rejected** |
| 21 | Runtime or toolchain version pin | `.nvmrc`, `go.mod`, `.ruby-version` | **rejected** |
| 22 | Coverage threshold in CI | workflow files | **rejected** |
| 23 | `.gitignore` translated wholesale into path globs | `.gitignore` | **rejected** |
| 24 | Monorepo package boundaries, as per-package rules | workspace manifest | **rejected** |
| 25 | Whether a path being edited already exists | not readable from any field | **rejected** |
| 26 | Shell-side access to secret files | derived from signal 9 | **rejected** |
| 27 | Filename casing convention | targeted glob over the source tree | **rejected** |
| 28 | Dependency freshness / package publish date | not in the repo at all | **rejected** |
| 29 | Whole-file predicates (license header, import order) | reading source files | **rejected** |
| 30 | Changelog discipline | `CHANGELOG.md` + git history | **rejected** |

## 2. The rules those signals imply

Nineteen rules, from seventeen signals. Each is shown as the file that landed on disk. Frontmatter conformance is stated per rule against the format's actual surface: five frontmatter keys (`event`, `kind`, `action`, `conditions`, `enabled`, enumerated at `internal/rule/rule.go:131-169`), six events, five kinds, six canonical fields, six operators each negatable with a `not_` prefix (`internal/rule/rule.go:107`).

### 2.1 Lockfile identity

`package-lock.json` present with no `yarn.lock`, `pnpm-lock.yaml`, or `bun.lockb` beside it. Both facts are name-only stats. This is the cheapest high-yield signal in the set: it fixes the literal in two different rules.

```markdown
---
event: PreToolUse
kind: file_edit
action: block
conditions:
  - field: path
    glob: "**/package-lock.json"
---
Never hand-edit the lockfile. Change `package.json` and run `npm install`.
```

```markdown
---
event: PreToolUse
kind: shell
action: block
conditions:
  - any:
      - field: command
        starts_with: "yarn "
      - field: command
        starts_with: "pnpm "
---
This repo is on npm: `package-lock.json` is the only lockfile. Use `npm`.
```

Replays: `path=/Users/x/repo/package-lock.json` matches, `path=package-lock.json` matches, `path=/Users/x/repo/package.json` does not. `command=yarn add left-pad` blocks.

Two things this pair demonstrates that generalize to the whole set. First, the `**/` prefix is **mandatory**, not stylistic: `path` arrives verbatim from the harness (`file_path` for Claude Code's `Edit`/`Write`, an extracted relative path for Codex's `apply_patch`, `internal/harness/harness.go:108-121`), so it is absolute on one harness and relative on the other. Globs are anchored to the whole field (spec section 2), and `**/` matches zero directories too, so `**/package-lock.json` is the one spelling that covers both forms. A bare `package-lock.json` would match only the Codex shape.

Second, the shell half leaks. `command=cd web && yarn add left-pad` does **not** match, confirmed by replay. That is C2, not a bug: no operator tokenizes. The file_edit half has no equivalent leak because `path` is a single value, not a sentence.

### 2.2 Generated trees named in `.gitignore`

```markdown
---
event: PreToolUse
kind: file_edit
action: block
conditions:
  - field: path
    glob: "**/node_modules/**"
---
`node_modules/` is generated and gitignored. Change the dependency, not its
installed copy.
```

The directory name comes from `.gitignore`; `target/`, `vendor/`, `dist/`, `_build/` are the same rule with one literal changed. Note what is *not* claimed here: this is not a translation of `.gitignore` into globs, which is rejected in section 2.20. It is one bare directory name lifted out of it.

### 2.3 Linter config plus source layout

The ticket's own test case. "The repo uses ESLint" implies this and nothing broader:

```markdown
---
event: PreToolUse
kind: file_edit
action: block
conditions:
  - field: path
    glob: "**/src/**/*.ts"
  - field: content
    contains: eslint-disable
---
Fix the lint violation rather than disabling the rule.
```

This is `forbidContentPattern({ match: 'eslint-disable', ... })` from `nizos/probity`'s own `probity.config.ts`, scoped by its `files: ['src/**', 'test/**']` block, restated in handrail's format. It is a **suppression** rule, not a **bypass** rule, and that distinction is the whole answer to the ticket's question. The repo tells you the linter's identity and its scope; the linter's *findings* are not in any payload field, so "block edits that fail lint" cannot be written (section 2.19). "Block edits that add a suppression comment" can, because the suppression is literal text in `content`.

`content` is the edit's new text, not the resulting file: `internal/harness/harness.go:110` maps `content`, `new_string`, and `new_source` onto it. So this rule fires on an edit that *introduces* `eslint-disable`, and stays quiet on an edit to a file that already has one. That is the behaviour you want, and it is worth stating in the message when T18 proposes it.

Replay confirms the absent-field rule too: a `file_edit` payload carrying only `path` and no `content` does not match, in either polarity (spec section 2, "Absent fields").

### 2.4 Task wrapper with a known expansion

`Taskfile.yml` defines `test: go test -race -shuffle=on ./...`. Both the wrapper's name and its expansion are repo facts, which is what makes the message specific enough to be obeyed rather than argued with:

```markdown
---
event: PreToolUse
kind: shell
action: warn
conditions:
  - field: command
    starts_with: go test
---
Run `task test`, which is `go test -race -shuffle=on ./...`. A bare `go test`
skips the race detector and shuffling, so a pass proves less than CI's pass.
```

Same C2 leak as 2.1 on compound commands. `warn` rather than `block` is deliberate and is a taste call, not a signal call (section 4).

### 2.5 Git hooks actually installed

```markdown
---
event: PreToolUse
kind: shell
action: block
conditions:
  - any:
      - field: command
        contains: "--no-verify"
      - field: command
        matches: git\s+commit\s+(\S+\s+)*-\w*n
---
This repo installs pre-commit hooks under `.husky/`. Never skip them.
```

This is the sharpest example of a signal *gating* a rule that looks universal. With no `.husky/`, `.pre-commit-config.yaml`, or `lefthook.yml`, `--no-verify` is a no-op and the rule is pure noise. The signal does not supply the pattern, it supplies the *justification*, and a proposal pass that skips the stat proposes a rule that guards nothing.

`contains` rather than `starts_with` is also deliberate: it survives `cd x && git commit --no-verify`, where 2.1 and 2.4 do not. Where a distinctive flag exists, `contains` on the flag is strictly better than `starts_with` on the verb. Replays: `--no-verify` matches, `git commit -n -m wip` matches via the second branch, `git commit -m wip` does not.

### 2.6 Test naming convention plus framework

```markdown
---
event: PreToolUse
kind: file_edit
action: block
conditions:
  - any:
      - field: path
        glob: "**/*.test.ts"
      - field: path
        glob: "**/*.spec.ts"
  - field: content
    matches: \b(describe|it|test)\.only\(
---
Never commit a focused test: `.only` silently skips the rest of the suite.
```

Both literals are read: the glob from the actual test file names on disk, the identifiers from the framework in devDependencies (`describe`/`it`/`test` for vitest and jest; `_test.go` and `t.Skip` for Go; `spec/**/*_spec.rb` and `:focus` for RSpec). This rule is a good illustration of an `any:` group and a plain condition ANDing correctly: the group is one entry of the implicit-AND list, which is the only nesting the format allows.

### 2.7 and 2.8 CI workflows

```markdown
---
event: PreToolUse
kind: file_edit
action: warn
conditions:
  - field: path
    glob: "**/.github/workflows/*.yml"
---
You are editing CI. Say what you changed and why before you change it.
```

```markdown
---
event: PreToolUse
kind: file_edit
action: block
conditions:
  - field: path
    glob: "**/.github/workflows/*.yml"
  - field: content
    matches: uses:\s+\S+/\S+@v\d
---
Every action in this repo is pinned to a full commit SHA. Pin the new one too.
```

The second is only proposable after reading the workflows and finding that every existing `uses:` already carries a 40-hex SHA. That observation is cheap (the workflow files total 5.3 KB here) and it converts a security preference into a repo fact: the convention is already in force, the rule just stops it being broken. Replays: `uses: actions/checkout@v5` blocks, `uses: actions/checkout@08c6903...` does not, and both fire the broader warn.

### 2.9 and 2.10 Secret and key material

```markdown
---
event: PreToolUse
action: block
conditions:
  - any:
      - field: path
        glob: "**/.env"
      - field: path
        glob: "**/.env.*"
  - field: path
    not_glob: "**/.env.example"
  - field: path
    not_glob: "**/.env.sample"
  - field: path
    not_glob: "**/.env.template"
---
`.env` holds live credentials and is gitignored. Never read it or write it.
Use `.env.example` to learn the variable names.
```

The three `not_glob` exceptions are not decoration. Two independent corpora say a bare `.env.*` matcher is too tight: cc-safety-net's `allow_paths` comment names "a repo's `.env.test`, a fixtures directory", and `protect-secrets.js` ships a seven-entry `ALLOWLIST` ahead of its `.env` matcher. This is the format's answer to a lookahead: an `any:` group establishes the positive set, and negated conditions ANDed after it carve exceptions out. Replayed at eight paths, eight as expected: `.env`, `.env.local`, `.env.production` and a bare relative `.env` all block; `.env.example`, `.env.sample`, `.env.template` and an unrelated `config.json` do not.

```markdown
---
event: PreToolUse
action: block
conditions:
  - any:
      - field: path
        glob: "**/*.pem"
      - field: path
        glob: "**/*.key"
      - field: path
        glob: "**/id_rsa"
---
Key material. Never read or write it.
```

Both omit `kind:` on purpose, which is the idiom worth naming for T18. A kind-less rule covers `file_edit` and `file_read` in one file, and is automatically inert on `shell` and `mcp` calls because `path` is absent there and an absent field never matches in either polarity. Writing two rules, or one rule per kind, buys nothing. Verified by replay: `file_read` on `/r/.env` blocks, `file_read` on `.env` blocks, `file_read` on `/r/.env.local` blocks, and a `not_glob` probe on a `shell` payload does not fire.

The shell half of this protection is rejected: see section 2.24.

### 2.11 Tag-triggered release

```markdown
---
event: PreToolUse
kind: shell
action: block
conditions:
  - any:
      - field: command
        contains: "git push --tags"
      - field: command
        matches: git\s+tag\s+(-\w+\s+)*v?\d
      - field: command
        starts_with: npm publish
---
A tag push publishes a release: `.github/workflows/release.yml` triggers on
`push: tags`. Releases are cut by hand from a release branch.
```

The signal is the workflow's `on: push: tags:` block plus a manifest that is not `"private": true`. Without the workflow, a tag is bookkeeping; with it, a tag is a publish, and that is the whole difference between a rule worth proposing and a rule that annoys. Replays: `git push --tags` blocks, `git tag -a v1.0.0 -m release` blocks, bare `git tag` (which only lists) does not, `npm publish` blocks.

### 2.12 The guardrails themselves

```markdown
---
event: PreToolUse
action: block
conditions:
  - field: path
    glob: "**/.handrail/**"
---
The guardrails are not yours to edit. Ask the user to run `/handrail:add`.
```

Signal: `.handrail/` exists. Precedent in the field is unanimous. Kiro hardcodes it ("writes to `.kiro/settings/` always denied", https://kiro.dev/docs/permissions.md). cc-safety-net ships it as an always-on guard, `src/guards/policy-protection.ts:27`: `'This path contains the protected policy config and you must not modify or delete it.'` probity does it with native Claude Code denies in its own `.claude/settings.json`: `"Edit(**/probity.config.ts)"`, `"Write(**/probity.config.ts)"`, `"Edit(**/.claude/settings*.json)"`, `"Write(**/.claude/settings*.json)"`. Under D1 this is the rule with the most adversarial value in the set, and the only one where the `**/` glob form matters for a reason other than harness portability: probity's spelling is the same shape.

The shell companion (`rm -rf .handrail`) does not exist and cannot be made reliable, same reason as 2.24. Confirmed by replay: `command=rm -rf .handrail` matches nothing.

### 2.13 Workspace manifest

```markdown
---
event: PreToolUse
kind: shell
action: block
conditions:
  - field: command
    starts_with: "pnpm add "
  - field: command
    not_contains: "--filter"
---
This is a pnpm workspace. Name the package: `pnpm add --filter <pkg> <dep>`.
```

This is the one rule in the set that needs the `not_` prefix for a structural reason rather than a stylistic one. The natural expression is a negative lookahead, and RE2 has none; two ANDed conditions on the same field, one positive and one negated, is the substitute. Worth teaching in `skills/add/SKILL.md`, because an author who reaches for `(?!...)` gets a validation error with no hint of the workaround.

### 2.14 and 2.15 A constraint the repo states in prose

The highest-precision signal in the whole set, and the only one where consent is already on disk. This repo's own `CLAUDE.md` says "`os.Exit` only in `main.go`, so `run()` returns an exit code and stays testable":

```markdown
---
event: PreToolUse
kind: file_edit
action: block
conditions:
  - field: path
    glob: "**/*.go"
  - field: path
    not_glob: "**/main.go"
  - field: content
    contains: os.Exit(
---
`os.Exit` lives only in `main.go`, so `run()` returns an exit code and stays
testable. `forbidigo` fails the build on this.
```

Three conditions, all ANDed, two on the same field in opposite polarities. Replays: `path=/r/internal/rule/rule.go` with `content=os.Exit(1)` blocks; `path=/r/main.go` with the same content does not.

The second, from probity's dogfooded config, is the one that does **not** port verbatim:

```markdown
---
event: PreToolUse
kind: file_edit
action: block
conditions:
  - field: path
    glob: "**/*.md"
  - field: content
    matches: "[\\p{So}\\x{1F300}-\\x{1FAFF}\\x{2190}-\\x{21FF}\\x{FE0F}]"
---
No emoji in documentation.
```

probity writes `/\p{Extended_Pictographic}/u`. Handed to handrail unchanged, `check` fails:

```
handrail: .../no-emoji-in-docs.md: line 9: invalid regexp: error parsing regexp: invalid character class range: `\p{Extended_Pictographic}`
```

RE2 supports Unicode general categories and script names, not derived properties, so `Extended_Pictographic` does not exist for it. The character class above is a working approximation, verified to match a rocket emoji and to leave plain ASCII alone. This is a concrete portability finding: importing a rule from another tool's corpus is a translation, not a copy, and the translation can silently narrow.

### 2.16 Terraform state

```markdown
---
event: PreToolUse
kind: shell
action: block
conditions:
  - any:
      - field: command
        contains: terraform apply
      - field: command
        contains: terraform destroy
---
This repo carries Terraform state. Plan only; a human applies.
```

Replays: `terraform destroy` blocks, `terraform plan` does not.

### 2.17 `.mcp.json` naming a production server

The only rule in the set that reaches the `mcp` kind and the `server`/`tool` fields, and the only one whose signal is a *config value* rather than a file's existence:

```markdown
---
event: PreToolUse
kind: mcp
action: block
conditions:
  - field: server
    equals: prod-db
  - field: tool
    not_starts_with: read_
---
`.mcp.json` points `prod-db` at the production database. Reads only; nothing
that writes, migrates, or drops.
```

`server` and `tool` come from splitting `mcp__<server>__<tool>` (`internal/harness/harness.go:122-127`), so the server name in `.mcp.json` is exactly the literal the rule needs. The tool names are not in `.mcp.json` (the server supplies them at connect time), which is why the second condition is a negated prefix over a naming convention rather than an enumeration. Replays: `server=prod-db tool=execute_sql` blocks, `server=prod-db tool=read_query` does not, `server=staging-db tool=execute_sql` does not.

### 2.18 Migrations directory, at `warn` only

```markdown
---
event: PreToolUse
kind: file_edit
action: warn
conditions:
  - field: path
    glob: "**/db/migrate/**"
---
An applied migration is immutable. If this one has run anywhere, write a new
migration instead of editing this file.
```

The signal wants a block on *modifying* an existing migration while allowing a *new* one. No canonical field says whether the path already exists, and no operator can ask the filesystem, so the two cases are indistinguishable. `warn` is the honest downgrade: the message can say the thing the matcher cannot.

### What the passing set does and does not exercise

Counted across all nineteen rules, the repo-signal space touches a strikingly narrow part of the format:

| Format surface | Used by the passing set |
|---|---|
| Events (6) | `PreToolUse` only. **1 of 6.** |
| Kinds (5) | `shell`, `file_edit`, `mcp`, plus three rules with no `kind:` at all. `file_read` never appears alone, `other` never appears. |
| Fields (6) | `command`, `path`, `content`, `server`, `tool`. `prompt` never appears. **5 of 6.** |
| Operators (6, each negatable) | `glob`, `not_glob`, `contains`, `not_contains`, `starts_with`, `not_starts_with`, `matches`, `equals`. `ends_with` and its negation never appear, subsumed by `glob`; `not_matches` and `not_equals` never appear. |
| Frontmatter keys (5) | `event`, `kind`, `action`, `conditions`. `enabled:` never appears, because a proposal that lands disabled is not a proposal. |

The single-sentence version, and probably the most useful thing this ticket produces for T18:

> **A repo-derived proposal is always a `PreToolUse` rule whose conditions are over `command`, `path`, or `content`.**

The reasoning holds for the other five events by construction, and each was probed:

- `PostToolUse` carries the same fields but cannot block (spec section 4), so every repo-derived rule on it degrades to a nag. A `PostToolUse` rule with `action: block` validates and `handrail test` even prints `block` for it, which is worth knowing when T18 verifies a proposal: `test` reports the rule's action, not the harness-degraded outcome.
- `UserPromptSubmit` carries only `prompt`. A repository says nothing about what its user will type.
- `SessionStart`, `SessionEnd`, and `Stop` carry no matchable field at all. A rule on them can only be unconditional, and it validates: a `SessionStart` rule with no `conditions:` key passes `check` and warns on every single session. That is a fine mechanism for a bootstrap notice and a terrible one for a guardrail, and no repo signal produces one.

## 3. What the field already ships

Four corpora, read as source.

| Corpus | Built-in rules | Derived by reading the guarded repo | Reads the repo at all? |
|---|---|---|---|
| kenryu42/cc-safety-net | 60 hardcoded ids | 0 | resolves `.git` and cwd at runtime, for `rm -rf` classification |
| Dicklesworthstone/destructive_command_guard | 99 packs, several hundred rules | 0 | no; the user enables vendor-scoped packs by hand |
| karanb192/claude-code-hooks | ~130 across 6 guard plugins | 0 | `git branch --show-current`, compared to a hardcoded list |
| nizos/probity | 0 built-in rules, 5 rule factories | n/a, everything is user-authored | no |

The headline is uniform and quantitative: **no tool in the field derives a rule by reading a repository, and the count of built-in repo-derived rules across all four corpora is zero.** Every repo-specific rule that exists in the field is one a human wrote by hand or one a human opted into by hand. The closest anything comes is `destructive_command_guard`, which ships ninety-nine vendor-scoped packs and makes the user enable the ones their repo needs. That is the same question this ticket asks, answered with a config file instead of a read.

### kenryu42/cc-safety-net

Sixty built-in rule ids, all hardcoded TypeScript, all `deny` (there is no warn and no ask: `src/ir/decision.ts:14-22`). Twenty-five git rules (`git.reset-hard`, `git.push-force`, `git.clean-force`, `git.stash-clear`, ...), seven `rm` rules, nine PowerShell equivalents, six find/device rules (`find.delete`, `dd.device-write`, `mkfs.device`, `shred.target`), thirteen execution/dynamic rules (`interpreter.dangerous-command`, `awk.system-dynamic`, `xargs.rm-recursive-force-dynamic`, `shell.dynamic-executable`), plus a large secret-protection tier of exact basenames (`.env`, `.npmrc`, `.netrc`, `.git-credentials`, `id_rsa`, ...), home paths (`~/.ssh`, `~/.aws`, `~/.kube/config`), and extensions (`pem`, `p12`, `pfx`, `jks`, `keychain`, ...).

Not one of them references a package manager, lockfile, linter, test command, CI file, or project path. The only project-awareness is *dynamic* rather than derived: `.git` is resolved from the live filesystem at runtime and `rm -rf` is classified relative to the session cwd. The single bundled user-facing rule, written by `rule init --example`, is a `docker system prune` block (`src/rules/policy/config-file.ts:73-104`).

Two findings matter beyond the inventory.

**The `tests:` block is not run.** The map issue's "Not yet specified" section records cc-safety-net's `tests:` block as "run during `check`". It is not. `src/cli/rule/doc.ts:118` states it flatly: "Fixtures are optional documentation of intended behavior. Fixtures are shape-validated only; CC Safety Net does not execute them." `skills/cc-safety-net/SKILL.md:43` repeats it. Validation only checks that `command` is a non-empty string, `expect` is `blocked`/`allowed`, and a `blocked` fixture names a rule that exists in the rulebook. This should be corrected on #85 before that idea is ticketed, because "rule files carrying their own executable fixtures" is currently attributed to a tool that does not do it. Handrail's `test` command already runs them properly, which is a real differentiator rather than a gap.

**The one existing precedent for repo inspection is a prompt instruction, not a feature.** `skills/cc-safety-net/SKILL.md:23`: "Inspect relevant project files only when the user asks for rule suggestions or the requested rule depends on project context. Look at manifests, scripts, task runners, CI, infrastructure, database, migration, and deployment files that explain risky commands." That is an unimplemented sketch of exactly this ticket's read list, sitting in a skill with no rule corpus behind it. It refines F17 rather than contradicting it: the *idea* exists in the field, nothing has been built on it.

Third, a signal used backwards. `src/policy/allow-paths.ts:48-55` comments on `secret_protection.allow_paths` (default `[]`): "Allow entries vouch for paths the user manages themselves (a repo's `.env.test`, a fixtures directory)." That is a repo signal implying a **relaxation**, not a restriction. Section 2.9's `.env.*` glob would block `.env.test` and `.env.example`; a proposal pass that reads only the deny direction proposes rules that are immediately too tight. T18 should read fixture and example directories for the same reason it reads `.gitignore`.

### Dicklesworthstone/destructive_command_guard

By far the largest corpus surveyed: **99 packs across 27 categories**, several hundred rules. The built-in packs are Rust source compiled into the binary (`src/packs/**`), not data files; only user-authored external packs use the YAML schema at `docs/pack.schema.yaml`. The auto-generated reference under `docs/packs/*.md` prints reasons and severities but omits destructive regexes, so the source is the only complete record.

The relevant structure is what dcg does *instead of* reading a repo. `README.md:190-215` makes exactly five packs default-on: `core.filesystem` and `core.git` (always on, cannot be disabled), `system.disk`, plus `windows.filesystem` and `windows.system` on Windows. All 94 others are opt-in. Sorting the corpus by "could you justify this rule without having read a particular repo" reproduces that split almost exactly:

- **Universal, default-on.** `core.filesystem` (28 rules: `rm -rf` on root or `$HOME`, `find -delete`, `unlink`, `truncate`, `shred`, `tar --remove-files`, `dd`, redirect-truncate onto `/etc|/usr|/bin|/sbin|/root|/boot|/lib|/var|/home|/Users|/sys|/proc|/dev|/opt`, fork bomb). `core.git` (14: hard reset, `clean -fdx`, force push, `reflog expire`, stash drop). `system.disk` (42: `mkfs`, `dd of=/dev/`, `wipefs`, LVM removes, `zpool destroy`).
- **Repo-scoped, opt-in.** `package_managers` (every rule names a specific tool: `cargo-publish`, `pip-uninstall`, `gradle-publish`), `infrastructure.terraform`/`pulumi`/`ansible`/`atmos`, `cicd.github_actions`/`gitlab_ci`/`jenkins`/`circleci`, `database.*` (8 engines), `containers.*`, `kubernetes.*`, `cloud.*`, `platform.*`, and eighteen further vendor categories. Which of these applies is *entirely* determined by files in the repo.

So the answer dcg gives to this ticket's question is: ship the repo-specific rules, and make the human turn them on. That is a real design choice with a real cost (the user must know that `infrastructure.terraform` exists), and it is the alternative D3 rejected. Worth naming as the road not taken.

Three findings bear directly on section 2's verdicts.

**`strict_git` ships both `push-main` and `push-master`, because it cannot know which.** Sixteen rules, all High, in `src/packs/strict_git/mod.rs`:

```
"push-master"  r"git\s+(?:\S+\s+)*push\s+(?:.*[\s:/])?\+?master(?:\s|$)"
"push-main"    r"git\s+(?:\S+\s+)*push\s+(?:.*[\s:/])?\+?main(?:\s|$)"
```

This is independent corroboration of rejection 1, from the opposite direction. dcg *does* want a default-branch rule and cannot express one without the repo, so it ships both spellings and matches the literal branch name in the command text. Note what that costs: the pattern only fires when the branch is named explicitly on the command line, so a bare `git push` on `main` passes both rules. The signal is genuinely unavailable, and the workaround is genuinely partial. Rejection 1 stands, and now has a precedent.

Also in that pack, corroborating section 2.1: `"add-all-dot"` matches `git add .` with the reason "git add . stages everything including secrets, .env files, and build artifacts", which is a `.gitignore` concern being handled by a pattern that cannot see `.gitignore`.

**The bypass-protection pack independently reaches section 2.12's rule.** `careful_company_running_windows.guardrails` is an anti-tamper pack aimed at the agent, and its keyword list literally enumerates agent config directories: `.claude`, `.codex`, `.cursor`, `.gemini`, `.copilot`, plus `settings.json`, `hooks.json`, `DCG_BYPASS`. Four of its rules:

| name | severity | reason |
|---|---|---|
| `dcg-bypass-or-uninstall` | critical | Bypassing or uninstalling dcg removes the guard that is supervising this session. |
| `dcg-policy-self-weakening` | critical | Granting an allowlist exception or overriding pack/policy config lets the agent clear its own path. |
| `agent-hook-config-tamper` | high | Editing or deleting the agent's hook configuration can silently remove dcg's protection. |
| `agent-hook-config-overwrite` | high | Copying a file ONTO the agent's hook configuration replaces it and can silently remove dcg's protection. |

cc-safety-net, probity, dcg, claude-code-hooks, and Kiro all protect their own config, which makes section 2.12 the single most corroborated rule in the passing set. `agent-hook-config-overwrite` also names a bypass the `path`-glob approach misses in the same way rejection 9 describes: `cp x .handrail/local/y.md` is a shell command, not a `file_edit`, so no `path` rule sees it.

**The untrusted-repo trust boundary is stated three times and enforced in code.** `docs/configuration.md:14-32`:

> The automatically discovered `.dcg.toml` at a repository root is not a normal precedence layer. Opening a newly cloned repository must not give that repository authority over the user's security policy. Automatic discovery therefore accepts only settings that monotonically add enforcement.

The permitted set is six keys: `packs.enabled`, `policy.default_mode = "deny"` and per-pack or per-rule entries equal to `"deny"`, `general.fail_closed = true`, and three heredoc-strictness settings. `src/config.rs:677-770` implements it as an explicit allowlist whose doc comment says "new config fields are denied by default until their monotonic safety has been reviewed", with two named tests. `README.md:715-724` spells out what is refused: "allow overrides, pack disables, custom pack paths, custom regex overrides (including block regexes), resource limits, language filters, agent profiles, nested project overrides, and per-rule target-path exemptions". `DCG_CONFIG=.dcg.toml` is the explicit user-side opt-in to trusting the whole file.

Handrail already lands in the right place here by construction: Project-shared rules are trust-gated, and there is no repo-writable knob that can *disable* a Global rule, only a shadowing stub the user has to create. But the framing is worth borrowing verbatim for T18, because a repo-derived proposal pass is a new path by which repository content influences policy. The property to preserve is that the repo supplies *evidence*, and the user supplies *authority*. dcg gets there with a config allowlist; handrail gets there with per-rule approval. Same invariant, different mechanism, and worth saying out loud in the skill.

One last observation on format. dcg's shipped external-pack example is not a general rule at all:

```yaml
id: example.deployment
destructive_patterns:
  - name: prod-direct-deploy
    pattern: \bdeploy\s+(?:--env\s*[=\s]?\s*|--environment\s*[=\s]?\s*)prod(?:uction)?\b
    severity: critical
```

A deploy-environment policy, living in `examples/` rather than in the binary, precisely because it is per-organization. That is the same instinct that puts sections 2.4, 2.11 and 2.16 in Grade 3 below.

### karanb192/claude-code-hooks

Six guard plugins read as source: `block-dangerous-commands`, `git-safety`, `config-guard`, `protect-secrets`, `protect-tests`, `format-code`. About 130 rules between them, each an entry in a hardcoded JavaScript array with a `level` of `critical`, `high`, or `strict` selected by a `HOOK_SAFETY_LEVEL` dial.

**Repo-derived rules: zero of about 130.** Not "few". Zero. Every literal in all six files is a POSIX or git or `gh` or docker command name, a well-known credential filename, an ecosystem-wide test convention, or Claude Code's own config layout. No lockfile name, no project source directory, no linter config, nowhere. The only filesystem reads in the six plugins are the hook's own log directory, an existence check on the file just edited, and symlink resolution of a target path.

That is F17's clearest confirmation, and three of the plugins fail in ways that are directly instructive.

**`git-safety.js` is rejection 1, implemented, with its costs visible.** It hardcodes the branch list and runs a git command for the current branch:

```js
const PROTECTED_BRANCHES = ['main', 'master'];
```
```js
function getCurrentBranch() {
  try {
    return execFileSync('git', ['branch', '--show-current'], { encoding: 'utf-8' }).trim();
  } catch { return ''; }
}
```
```js
    if (p.branchOnly) {
      if (!branch) branch = getCurrentBranch();
      if (!PROTECTED_BRANCHES.includes(branch)) continue;
    }
```

There is no `git symbolic-ref refs/remotes/origin/HEAD`, no `gh api ... default_branch`, and no config for the list. Two consequences, both of which handrail would inherit and cannot even mitigate: a repo whose default branch is `develop` or `trunk` gets no protection at all, and the by-name rule `/\bgit\s+push\b.*\bmain\b/` fires on a feature branch called `main-fix`. Note that the working half of this plugin depends on **running a command at evaluation time**. Handrail's matcher is a pure predicate over payload fields; it does not shell out, and adding that would be a much larger design change than a new field. Rejection 1 is confirmed twice over now: dcg cannot express it and ships both spellings, claude-code-hooks expresses it only by executing git.

**`format-code.js` is rejection 2 from the acting side.** Even a plugin whose whole job is to run the formatter does not detect which one the repo uses:

```js
const PRETTIER_EXTS = new Set(['.js', '.ts', '.json', '.md', '.yaml', '.yml', '.html']);
const FORMATTERS = { '.py': (fp) => [ ['uv','run','ruff','check','--fix','--exit-zero','--quiet',fp], ['uv','run','ruff','format','--quiet',fp] ] };
```

A literal extension-to-command table. It never looks for `.prettierrc`, `pyproject.toml`, `ruff.toml`, `.editorconfig`, or a `format` script in `package.json`. `npx --yes prettier` will silently fetch prettier from the network in a repo that does not have it. The formatter signal is readable and nobody reads it, because the action it implies is "run this program", which is a different product from a matcher.

**`protect-tests.js` is the best argument in the whole survey *for* signal 6.** It decides what a test file is from a fixed convention regex:

```js
const TEST_PATH = new RegExp([
    '(^|/)(tests?|__tests__|spec|specs)/',
    '(^|/)test_[^/]+\\.py$',
    '_test\\.(py|go|rb|js|jsx|ts|tsx|mjs|cjs)$',
    '\\.(test|spec)\\.(js|jsx|ts|tsx|mjs|cjs)$',
    '(^|/)[^/]+_spec\\.rb$',
    '(^|/)[^/]*Test\\.(java|kt|cs)$',
    '(^|/)Test[^/]*\\.(java|kt|cs)$',
].join('|'), 'i');
```

Run that against **this** repository and it misses the primary test seam entirely: handrail's testscript cases live in `testdata/script/*.txtar`, which matches none of the seven alternatives. The same holds for a repo using `t/`, `features/`, or `*_test.exs`. A shipped universal pattern is a guess about test layout; one targeted glob over the actual tree is a fact. This is the concrete case where the repo-derived rule is not merely as good as the distributed one, it is strictly better, and it is exactly the argument D3 needs.

Two smaller corroborations. `config-guard.js` protects the agent's own configuration as its entire premise, with `.claude/settings.json`, `.claude/hooks/`, `hooks.json`, `.mcp.json`, `.claude-plugin/`, `CLAUDE.md`, and `.claude/{rules,agents,commands}/`, reads always allowed and only mutations blocked. That matches section 2.12's design decision (reads are fine, writes are not) and is the fifth independent instance of the pattern. And `protect-secrets.js` ships an explicit allowlist ahead of its `.env` matcher:

```js
const ALLOWLIST = [/\.env\.example$/i, /\.env\.sample$/i, /\.env\.template$/i,
                   /\.env\.schema$/i, /\.env\.defaults$/i, /env\.example$/i, /example\.env$/i];
```

Two independent corpora now say the same thing about section 2.9: a bare `**/.env.*` is too tight, and the exceptions are predictable enough to enumerate. T18 should propose that rule with the example-file exclusions already in it, which under handrail's format means an additional `not_glob` condition per exception, since RE2 has no lookaround.

### nizos/probity

The most instructive corpus, because it is a real project's real rules rather than a shipped default set. `probity.config.ts` at the repo root, seven rule invocations:

```ts
export default defineConfig({
  rules: [
    requireCommand({
      before: { kind: 'command', match: /git commit/ },
      command: /npm run checks/,
      after: { kind: 'write' },
      reason: 'Run `npm run checks` after the latest write before committing.',
    }),
    forbidCommandPattern({ match: /(?:^|[;&|])\s*find\s/, reason: '...' }),
    forbidCommandPattern({ match: /(?:^|[;&|])\s*sed\s/, reason: '...' }),
    forbidCommandPattern({ match: /(?:^|[;&|])\s*echo\b.../, reason: '...' }),
    {
      files: ['src/**', 'test/**'],
      rules: [
        enforceTdd({ maxEvents: 12, maxContentChars: 10000 }),
        forbidContentPattern({ match: 'eslint-disable', reason: '...' }),
      ],
    },
    { files: ['**/*.md'], rules: [forbidContentPattern({ match: /\p{Extended_Pictographic}/u, reason: 'No emojis in documentation' })] },
  ],
})
```

Scoring the author's own seven rules for repo-derivability:

| Rule | Derivable? |
|---|---|
| `requireCommand` → `npm run checks` before `git commit` | Yes, fully. `package.json` declares `"checks": "npm run lint:check && npm run format:check && npm run typecheck && npm test"`, and `package-lock.json` fixes the package manager. Both literals are read. |
| forbid `find` | No. Universal agent hygiene; the reason names Claude Code's Glob and Grep tools, not the project. |
| forbid `sed` | No. Same. |
| forbid `echo` redirection | No. Same. |
| `enforceTdd` scoped to `src/**`, `test/**` | Scope yes, rule no. `CLAUDE.md` says "Strict TDD" and names the two trees; the raised limits (12 / 10000 vs defaults 10 / 6000) are tuning. |
| forbid `eslint-disable` in `src/**`, `test/**` | Yes, fully. `eslint.config.js`, `eslint` in scripts and devDeps, `lint-staged` running `eslint --fix`, and the source layout are all repo facts. |
| forbid emoji in `**/*.md` | Observable, not stated. The repo is markdown-heavy and uses no emoji anywhere, but nothing declares the convention. You would be inferring it from the absence of counterexamples. |

Two of seven are hard repo-derivable, one more in scope only, one observable-but-unstated, and three are universal. **probity ships no preset rules at all**: five rule *factories* (`enforceTdd`, `enforceFilenameCasing`, `forbidCommandPattern`, `forbidContentPattern`, `requireCommand`) and zero bundled patterns. Every literal in the ecosystem is user-authored. That is the strongest available confirmation of D3's premise from the opposite direction: the one tool whose author dogfoods hardest deliberately ships nothing to distribute.

Three structural facts about probity that bear directly on handrail's format:

- Its `files: [...]` block filters **write actions only** (`src/rules/utils/match-paths.ts`: `if (action.kind !== 'write') return true`). Handrail gets the same effect from `kind:` plus absent-field semantics, without the special case.
- `requireCommand` and `enforceTdd` are stateful: they consume `ctx.history()` and `ctx.rawHistory()`. The other three are stateless predicates over one pending action. Handrail's matcher is entirely in the second class. That is the boundary that kills section 3's most-cited rule for us, and it is a design boundary rather than a missing feature.
- probity has exactly two action kinds (`write`, `command`) and no severity: a rule returns `pass` or `violation`, and any violation becomes `block`. There is no warn. Several of section 2's rules are only proposable *because* handrail has one.

**The most useful datum in this entire ticket is a negative one.** probity's source tree is uniformly kebab-case in `src/` and `test/`. probity ships `enforceFilenameCasing({ style: 'kebab-case' })` as a built-in. `docs/rules.md` uses exactly that as its worked example. The author did not enable it. A repo-derivation pass, given the signal and the tool and the example, would have proposed a rule its own author looked at and declined. See section 4.

## 4. Where the signal ends and taste begins

The line, stated once:

> **A signal fixes the matcher. It never fixes the action, the tier, or whether the rule should exist at all.**

"This repo has a formatter" and "this repo has `package-lock.json`" are facts. "Block edits that bypass the formatter" and "block hand-edits to the lockfile" are policies, and the repo is silent on both. Nothing readable in a tree distinguishes `warn` from `block`, and nothing distinguishes Global from Project-personal. Section 2 chose an action for every rule; every one of those choices is the author's, not the repo's.

The proposal set splits into three grades, and T18 should treat them differently:

**Grade 1, transcribed.** The repo states the constraint in prose a human wrote: `CLAUDE.md`, `AGENTS.md`, `CONTRIBUTING.md`, a PR template. Consent is already on disk and quotable. Section 2.14 (`os-exit-only-in-main`) is this grade, and its evidence is a sentence from `CLAUDE.md`. This is the only grade where the skill can lead with the rule rather than the question, because it is transcribing a decision rather than making one. Analogous to the Analyzer's "explicit user corrections" category, which is also the strongest signal it has.

**Grade 2, fact-determined matcher, uncontroversial policy.** The repo fixes every literal, and the policy is one almost nobody argues with: do not hand-edit a lockfile, do not edit inside `node_modules`, do not read `.env`, do not edit the guardrails. Sections 2.1, 2.2, 2.9, 2.10, 2.12. Propose with the fact as evidence, and ask for the action rather than assuming it, because `warn` and `block` are genuinely different products here.

**Grade 3, fact-parameterized taste.** The repo supplies the parameter only *after* the preference is chosen. "Never suppress a lint error" is taste; `eslint-disable` and `src/**/*.ts` are facts. "Always use the wrapper" is taste; `task test` and `go test -race -shuffle=on ./...` are facts. "Pin actions to SHAs" is taste; the fact that every existing one already is, is a fact. Sections 2.3, 2.4, 2.6, 2.8, 2.11, 2.13, 2.16, 2.17, 2.18. The skill must **ask the policy question first and fill the parameter second**. Leading with a fully written rule here reads as an assertion about how the user should work, and it is the shape most likely to burn the trust the ticket is protecting.

Two pieces of primary evidence that Grade 3 must ask rather than assert:

1. probity's kebab-case non-adoption, above. Signal present, tool present, worked example in the tool's own docs, author declined. A proposal pass without a question would have been wrong about the author's own repo, using the author's own tool.
2. cc-safety-net's `allow_paths` comment naming `.env.test` and fixtures directories. The same signal that argues for a restriction can argue for an exemption, and only the user knows which.

The practical shape this suggests for T18: propose Grade 1 as drafts, propose Grade 2 as drafts with the action left open, and propose Grade 3 as **questions with a draft attached**, one at a time, in the per-rule approval loop the Analyzer already uses (`skills/analyze/SKILL.md` step 5). And a hard floor borrowed from the Analyzer's "what does not count" section: **zero proposals is a valid result.** A repo with no lockfile, no linter, no CI, no hooks, and no prose conventions yields nothing, and saying so is better than reaching.

## 5. Cost

Measured against a binary-free probe list: 58 name-only stats, 8 targeted globs, and a fixed read set of at most a dozen small files.

| Repo | Tracked files | Probes present | Files read | Bytes read | Tokens (at 3.6 B/token) |
|---|---|---|---|---|---|
| `svyatov/handrail` | 99 | 8 of 58 | 7 | 12,715 | ~3,500 |
| A 3,848-file JS monorepo | 3,848 | 5 of 58 | 7 | 18,864 | ~5,200 |

The cost is **flat in repo size**, because the probe list is fixed and the read set is a named list rather than a discovered one. The only thing that grows between those two rows is the size of the individual files (`package.json` is 8.4 KB in the monorepo, `Makefile` 6.3 KB), not their count.

That flatness is not automatic. It depends on two rules that a naive implementation breaks immediately:

**Never list the tree.** `git ls-files` on the 3,848-file monorepo is 256,198 bytes, about 71,000 tokens: roughly fourteen times the entire probe pass, for one command, and linear in repo size. A 100,000-file monorepo would be around 1.8M tokens, which is not a budget question but an impossibility. Every signal in section 1 is reachable by a named stat or a targeted glob, so the tree walk buys nothing. Where a glob is needed, run it through `git ls-files -- '<pattern>'` so it respects `.gitignore` and never descends into `node_modules/`, `vendor/`, or `target/`.

**Read lockfiles by name, never by content.** The monorepo's `yarn.lock` is 861,398 bytes, about 239,000 tokens, which is forty-six times the whole probe pass, and it answers exactly one question that its filename already answered. The same holds for `package-lock.json` (63 KB in a small project here), `Cargo.lock`, and `go.sum`. Every lockfile signal in section 1 is a `stat`.

With both observed, a proposal pass costs about what reading one medium-sized source file costs. It fits in one agent session on any repo, with room for the corpus of existing rules from `handrail check --json` and the per-rule approval dialogue on top. Cost is not a constraint on this feature, and T18 does not need a budget mechanism, a sampling strategy, or an incremental mode. It needs a fixed probe list and a rule against globbing.

## Verdict: signals that pass the bar

Seventeen signals, nineteen rules, every one validated with `handrail check` and replayed with `handrail test`.

1. **Lockfile identity.** `package-lock.json` present, siblings absent. Two rules: no hand-edits to the lockfile; no other package manager. Section 2.1.
2. **Generated tree named in `.gitignore`.** `node_modules/`, `dist/`, `target/`, `vendor/`. One rule: no edits inside it. Section 2.2.
3. **Linter config plus source layout.** One rule: no suppression comments in source. Section 2.3.
4. **Task wrapper with a known expansion.** One rule: warn when the raw runner is invoked instead of the wrapper. Section 2.4.
5. **Git hooks actually installed.** One rule: no `--no-verify`. The signal supplies the justification, not the pattern. Section 2.5.
6. **Test naming convention plus framework.** One rule: no focused tests. The case where the derived rule beats the distributed one outright: claude-code-hooks' shipped seven-alternative `TEST_PATH` regex misses this repository's own `testdata/script/*.txtar` entirely. Section 2.6.
7. **CI workflows exist.** One rule: warn on edits to them. Section 2.7.
8. **Existing `uses:` lines are all SHA-pinned.** One rule: block an unpinned one. Section 2.8.
9. **`.env` present or gitignored.** One kind-less rule covering read and edit. Section 2.9.
10. **Key material on disk or gitignored.** One kind-less rule. Section 2.10.
11. **Tag-triggered release plus a publishable manifest.** One rule: no tags, no publish. Section 2.11.
12. **`.handrail/` exists.** One rule: the guardrails are not the agent's to edit. Section 2.12.
13. **Workspace manifest.** One rule: workspace installs must name a package. Section 2.13.
14. **A constraint stated in repo prose.** Two rules here, and in general as many as the prose states. Highest precision in the set. Sections 2.14, 2.15.
15. **Terraform state in the tree.** One rule: plan only. Section 2.16.
16. **`.mcp.json` naming a production server.** One rule: that server is read-only. Section 2.17.
17. **Migrations directory.** One rule, at `warn` only, because create and modify are indistinguishable. Section 2.18.

## Verdict: signals rejected, and why

Thirteen. Each was carried as far as writing the rule; each failed there rather than in the abstract.

1. **Protected branch and default branch name.** No canonical field carries the current branch, and `git push` normally omits it, so the rule would have to read a value that is not in the payload. Corroborated twice in the field: dcg ships both `push-main` and `push-master` because it cannot know which, and claude-code-hooks gets the working half only by shelling out to `git branch --show-current` and comparing against a hardcoded `['main', 'master']`. Handrail's matcher is a pure predicate over payload fields and does not execute anything, so the second route is closed by design, and the first has the false positive that a branch named `main-fix` trips it. (Force-push rules are writable but universal: the protection state does not change them, so this is not a repo signal.)
2. **Formatter configured.** The implied rule is "block edits that bypass the formatter". There is no field for formatted-ness, and no operator runs a program. `content` carries the edit's text, not a verdict on it. Contrast 2.3: a *suppression comment* is literal text and therefore writable; a *lint failure* is not. Corroborated by the one tool that does act on formatting: `format-code.js` picks its formatter from a hardcoded extension table and never reads `.prettierrc`, `ruff.toml`, or a `format` script, because the signal implies an action ("run this program") rather than a matcher.
3. **The project's check command as a precondition for committing.** probity's `requireCommand({ before: /git commit/, command: /npm run checks/, after: { kind: 'write' } })` needs `ctx.history()`: it finds the last matching command event and scans forward for an invalidating write. Handrail's matcher sees exactly one event and keeps no state between them. The stateless approximation, a `PostToolUse` warn on every source edit, is noise the agent tunes out, and `PostToolUse` cannot block anyway. Rejected as a *matcher*; it may be a feature, but it is not a rule.
4. **Runtime or toolchain version pin.** `.nvmrc` implies "use Node 22", which has no field. `command contains "node"` says nothing about what the shell resolves.
5. **Coverage threshold in CI.** Nothing an event carries expresses coverage.
6. **`.gitignore` translated wholesale into path globs.** The dialects genuinely differ: gitignore has `!` negation lines, leading-slash anchoring, and directory-versus-file semantics, while handrail's globs are `path.Match` plus `**`, anchored to the whole field, with `^` negating a `[...]` class and `!` an ordinary member (spec section 2, and `skills/add/SKILL.md` calls out the `!` divergence explicitly). A mechanical translation changes meaning silently, which is the worst failure mode available. Only bare directory-name entries survive, and those are already signal 2.
7. **Monorepo package boundaries as per-package rules.** Tiers are discovered at the repo root only, with no walk-up (spec section 3); monorepo nesting is a v1 non-goal (N5). A workspace signal can produce exactly one root-tier rule, which section 2.13 already is.
8. **Whether a path being edited already exists.** No field answers it and no operator can ask the filesystem. This is what caps section 2.18 at `warn` and it would cap several other "modify but not create" rules the same way.
9. **Shell-side access to secret files.** Writable but leaky in both directions: `contains: .env` also fires on `.env.example` and `environment`, while no operator tokenizes, so `cat "$(echo .env)"` passes anyway (C2). A rule that blocks reading `.env.example` is exactly the false positive that costs the trust this ticket is protecting. Verified: `command=cat .env` matches nothing in the passing set, and that is the honest state. The `path`-side rule (2.9) holds because `path` is one value rather than a sentence.
10. **Filename casing convention.** Writable as `path matches: src/[^/]*[A-Z]`, and rejected on evidence rather than expressibility: probity's own author had the signal, shipped the built-in rule, used it as the documentation example, and chose not to enable it in the repo it describes. Signal is not consent.
11. **Dependency freshness.** `AikidoSec/safe-chain`'s 48-hour rule needs a package's publish date, which is not in the repository and not in any payload. Handrail would have to make a network call from a matcher. Not a rule.
12. **Whole-file predicates.** License headers, import order, "every source file has a test", file length. `content` is the edit's new text (`new_string`), not the resulting file, so a predicate over the whole file cannot be evaluated from it. A `not_contains: SPDX-License-Identifier` rule would fire on every ordinary edit to an already-licensed file.
13. **Changelog discipline.** "Every change updates `CHANGELOG.md`" is cross-event state, same failure as 3, and additionally needs to know what a "change" is.

Six of the thirteen (1, 2, 4, 5, 12, 13) fail for the same underlying reason: **the fact is real, and the payload has no field that carries it.** Two more (3, 13) fail on statelessness. That pattern is itself a finding. If the event ring widens or a canonical field is added, re-run this list rather than the signal list, because the signals are not going to change.
