# Repo signals

One section per signal id that `handrail survey` prints. Each gives what to read,
the grade, and a complete draft for the repository the Examples describe. Put the
literal you found where the draft has the example's: the lockfile name, the
directory, the source layout, the target's command, the server name. Replace the
Examples with the literal you found and a real neighbouring file (step 7 of
[SKILL.md](SKILL.md)). The `action:` line in a Grade 2 draft is the user's pick,
and in a Grade 3 draft the policy answer; the drafts show one value so that each
passes `check` as written. Write the message fresh for the repository.

Grade 2: show the draft and ask the action. Grade 3: ask the policy question
first, and show the draft only on a yes.

## lockfile

Reads: nothing. Grade 2. One rule over every lockfile found, one `any:` entry
each (a single condition when one was found).

```markdown
---
event: PreToolUse
kind: file_edit
action: block
conditions:
  - field: path
    glob: "**/package-lock.json"
examples:
  match:
    - path: package-lock.json
  no_match:
    - path: package.json
---
package-lock.json is written by npm. Change package.json and run npm instead of
editing the lockfile.
```

Where exactly one JavaScript package manager's lockfile is present, a second
rule: `starts_with` each of `npm `, `yarn `, `pnpm `, `bun ` except the one in
use.

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
      - field: command
        starts_with: "bun "
examples:
  match:
    - command: cd web && yarn add x
  no_match:
    - command: npm install
---
This repository uses npm, and its lockfile is package-lock.json. Run the npm
command instead.
```

## gitignored-tree

Reads: `.gitignore`, for a bare directory-name line (`node_modules/`, `dist/`,
`target/`, `vendor/`, `_build/`). Never lift a line holding `*`, `!` or an inner
`/`. Grade 2. One rule per directory.

The same read also proposes `env-file`, `key-material` and `terraform-state`
when `.gitignore` names their files: an ignored file is absent from the index, so
`survey` cannot report those signals itself.

```markdown
---
event: PreToolUse
kind: file_edit
action: block
conditions:
  - field: path
    glob: "**/node_modules/**"
examples:
  match:
    - path: node_modules/x/index.js
  no_match:
    - path: src/node_modules.ts
---
node_modules/ is installed by the package manager, not edited. Change the
dependency and reinstall instead.
```

## linter

Reads: the config found and the `package.json` scripts, for the source layout the
lint command covers. Grade 3: "Should the agent be stopped from suppressing
<linter> findings under <layout>?" The suppression marker is the linter's own:
`eslint-disable`, `biome-ignore`, `//nolint`, `rubocop:disable`, `noqa`.
`content` is a Write's whole file and an Edit's new text, so the rule also
fires on a rewrite of a file, or an Edit of a hunk, that already holds a
suppression. Say so in the question, and offer `ask` ahead of `block`.

```markdown
---
event: PreToolUse
kind: file_edit
action: ask
conditions:
  - field: path
    glob: "src/**"
  - field: content
    contains: eslint-disable
examples:
  match:
    - path: src/a.ts
      content: "// eslint-disable-next-line"
  no_match:
    - path: src/a.ts
      content: const a = 1
---
Fix the ESLint finding instead of suppressing it. If the rule is wrong for this
line, say so and let the human add the suppression.
```

## task-wrapper

Reads: the file found, for a target and the command it runs. Grade 3: "This
repository runs <command> through `<runner> <target>`. Should a bare <command>
be stopped?" The matcher is the program and subcommand the target runs; the
message names the target.

```markdown
---
event: PreToolUse
kind: shell
action: block
conditions:
  - field: command
    starts_with: go test
examples:
  match:
    - command: cd x && go test ./...
  no_match:
    - command: task test
---
Run the tests with `task test`, which runs go test the way this repository
expects.
```

## git-hooks

Reads: nothing. Grade 2.

```markdown
---
event: PreToolUse
kind: shell
action: block
conditions:
  - any:
      - field: command
        contains: --no-verify
      - field: command
        matches: git\s+commit\s+([^\s;&|]+\s+)*-\w*n
examples:
  match:
    - command: git commit -n -m wip
  no_match:
    - command: git commit -m wip
    - command: git commit -m wip && git log -n 3
---
This repository runs hooks on every commit. Do not skip them: fix what they
report.
```

## test-naming

Reads: `package.json` devDependencies for `jest`, `vitest` or `mocha`; `Gemfile`
for `rspec`. None of those found, or no test file among the listed paths: no
proposal. Grade 3: "Should the agent be stopped from committing a focused
test?" The `path` glob follows the naming of the test files `survey` listed.

```markdown
---
event: PreToolUse
kind: file_edit
action: block
conditions:
  - field: path
    glob: "**/*.test.ts"
  - field: content
    matches: \b(describe|it|test)\.only\(
examples:
  match:
    - path: src/a.test.ts
      content: it.only(
  no_match:
    - path: src/a.test.ts
      content: it(
---
A focused test skips the rest of the suite. Remove `.only` before you finish.
```

For RSpec, the same shape with `glob: "**/*_spec.rb"` and an `any:` of
`matches: \b(fdescribe|fcontext|fit)\b` and `contains: "focus: true"`.

## ci-workflows

Reads: nothing. Grade 3: "Should changes to CI workflows need your approval?"

