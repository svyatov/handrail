# Guard rules to offer

handrail ships no rules. These are rules you offer when the user asks to protect
handrail itself, their agent setup, their credentials, or calls handrail cannot
read. Write each only on request, into the Global tier unless the user says
otherwise. Where `$XDG_CONFIG_HOME` or `$XDG_STATE_HOME` is set, write the
resolved directory in place of the default glob.

## Rule directories

A pair, because a `path` rule never sees a file an interpreter writes. Both use
`ask`: handrail's own skills write rules through the same tool calls, and the
human's approval of each write is the consent. On Codex these guards warn;
the skills require per-rule approval in chat before writing, which the hook
does not enforce.

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
sees. On Codex both become warns, so calls proceed with the rule's message.

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

## Secrets in what the agent writes

A pair, because a file write carries `content` and a shell call carries
`command`. The pattern holds only prefixed shapes that rarely match anything but
a credential: AWS access key ids, GitHub tokens, Anthropic keys, Slack tokens and
private key headers. Add a shape the user names to both patterns, and never a
generic high-entropy one: that is a scanner's job, in pre-commit or CI.

Write the shell rule first. Its Examples hold key shapes with no `EXAMPLE` in
them, so once the file rule is on it blocks any write of the shell rule; to
change the shell rule later, the user edits it by hand. Keep each
`# gitleaks:allow` comment: it stops gitleaks and betterleaks from failing a
commit of the rule on a fake token.

```markdown
---
event: PreToolUse
kind: shell
action: block
conditions:
  - field: command
    matches: \b(AKIA|ASIA)[0-9A-Z]{16}\b|\bgh[pousr]_[A-Za-z0-9]{36}\b|\bghs_[0-9]+_eyJ[A-Za-z0-9._-]+|\bgithub_pat_[A-Za-z0-9_]{82}\b|\bsk-ant-[a-z]+[0-9]{2}-[A-Za-z0-9_-]{80,}|\bxox[abposr]-[0-9A-Za-z-]{10,}|-----BEGIN [A-Z ]*PRIVATE KEY
examples:
  match:
    - command: export AWS_ACCESS_KEY_ID=ASIAZ7QJ4HRD2KX5LMNB # gitleaks:allow
    - command: export GITHUB_TOKEN=ghs_123_eyJhbGciOiJub25lIn0.e30.AAAA # gitleaks:allow
    - command: "cat <<'EOF' > .env\nAWS_ACCESS_KEY_ID=AKIAZ7QJ4HRD2KX5LMNB\nEOF" # gitleaks:allow
  no_match:
    - command: git commit -m "rotate the AKIA keys"
---
This command holds what looks like a live credential. Read it from the
environment or a secrets manager instead of putting it in the command line.
```

```markdown
---
event: PreToolUse
kind: file_edit
action: block
conditions:
  - field: content
    matches: \b(AKIA|ASIA)[0-9A-Z]{16}\b|\bgh[pousr]_[A-Za-z0-9]{36}\b|\bghs_[0-9]+_eyJ[A-Za-z0-9._-]+|\bgithub_pat_[A-Za-z0-9_]{82}\b|\bsk-ant-[a-z]+[0-9]{2}-[A-Za-z0-9_-]{80,}|\bxox[abposr]-[0-9A-Za-z-]{10,}|-----BEGIN [A-Z ]*PRIVATE KEY
  - field: content
    not_contains: EXAMPLE
examples:
  match:
    - content: "aws_access_key_id = AKIAZ7QJ4HRD2KX5LMNB" # gitleaks:allow
    - content: "aws_access_key_id = ASIAZ7QJ4HRD2KX5LMNB" # gitleaks:allow
    - content: "token: ghp_000000000000000000000000000000000000"
    - content: "token: ghs_123_eyJhbGciOiJub25lIn0.e30.AAAA" # gitleaks:allow
    - content: "token: github_pat_0000000000000000000000000000000000000000000000000000000000000000000000000000000000"
    - content: "key: sk-ant-api03-00000000000000000000000000000000000000000000000000000000000000000000000000000000"
    - content: "token: xoxb-0000000000-0000000000"
    - content: "-----BEGIN RSA PRIVATE KEY-----"
  no_match:
    - content: "aws_access_key_id = AKIAIOSFODNN7EXAMPLE"
---
This call writes what looks like a live credential. Read it from the environment
or a secrets manager at run time instead of writing it into a file.
```

State two limits when you offer it. `not_contains: EXAMPLE` keeps the AWS
documentation key in fixtures from firing, and so also lets through a real key
in a write that holds the word. The shell rule has no such exception: a `not_`
term never reads the whole command line, and a heredoc body is only there, so
it also blocks a command that only names a key shape, such as a search for a
private key header or a `sed` that deletes one. And the pair sees only the
call's own text: a secret copied by `cp` or produced by a program is invisible
to it.

## Calls handrail cannot read

handrail never invents a block: it reports its own failure and lets a rule
decide. `ask` requests approval on Claude Code and warns on Codex; offer
`block` when the call must fail closed on both harnesses.

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
warn and the call proceeds.
