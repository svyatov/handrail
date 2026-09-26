---
name: survey
description: Propose handrail Rules for the current repository from the facts it states (lockfiles, CI workflows, .env files, git hooks and more) and the prohibitions its instruction files write down, with per-rule approval. Also proposes rules from the user's own CLAUDE.md or AGENTS.md when asked. Use when the user wants rules for this repo, says "survey this repo", "what should handrail guard here", "suggest rules", or "turn my CLAUDE.md into rules".
license: MIT
compatibility: Requires the handrail binary, on PATH or under $XDG_DATA_HOME/handrail/bin. The handrail plugin's SessionStart hook installs it.
---

# Survey this repository for guardrails

You are the Surveyor. You propose Rules from what this repository states, and the
user decides each one. You write nothing without its own approval. You propose
guards only: never a rule that relaxes another, even where a literal you found
collides with one.

A repository fact fixes a rule's matcher. It never fixes the action, the tier, or
whether the rule should exist: those are the user's.

## 1. Resolve the binary

```sh
HANDRAIL=$(command -v handrail || echo "${XDG_DATA_HOME:-$HOME/.local/share}/handrail/bin/handrail")
```

If it does not exist, the SessionStart bootstrap has not run yet: tell the user
to start a new session, or to install with `brew install svyatov/tap/handrail`.

## 2. Read the facts

```sh
"$HANDRAIL" survey
"$HANDRAIL" check --json
```

`survey` prints `signals[]`, each an `id` and the `paths` its probes found, and
`instruction_files[]`, each a `path` and a `class`: `repo` or `user`. Open no
file that `survey` did not list. `check --json` is the effective ruleset; if its
`errors[]` is not empty, show the errors first, since nothing can land until
`check` is clean.

## 3. Guard the rule directories first

Before any signal, replay one call per rule-directory guard, all three in one
shell call:

```sh
G=${XDG_CONFIG_HOME:-$HOME/.config}/handrail
"$HANDRAIL" test PreToolUse --kind file_edit --field path=.handrail/local/survey-probe.md --json
"$HANDRAIL" test PreToolUse --kind file_edit --field "path=$G/survey-probe.md" --json
"$HANDRAIL" test PreToolUse --kind shell --field 'command=cd .handrail/local' --json
```

The call names a rule directory, so where the `shell` guard already stands it
raises that guard's approval prompt: ask the user to approve it. On Codex the
guard blocks the call instead; the block shows the `shell` guard is in place,
so ask the user to run the same commands in their own terminal and paste the
output.

A guard is in place when `matched[]` holds an entry with `action` `ask` or
`block` whose rule is not on trial by its own file (`trial` false in its
`check --json` entry). Where the first two do not both show one, propose the
`file_edit` guard as `guard-rule-files.md`; where the third does not, the
`shell` one as `guard-rule-commands.md`. Propose each as "Rule directories" in
[../add/guards.md](../add/guards.md) writes it, Examples included, at the Global
tier with `action: ask`, and say why: without it, anything the agent runs can
rewrite the rules. Where `$XDG_CONFIG_HOME` is set, apply the note at the top of
guards.md: the `file_edit` guard's second glob becomes `"$G/**"`, its Example
under `.config/handrail/` moves to a file under `$G`, and the `shell` guard's
pattern gains `$G` with its dots escaped. These proposals come first and follow
steps 6 and 7 like any other.

**Not enforcing.** When `enforcement` in the `test --json` output is not
`enforce`, handrail delivers nothing in this project, and every match reports
`trial` true. Say so once, and judge trial from `check --json` wherever this
skill reads `trial`.

## 4. Build the list

Three sources, and nothing else. Zero proposals is a valid result.

**Repo signals.** For each `id` in `signals[]`, read its section in
[signals.md](signals.md): what to read, the grade, and the draft. Read only the
files it names from the paths `survey` listed. A literal is lifted from a file,
never copied with its surroundings.

**Repo prose.** Each `instruction_files[]` entry with class `repo`. Read it for
prohibitions.

**The user's own files**, only when the user asked for them in this request:
each entry with class `user`. On a plain run, skip them and say in one line
that you can.

A prose sentence becomes a rule only when all three hold:

1. It is a prohibition ("never", "do not", "must not"). A requirement ("always
   run the tests before committing") is a claim about a sequence of events, and
   no rule on one call can check it: skip it.
2. It is a test on one call's canonical fields (`command`, `path`, `content`,
   `server`, ...), as [../add/SKILL.md](../add/SKILL.md) step 3 lists them.
3. The matcher never fires on an act the sentence itself allows. A sentence
   whose stated exception you cannot express is dropped, not approximated.

A rules file's `paths:` frontmatter becomes a `path` condition on the draft. You
write every rule body yourself. Prose appears only as quoted evidence, with the
file and line it came from, never in a rule's message: a cloned repository's
words must not reach the agent with handrail's authority. Never edit an
instruction file: its sentence is what the agent reads before it acts, and the
rule only stops the attempt.

## 5. Skip what is already covered

Before asking anything about a proposal, the Grade 3 policy question included,
replay its positive payload, built from the literal you found, against the live
ruleset:

```sh
"$HANDRAIL" test PreToolUse --kind file_edit --field path=package-lock.json --json
```

Order the actions `block` > `ask` > `warn`, and treat a rule on trial by its own
file as weaker than all three. When `matched[]` holds a rule at the proposal's
action or stronger, skip the proposal and list it as covered by that rule. When
it holds only weaker ones, propose it and name the existing rule. Coverage is
this replay, never another rule's Examples.

## 6. Propose, one at a time

Order: repo prose (Grade 1), then Grade 2, then Grade 3, as
[signals.md](signals.md) grades each signal. For each, show:

1. **Evidence**: the quoted sentence with its file and line, or the signal id
   and the paths `survey` found.
2. **The complete draft**: the whole rule file, frontmatter, Examples and
   message, exactly as it would land.
3. **Path and tier.** Project-personal (`.handrail/local/<name>.md`) by default.
   Global (`${XDG_CONFIG_HOME:-$HOME/.config}/handrail/<name>.md`) when the
   matcher holds no literal from this repository, and then its Examples and
   message hold none either. A matcher derived from what this repository uses
   (its package manager, its directories, a command from its prose) holds one.
   A proposal from the user's own files suggests Global, or Project-personal
   for `CLAUDE.local.md`, and says a Global rule applies in both harnesses.
   Never write Project-shared (`.handrail/`): name it once, in the final
   report, as a step the user can take by hand.
4. **What it catches.** State the blast radius; a matcher broader than the fact
   is the user's decision.

Then ask, per rule, with `AskUserQuestion` where the harness has it:

- Grade 1: approve, trial, or drop.
- Grade 2: `block`, `ask`, `warn`, trial, or drop. The pick is the draft's
  `action:`.
- Grade 3: first the policy question [signals.md](signals.md) gives, with no
  draft shown. On a yes, show the draft and ask approve, trial, or drop.

Trial adds `trial: true`; the rule then matches and logs without enforcing. A
rule lands enforcing unless the user picks trial. Approving one is not approving
the next, and nothing is written before its own approval.

## 7. On approval: write, validate, replay

1. **Write** the file, following "Pick the tier and the path" in
   [../add/SKILL.md](../add/SKILL.md), including its notes on `sync` and on
   Codex. If its name is already in the effective ruleset, pick another:
   shadowing a rule by accident is a bug.
2. **`"$HANDRAIL" check`** must pass with the new rule in the table. It also
   runs the rule's Examples.
3. **Replay twice.** The positive payload must match. A real neighbouring file
   from this repository, or for a transcribed sentence the closest act the
   sentence allows, must not:

   ```sh
   "$HANDRAIL" test PreToolUse --kind file_edit --field path=package-lock.json
   "$HANDRAIL" test PreToolUse --kind file_edit --field path=package.json
   ```

   Tell the user both payloads are synthetic. Write them as the rule's `match`
   and `no_match` Examples, beside any the draft already carries. A Global
   rule's pair holds no repository literal, so its neighbour is a generic one,
   such as the draft's. A guard keeps the Examples guards.md gives. A replay
   that fails means the matcher is wrong: fix it and replay again. Never leave
   a rule on disk that failed its own replay.

## 8. Report

Which rules landed (enforcing or on trial), which the user dropped, which were
covered and by what, and that Project-shared is a manual step. A declined
proposal is offered again on the next run.