```markdown
---
event: PreToolUse
kind: file_edit
action: ask
conditions:
  - field: path
    glob: "**/.github/workflows/*.y*ml"
examples:
  match:
    - path: .github/workflows/ci.yml
  no_match:
    - path: .github/dependabot.yml
---
CI workflows run with the repository's secrets. The human approves each change
to them.
```

## pinned-actions

Reads: every workflow listed. The signal holds only when every `uses:` naming a
repository action carries a 40-hex commit SHA; otherwise drop it. Grade 3:
"Every action here is pinned to a commit SHA. Should an unpinned `uses:` be
stopped?"

```markdown
---
event: PreToolUse
kind: file_edit
action: block
conditions:
  - field: path
    glob: "**/.github/workflows/*.y*ml"
  - field: content
    matches: uses:\s+[^\s@]+/[^\s@]+@([0-9a-f]{0,39}[^0-9a-f\s]|[0-9a-f]{1,39}(\s|$))
examples:
  match:
    - path: .github/workflows/ci.yml
      content: "uses: actions/checkout@v5"
  no_match:
    - path: .github/workflows/ci.yml
      content: "uses: actions/checkout@08c6903cd8c0fde910a37f88322edcfb5dd907a8"
---
Every action in this repository is pinned to a full commit SHA. Pin this one
too, with the tag it resolves to in a trailing comment.
```

## env-file

Reads: nothing. Grade 2. No `kind:`, so a shell call naming the file reaches the
rule through its file payload. Never propose a `command` rule on these files:
`contains: .env` also fires on `.env.example`.

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
examples:
  match:
    - kind: file_read
      path: .env
    - kind: shell
      command: cat .env
  no_match:
    - kind: file_read
      path: .env.example
---
.env files hold this repository's secrets. Do not read or write them; ask the
human for the value you need, or use .env.example.
```

## key-material

Reads: nothing. Grade 2. No `kind:`. Keep the globs whose files were found.

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
      - field: path
        glob: "**/id_ed25519"
examples:
  match:
    - kind: file_read
      path: certs/server.pem
  no_match:
    - kind: file_read
      path: certs/README.md
---
Key material stays out of the agent's hands. Ask the human for anything you
need from this file.
```

## tag-release

Reads: the workflows, for an `on:` that fires on a tag push; `package.json`, for
`"private": true`. No workflow whose `on:` fires on a tag push: drop the git
entries. The `npm publish` entry only where `package.json` is present and not
private. Neither left: no proposal. Grade 3: "A tag push publishes a release
here. Should tagging and publishing need your approval?"

```markdown
---
event: PreToolUse
kind: shell
action: ask
conditions:
  - any:
      - field: command
        matches: git\s+push\s+([^\s;&|]+\s+)*(--tags|--follow-tags|(refs/tags/)?v?\d)
      - field: command
        matches: git\s+tag\s+([^\s;&|]+\s+)*v?\d
      - field: command
        starts_with: npm publish
examples:
  match:
    - command: git tag -a v1.0.0 -m r
    - command: git push origin v1.0.0
  no_match:
    - command: git tag
    - command: git push origin main
---
A tag push publishes a release from this repository. The human approves each
one.
```

## workspace

Reads: nothing. Grade 3: "This is a pnpm workspace. Should `pnpm add` without
`--filter` be stopped?"

```markdown
---
event: PreToolUse
kind: shell
action: block
conditions:
  - field: command
    starts_with: "pnpm add "
  - field: command
    not_contains: --filter
examples:
  match:
    - command: pnpm add left-pad
  no_match:
    - command: pnpm add --filter web left-pad
---
This is a pnpm workspace. Name the package the dependency belongs to with
--filter.
```

## terraform-state

Reads: nothing. Grade 3: "This repository holds Terraform state. Should apply
and destroy need your approval?"

```markdown
---
event: PreToolUse
kind: shell
action: ask
conditions:
  - any:
      - field: command
        contains: terraform apply
      - field: command
        contains: terraform destroy
examples:
  match:
    - command: terraform destroy
  no_match:
    - command: terraform plan
---
This changes real infrastructure. The human approves each apply and destroy.
```

## mcp-server

Reads: `.mcp.json`, for a server whose name, URL or arguments name production.
No such server, no proposal. Grade 3: "<server> looks like production. Should
its tools other than `read_` ones need your approval?"

```markdown
---
event: PreToolUse
kind: mcp
action: ask
conditions:
  - field: server
    equals: prod-db
  - field: tool
    not_starts_with: read_
examples:
  match:
    - server: prod-db
      tool: execute_sql
  no_match:
    - server: prod-db
      tool: read_query
---
prod-db is production. The human approves every call that is not a read.
```

## migrations

Reads: nothing. Grade 3: "Should changes under <dir> warn, or need your
approval?" Offer `warn` or `ask`, never `block`: no field tells a new migration
from an edit to one already applied.

```markdown
---
event: PreToolUse
kind: file_edit
action: ask
conditions:
  - field: path
    glob: "**/db/migrate/**"
examples:
  match:
    - path: db/migrate/001_init.rb
  no_match:
    - path: db/seeds.rb
---
A migration that has run is history. Write a new migration rather than editing
an applied one.
```
