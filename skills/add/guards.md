# Guard rules to offer

handrail ships no rules. These are rules you offer when the user asks to protect
handrail itself, their agent setup, or calls handrail cannot read. Write each
only on request, into the Global tier unless the user says otherwise. Where
`$XDG_CONFIG_HOME` or `$XDG_STATE_HOME` is set, write the resolved directory in
place of the default glob.

## Rule directories

A pair, because a `path` rule never sees a file an interpreter writes. Both use
`ask`: handrail's own skills write rules through the same tool calls, and the
human's approval of each write is the consent.

```markdown
---
event: PreToolUse
kind: file_edit
action: ask
conditions:
  - any:
      - field: path
        glob: "**/.handrail/**"
      - field: path
        glob: "**/.config/handrail/**"
examples:
  match:
    - path: .handrail/local/no-rm-rf.md
    - path: /home/u/.config/handrail/no-rm-rf.md
  no_match:
    - path: internal/handrail/load.go
---
This call edits a handrail rule. Rule changes need the human's approval each time.
```

```markdown
---
event: PreToolUse
kind: shell
action: ask
conditions:
  - field: command
    matches: '\.handrail\b|config/handrail|CONFIG_HOME\}?/handrail'
examples:
  match:
    - command: printf x > .handrail/local/no-rm-rf.md
    - command: rm ~/.config/handrail/no-rm-rf.md
  no_match:
    - command: handrail check
---
This command touches a handrail rule directory. Rule changes need the human's approval each time.
```

The shell rule also asks on reads (`ls .handrail`). It stays beside the path
rule because it also reads `cd` and interpreter routes that a shell `path` never
sees. On Codex both become blocks.

## handrail's state directory

It holds the trust registry, the Decision log and its grant, and the enforcement
state. No skill writes there, so this one blocks.

```markdown
---
event: PreToolUse
kind: file_edit
action: block
conditions:
  - field: path
    glob: "**/.local/state/handrail/**"
examples:
  match:
    - path: /home/u/.local/state/handrail/trusted
  no_match:
    - path: internal/rule/trust.go
---
This call writes handrail's own state: the trust registry, the Decision log and
its grant, and the enforcement state. Only the human changes these, with the
handrail command in their own terminal.
```

An agent that can write this directory can equally write rule files; the
rule-directory pair is what stands in front of those.

## Subagent definitions

A definition file decides what a spawned agent may do, including permissions
the parent session does not have.

```markdown
---
event: PreToolUse
kind: file_edit
action: block
conditions:
  - any:
      - field: path
        glob: "**/.claude/agents/**"
      - field: path
        glob: "**/.codex/agents/**"
examples:
  match:
    - path: .claude/agents/reviewer.md
  no_match:
    - path: docs/agents/issue-tracker.md
---
Subagent definitions decide what a spawned agent may do, including permissions
the parent session does not have. Change them yourself rather than having the
agent change them.
```

## Calls handrail cannot read

handrail never invents a block: it reports its own failure and lets a rule
decide. `ask` is the usual action; offer `block` as the stricter choice.

```markdown
---
event: PreToolUse
action: ask
conditions:
  - field: unreadable
    matches: .
---
handrail could not read part of this call. Say in plain text what you were about
to run, so the human can decide.
```

State two limits when you offer it. A payload handrail could not parse carries
no `kind`, so the rule must not have one. And a rule cannot fail closed on its
own tier being unlocatable: `rules` reaches a Project-tier rule when the Global
tier is missing, and reaches nothing when every tier is. An untrusted
Project-shared tier is not a failure and sets nothing. On Codex `ask` becomes a
block.
