---
name: add
description: Turn a plain-language guardrail into a handrail Rule file, prove it matches, and validate it. Use when the user wants to add, write, or create a handrail rule, says "never let the agent ...", "block ...", "ask me before ...", "warn me when ...", or wants a Global rule relaxed in one repository.
license: MIT
compatibility: Requires the handrail binary, on PATH or under $XDG_DATA_HOME/handrail/bin. The handrail plugin's SessionStart hook installs it.
---

# Add a handrail Rule

A Rule is one markdown file: a YAML frontmatter Matcher and a prose message. The
filename is the rule's identity. You write the file, `handrail` validates it.

When the user says a Global rule blocked a call that is legitimate in this
repository, resolve the binary (step 1), then follow "Relax a Global rule in one
repository" at the end instead of steps 2-6.

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
- **Action**: `block` denies the call with the message as the reason. `ask`
  hands the call to the human, who approves or refuses it after reading the
  message. `warn` lets it proceed, injects the message, and tells the human one
  line naming the rule. Default to `warn`; use `block` when the user says never
  or the act is destructive, and `ask` when they say "ask me first" or the act is
  only sometimes wrong. `ask` is valid only on `PreToolUse`; on Codex it becomes
  `block`.
- **Tier**: see step 4.
- **Trial**: for a rule the user hesitates over, offer `trial: true`. The rule
  matches and every hit is logged, and it delivers nothing, so the agent's
  behaviour stays unaltered while the user watches. Rules land enforcing unless
  the user picks trial.
- **Coaching**: for a `warn` whose message is coaching that fires often, offer
  `agent_only: true`, which withholds the human line. Never on `block` or `ask`.

When action or tier is still open, ask them as one multiple-choice question
(`AskUserQuestion` in Claude Code), with the default listed first.

When the user asks for a substitution ("use `trash` instead of `rm`"), write a
`block` whose message names the command to run instead. handrail never rewrites
a call.

## 3. Write the Matcher

```markdown
---
event: PreToolUse
kind: shell
action: block
conditions:
  - field: command
    starts_with: git push --force
examples:
  match:
    - command: git push --force origin main
  no_match:
    - command: git push origin main
---
Never force-push. Rewrite history locally and open a pull request instead.
```

