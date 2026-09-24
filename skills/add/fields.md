# Fields that need more than one condition

Read the entry for the field your rule tests. Each says what the field holds,
what it misses, and the rule shape that means what the user asked for.

## `content`

The text the call writes, not the file's resulting state. `content contains X`
catches a call that writes X. It does not catch a file that already holds X, and
it does not catch a call that leaves X in place. When the user's words are about
what a file must never hold rather than what a call must never write, say so:
that is a check that reads files, and a handrail rule is not one.

## `writes_empty` and `deletes`

`writes_empty: true` means the call writes no text: a whole-file write that
leaves the file empty, or an edit that removes text and adds none. `deletes:
true` means the call removes the file. Both come from the call, never from disk.
`deletes` is set only by a Codex patch; on Claude Code, and for any shell `rm`,
write a `command` rule on `rm` instead.

## `removed_content`

The text an edit names as removed. It usually also holds text the edit puts
back, because an edit names the whole region it touches. So "never remove X" is
two conditions:

```yaml
conditions:
  - field: removed_content
    contains: "# nosec"
  - any:
      - field: content
        not_contains: "# nosec"
      - field: writes_empty
        equals: true
```

Use `warn` or `ask`, never `block`, unless the user insists, because refactors
move text. Match the shortest token that identifies X, because on Claude Code an
edit can remove part of a line. A whole-file write names no removed text, so when
the user means "never remove X from this file", also offer a rule on writes to
that `path`.

## `response`

The agent's last message, on `Stop` and `SubagentStop`. The agent writes that
text, so test what it admits, never what it claims: "block when it says it
skipped the tests" catches a careless agent, and "block unless it says the tests
pass" teaches the agent to say so.

```yaml
event: Stop
action: block
conditions:
  - field: response
    matches: (?i)(didn't|did not|couldn't) (run|test)
```

with the message "Run the tests before you finish." A block here continues the
agent once, so it is a nudge, never a gate.

## `url` and `domain`

`url` is the destination a call names; `domain` is its host, parsed strictly.
Write host rules against `domain`, never `url`: `https://github.com@evil.com/`
passes the obvious `url` test. "This host and every subdomain" is an `any:` of
`equals` and `ends_with`:

```markdown
---
event: PreToolUse
action: block
conditions:
  - any:
      - field: domain
        equals: pastebin.com
      - field: domain
        ends_with: .pastebin.com
---
Never send anything to a paste site. If you need to share output, show it to me.
```

Use `url` only for a path-scoped rule. A fetch-shaped rule does not see `curl`:
a user who means "never reach this host" also needs a `command` rule, so say so.
A `url` key on an MCP tool fills `url` whether or not the tool fetches it; narrow
with `kind: network` or `tool` when the rule means fetches.

## `network_grant` and `unsandboxed`

On Claude Code, `network_grant` holds the hosts a shell call asks the sandbox to
open, and `unsandboxed` is `true` when it asks to run outside the sandbox. The
usual rule asks:

```markdown
---
event: PreToolUse
action: ask
conditions:
  - any:
      - field: network_grant
        matches: "."
      - field: unsandboxed
        equals: true
---
You asked for network or filesystem access beyond the sandbox. Say which host or
path you need and why before I approve it.
```

It fires only on Claude Code: Codex never shows a hook its escalation. A user who
wants disable-sandbox refused outright can set Claude Code's own
`allowUnsandboxedCommands: false`. For "ask unless every requested host is in my
list", see "Many values" below.

## `model`

The model a call asks for, as the harness spells it. A rule on `model` stops the
agent asking for a model on a spawn. It does not see a model chosen by a
subagent definition, a Codex role file or an environment variable, so pair it
with the agent-definition guard in [guards.md](guards.md). Add `kind: agent` when
the rule means spawns only.

## `agent_type`

The subagent a spawn asks for. A spawn that names no type leaves it absent, so
an allowlist written as `not_equals` on `agent_type` does not fire there. To
block every spawn whatever its type, write `kind: agent` with no `agent_type`
condition.

## `unreadable`

What handrail could not read: a field name, `command` for code not literal in a
call, `path` for a shell target it cannot read, `domain` for a URL whose host it
cannot trust, `payload`, or `rules`. handrail
never invents a block; a rule on `unreadable` decides. [guards.md](guards.md)
holds the template, and `ask` is its usual action.

## Many values

A field may hold several things the call does. A `not_` condition fires when any
of them fails it, so an allowlist is one `not_` condition:

- **Hosts**: `field: network_grant`, `not_matches: ^(github\.com|pypi\.org)$`,
  `action: ask`. "Ask unless every requested host is in my list."
- **Commands**: the pattern must also allow the wrappers and assignment prefixes
  it accepts, for example `not_matches: ^(sudo |[A-Z_]+=\S* )*git\b`, and it
  fires on any redirect to a file.
