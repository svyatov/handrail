---
name: add
description: Turn a plain-language guardrail into a handrail Rule file, validate it, and relay the native permission entry handrail recommends. Use when the user wants to add, write, or create a handrail rule, or says "never let the agent ...", "block ...", "warn me when ..." and wants it enforced from now on.
---

# Add a handrail Rule

A Rule is one markdown file: a YAML frontmatter Matcher and a prose message. The
filename is the rule's identity. You write the file, `handrail` validates it.

## 1. Resolve the binary

```sh
HANDRAIL=$(command -v handrail || echo "${XDG_DATA_HOME:-$HOME/.local/share}/handrail/bin/handrail")
```

If it does not exist, the SessionStart bootstrap has not run yet: tell the user
to start a new session, or to install with `brew install svyatov/tap/handrail`.
Do not hand-write harness config as a workaround.

## 2. Ask for what the description does not settle

Only ask about what you cannot infer. Most descriptions settle everything but
the tier.

- **What the rule catches**: which event, which tool kind, which field values.
- **Action**: `block` denies the call with the message as the reason; `warn`
  lets it proceed and injects the message. Default to `warn` unless the user
  says never, or the action is destructive.
- **Tier**: see step 4.

## 3. Write the Matcher

```markdown
---
event: PreToolUse
kind: shell
action: block
conditions:
  - field: command
    starts_with: git push --force
---
Never force-push. Rewrite history locally and open a pull request instead.
```

**Events**: `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `SessionStart`,
`SessionEnd`, `Stop`. `event:` is required. `block` is only enforceable on
`PreToolUse`, `UserPromptSubmit`, and `Stop`; elsewhere it degrades to a warn,
and `sync` and `doctor` report where. Codex also degrades a `UserPromptSubmit`
block to a warn.

**Kinds** (optional; omit to match every kind) and the fields each carries:

| `kind` | Fields |
|---|---|
| `shell` | `command` |
| `file_edit` | `path`, `content` |
| `file_read` | `path` |
| `mcp` | `server`, `tool` |
| `other` | none |

`UserPromptSubmit` carries `prompt`. A condition against a field the event does
not carry never matches, in either polarity, so narrow with `event:` and `kind:`
rather than relying on a field being absent. Only `PreToolUse` and `PostToolUse`
carry a kind at all: a `kind:` on any other event never matches.

**Conditions** are ANDed. One entry may be an `any:` group for OR, one level
deep only:

```yaml
conditions:
  - field: path
    glob: "**/*.env"
  - any:
      - field: content
        contains: SECRET
      - field: content
        matches: (?i)api[_-]?key
```

**Operators**, one per condition, as the key next to `field`: `matches` (RE2),
`contains`, `equals`, `starts_with`, `ends_with`, `glob`. Prefix any of them
with `not_` to negate. RE2 has no lookarounds and no backreferences. Everything
is case-sensitive; regexes opt in with an inline `(?i)`.

**Globs** are `path.Match` plus `**`, anchored to the whole field, so they match
all of it rather than a substring. `*` and `?` never cross `/`. `**` spans
directories only as its own segment, leading or after a `/`; anywhere else it
reads as `*`. `**/` matches zero directories too, so `**/*.env` covers a file at
the repo root, and a trailing `**` matches every depth below. In `[...]` classes
a leading `^` negates and `!` is an ordinary member, unlike gitignore; `\` makes
the next character literal.

**Message**: the markdown body, addressed to the agent, in prose. Say what is
forbidden and what to do instead. No templating; `{{` stays literal.

**Housekeeping**: `action:` defaults to `warn` and `enabled:` to true. There is
no `name:` field and no `pattern:` shorthand. When several rules match one
event, every message is delivered labeled with its rule name, and one `block`
makes the outcome a block.

## 4. Pick the tier and the path

Filename is the identity: kebab-case, descriptive, `.md`.

| Tier | Path | Use when |
|---|---|---|
| Project-personal | `.handrail/local/<name>.md` | Default. Private to this repo and this machine. |
| Global | `${XDG_CONFIG_HOME:-$HOME/.config}/handrail/<name>.md` | The rule is project-agnostic and should hold in every repo. |
| Project-shared | `.handrail/<name>.md` | Only when the user explicitly asks to commit it for the team. Never write here otherwise. Inert until `handrail trust` is run for this repo. |

If you just created `.handrail/local/` in this repo, run `"$HANDRAIL" sync`
once: sync is what appends the ignore line to `.git/info/exclude`, so without it
private rules show up in `git status`.

A same-named file in a higher tier shadows the lower one wholesale, and a
duplicate name within one tier is a hard error, so check the `check` output in
step 5 for the name you chose before settling on it. To disable an inherited
rule, write the same filename one tier up with only `enabled: false` in the
frontmatter and nothing else.

To re-enable it, delete that stub. Do not set `enabled: true` on it: an enabled
rule needs an `event`, so the stub fails validation instead of restoring the
original. Never delete the lower-tier rule to undo a disable, which removes the
guardrail from every project that inherited it.

## 5. Validate

```sh
"$HANDRAIL" check
```

Show the user the new rule's line from the output. Errors name the failing
file: fix and re-run until clean. A rule is live as soon as it validates; no
re-sync is needed.

A Project-shared rule in an untrusted repo is valid but absent from the table,
and `check` says so on stderr. That is not a rule to fix: tell the user to run
`"$HANDRAIL" trust`.

## 6. Prove it matches

Replay the case that motivated the rule:

```sh
"$HANDRAIL" test PreToolUse --kind shell --field command='git push --force origin main'
```

Exit code 2 means blocked, 0 means allowed. If the rule does not appear in the
matched list, the Matcher is wrong: fix it before moving on. Test a near-miss
too when the rule could be too broad.

## 7. Relay the Advisor

```sh
"$HANDRAIL" advise <rule-name> --harness <the harness you are running in> --json
```

Some block rules translate into a native harness permission entry, which is a
fail-closed backstop for the hook path. Without `--harness` the output covers
every harness handrail knows, so scope it: never offer a Codex entry inside a
Claude Code session. For each entry in the array, show the user its `entry`
field **verbatim** with its `location`, plus `scope_widening` and `caveats` when
they are set, and offer to apply it. Apply only on consent.

Never invent or adapt a harness pattern yourself: the translation knowledge
lives in the binary. An empty array (`[]`) means the rule does not translate
safely, which is normal and not a failure.