**Events**: `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `SessionStart`,
`SessionEnd`, `Stop`, `SubagentStart`, `SubagentStop`. `event:` is required.

- `block` holds on `PreToolUse` and `UserPromptSubmit`. On `PostToolUse`,
  `SessionStart`, `SessionEnd` and `SubagentStart` it is a validation error: use
  `warn`. Codex degrades a `UserPromptSubmit` block to a warn, and `sync` and
  `doctor` report it.
- On `Stop` and `SubagentStop` a `block` means "not done": the agent continues
  once, with the message as its next instruction. A `warn` there tells only the
  human. Narrow these rules with `response` (see [fields.md](fields.md)), or they
  fire at every turn end.
- `SubagentStart` takes `warn` only; its message reaches the new subagent.

**Kinds** (optional; omit to match every kind) and the fields each carries:

| `kind` | Fields |
|---|---|
| `shell` | `command`, plus `network_grant` and `unsandboxed` on Claude Code |
| `file_edit` | `path`, `content`, `removed_content`, `writes_empty`, `deletes` |
| `file_read` | `path` |
| `mcp` | `server` |
| `agent` | `agent_type`, `agent_prompt`, `model` |
| `network` | `url`, `domain` |
| `other` | none |

`tool` is carried by every kind. `prompt` is carried by `UserPromptSubmit`,
`response` by `Stop` and `SubagentStop`, and `agent_type` also by
`SubagentStart` and `SubagentStop`. `url`, `domain` and `model` fill from any
tool whose input carries them. `unreadable` is carried by any call handrail
could not fully read. Only tool events carry a kind: a `kind:` on any other event
never matches.

Before you write a condition on any field but `command` or `path`, read that
field's entry in [fields.md](fields.md): several fields only mean what the user
wants when paired with a second condition.

**`kind` or `tool`**: two vocabularies with one job each. `kind` is handrail's
own and portable, so a `kind: shell` rule means the same thing on both harnesses.
`tool` is the harness's own name for the call, so a rule naming `WebFetch` is
scoped to the harness that calls it that. Prefer `kind` unless the rule is about
one named tool. `tool` holds every name the harness answers to for that call,
including names the tool used to have, so a rule survives a vendor rename.

**Shell commands**: `command` is a shell program, not a string. handrail parses
it, and a condition asks its question of every command the program runs. So
`starts_with: rm -rf /` fires on `cd /tmp && rm -rf /`, on `echo $(rm -rf /)`,
on `(rm -rf /) && echo ok`, on `FOO=1 sudo timeout 30 rm -rf /`, on
`bash -lc 'rm -rf /'`, on `setsid rm -rf /`, on `rm    -rf   /`, and on
`rm "-rf" /`. `starts_with` on the command's own text is therefore the right
form, and the strongest one. Do not reach for `contains` or `matches` to work
around compound commands: they only widen the rule past what the user asked for.

handrail reads code inside a listed shell (`sh`, `bash`, `zsh`, `dash`, `ksh`,
`mksh`, `su -c`, `eval`, a heredoc) or a local Wrapper (`sudo`, `env`, `setsid`,
`flock`, `watch`, `xargs`, `find -exec`, `mise exec`, `direnv exec`,
`devbox run`, ...) whenever that code is literal in the call. What it still
cannot see, and what the message should say if it matters:

- **Code not in the call**: `bash -c "$X"`, `eval "$X"`, `curl ... | sh`. handrail
  sets `unreadable: command` here; see [guards.md](guards.md) for the rule that
  catches it.
- **Code in a file**: `bash script.sh`, `npm run clean`, `make clean`.
- **Remote and package runners**: `docker exec`, `kubectl exec`, `ssh`, `npx`.
  They run the command somewhere else or name a package, not a command.
- **Other shells' syntax**: `fish` and `pwsh` code.
- **Expansion**: variables are not expanded and globs are not resolved.

**Path rules reach shell calls.** A `path` rule on `kind: file_edit` or
`kind: file_read` also fires on the files a redirect or a common file command
names (`>`, `<`, `cp`, `mv`, `tee`, `sed -i`, `rm`, `cat`, ...). It never sees a
file an interpreter or an unlisted program touches, so for a path that must hold
against a hostile agent, add a `command` rule beside it, and never rely on a
`not_` `path` rule over shell. Give a `path` rule a `kind:`: without one it also
fires on reads.

**Absent fields**: a condition against a field the event does not carry never
matches, in either polarity. `not_ends_with` on `path` says nothing about a
shell command that has no path, so it does not fire there. Narrow with `event:`
and `kind:` rather than relying on a field being absent. A field the call carries
**empty is absent on the same terms**, so a `not_contains` rule on `content` does
not fire against a write that carries no text; `writes_empty` covers that case.

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

**Many values**: a field may hold several things the call does, such as each
command of a compound. A plain condition matches when any of them matches. A
`not_` condition matches when any of them fails it, which is how an allowlist
reads: `not_starts_with: git` fires on `git status; rm -rf /`. Conditions on the
same field read the same command, so a `not_` condition excepts exactly the
command the rule's other conditions found. For allowlist forms, read "Many values"
in [fields.md](fields.md).

**Globs** are `path.Match` plus `**`, anchored to the whole field, so they match
all of it rather than a substring. `*` and `?` never cross `/`. `**` spans
directories only as its own segment, leading or after a `/`; anywhere else it
reads as `*`. `**/` matches zero directories too, so `**/*.env` covers a file at
the repo root, and a trailing `**` matches every depth below. In `[...]` classes
a leading `^` negates and `!` is an ordinary member, unlike gitignore; `\` makes
the next character literal. A `path` is cleaned before matching (`src/../.env`
is `.env`), so never write `./`, `//` or an inner `..` in a `path` glob or
`equals`: `check` rejects it.

**Message**: the markdown body, addressed to the agent, in prose. Say what is
forbidden and what to do instead. No templating; `{{` stays literal. On a
`block` or `ask` the human reads this body too.

**Housekeeping**: `action:` defaults to `warn`, `enabled:` to true, `trial:` and
`agent_only:` to false. There is no `name:` field and no `pattern:` shorthand.
When several rules match one event, every message is delivered labeled with its
rule name, and the strongest action wins: `block`, then `ask`, then `warn`. A
rule matching several edits in one call delivers its message once, followed by
the files that matched.

## 4. Pick the tier and the path

Filename is the identity: kebab-case, descriptive, `.md`.

| Tier | Path | Use when |
|---|---|---|
| Project-personal | `.handrail/local/<name>.md` | Default. Private to this repo and this machine. |
| Global | `${XDG_CONFIG_HOME:-$HOME/.config}/handrail/<name>.md` | The rule is project-agnostic and should hold in every repo. |
| Project-shared | `.handrail/<name>.md` | Only when the user explicitly asks to commit it for the team. Never write here otherwise. Inert until `handrail trust` is run for this repo. |

After writing into `.handrail/local/`, run `"$HANDRAIL" sync` once unless
`.git/info/exclude` already holds its line: sync is what appends the ignore line
there, so without it private rules show up in `git status`.

If a rule you wrote under `.handrail/local/` is missing from `check`'s table and
`check` says on stderr that it skipped untrusted Project-shared rules, this
repository commits `.handrail/local/`, so the tier is read as Project-shared and
your rule is inert. Say so, and offer Global for this rule and the rest. Never
run `handrail trust` yourself: it would enable the repository's own committed
rules, and trusting a repository is the user's act, in their own terminal.

A same-named file in a higher tier shadows the lower one wholesale, and a
duplicate name within one tier is a hard error, so check the `check` output in
step 6 for the name you chose before settling on it. A Project-shared rule never
shadows a Global one: the shared file is dropped, so a rule for the team takes a
name of its own. A Project-shared rule may not set `agent_only`.

Writing into a rule directory may raise an approval prompt: that is the user's
guard rule doing its job ([guards.md](guards.md)), and the approval is the
consent. On Codex the prompt becomes a block; there, print the file and its path
for the user to save, and continue once they say it is saved.

## 5. Prove it matches, and keep the proof

Replay the case that motivated the rule, and a near-miss that must not match:

```sh
"$HANDRAIL" test PreToolUse --kind shell --field command='git push --force origin main'
"$HANDRAIL" test PreToolUse --kind shell --field command='git push origin main'
```

Exit code 2 means blocked, 3 means ask, 0 means allowed or warned. If the rule
does not appear in the matched list, the Matcher is wrong: fix it before moving
on. `test` prints the Candidates a command produced and anything handrail could
not read, so if a compound command does not match, read that list before
changing the operator.

Write both into the rule's frontmatter: the replay under `examples: match:`, the
near-miss under `no_match:`, each as the fields you passed to `--field`. Where
the `--kind` you replayed with is not the rule's `kind:`, write it into the
entry as `kind:`: an entry without one takes the rule's kind, else `other`, so a
`command` Example under a `path` rule needs `kind: shell` to yield its file
payload. They keep proving what the rule means after every handrail upgrade.
Never copy a credential into an Example.

## 6. Validate

```sh
"$HANDRAIL" check
```

Show the user the new rule's line from the output. Errors name the failing
file: fix and re-run until clean. `check` also runs every rule's Examples and
exits 1 when one fails. A rule is live as soon as it validates; no re-sync is
needed.

A Project-shared rule in an untrusted repo is valid but absent from the table,
and `check` says so on stderr. That is not a rule to fix: tell the user to run
`"$HANDRAIL" trust`.

To disable an inherited rule entirely, write the same filename one tier up with
only `enabled: false` in the frontmatter and nothing else. To re-enable it,
delete that stub. Do not set `enabled: true` on it: an enabled rule needs an
`event`, so the stub fails validation instead of restoring the original. Never
delete the lower-tier rule to undo a disable, which removes the guardrail from
every project that inherited it.

## Relax a Global rule in one repository

A repository cannot relax the user's rules; the user does, with a
Project-personal shadow. Follow these steps when the user says a Global rule
blocked a call that is legitimate here.

1. Find the rule by the name its block message carries, and read its file.
2. Offer the narrow shadow first, and the `enabled: false` stub second. The stub
   switches the whole rule off in this repository.
3. Narrow shadow: copy the rule verbatim into `.handrail/local/<same basename>.md`
   and add one condition, `field: command` with `not_equals: <the legitimate
   command>`. It excepts that command as written, alone or inside a compound.
   Behind a Wrapper or a leading assignment (`sudo ...`, `FOO=1 ...`) it is a
   different Candidate and stays blocked unless the rule's positive term is
   `starts_with`, which the outer form never meets.
4. If the rule has no `match:` Examples, ask the user for one call it must still
   block. Write it into the shadow's `match:`, and the legitimate call into its
   `no_match:`. If an existing `match:` Example is the legitimate call, stop: the
   rule promises to block exactly this, so the choice is the stub or editing the
   Global rule.
5. Before writing, state what the shadow stops blocking. This is a weakening, and
   the user approves it on its own.
6. The write lands under the rule-directory guard's `ask`; that prompt is the
   user's grant. On Codex, print the file and its path instead.
7. Run `"$HANDRAIL" check`, which also tests the shadow against the original's
   `match:` Examples, so a later change to the Global rule that the copy misses is
   reported.
