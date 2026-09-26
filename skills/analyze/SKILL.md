---
name: analyze
description: Mine the current session and handrail's Decision log to propose new handrail Rules, tune existing ones, and conclude rules on trial, with evidence and per-rule approval. Use when the user wants to turn this session's corrections into guardrails, says "analyze this session", "what should I have blocked", "make that a rule so it never happens again", asks whether a rule is working, or wants to promote or drop a trial rule.
license: MIT
compatibility: Requires the handrail binary. Transcript lookup supports Claude Code and Codex CLI.
---

# Analyze this session for guardrails

You are the Analyzer. You have three jobs: propose **new** Rules for behaviors
worth preventing, **tune** existing rules where the evidence says they are wrong,
and **conclude** rules on trial. You write nothing without approval.

## 1. Resolve the binary

```sh
HANDRAIL=$(command -v handrail || echo "${XDG_DATA_HOME:-$HOME/.local/share}/handrail/bin/handrail")
```

If it does not exist, the SessionStart bootstrap has not run yet: tell the user
to start a new session, or to install with `brew install svyatov/tap/handrail`.

## 2. Find the session transcript

Claude Code:

```sh
find "${CLAUDE_CONFIG_DIR:-$HOME/.claude}/projects" -name "$CLAUDE_CODE_SESSION_ID.jsonl"
```

Codex CLI:

```sh
find "${CODEX_HOME:-$HOME/.codex}/sessions" -name "*-$CODEX_THREAD_ID.jsonl"
```

Both harnesses export the id to the shell, so an empty result means the file is
not written yet, not that you have the wrong path. If the id variable itself is
unset, say so and take the no-transcript route below; never fall back to the
newest transcript on disk, which is some other session's.

The transcript is JSONL, one JSON record per line, often megabytes. Do not read
it whole. Your own context is the primary source of signals; grep the file for
distinctive strings to quote evidence verbatim, and to recover turns that
compaction dropped out of your context.

**The tail lags.** Claude Code writes the transcript asynchronously, so the last
few turns, including the request that started this analysis, may be missing from
the file. Never conclude a behavior did not happen because the file does not
show it. Your context wins over the file for recent turns; the file wins for old
ones.

**No transcript at all** (harness without transcript access, or the file is
missing): say so, and work from your own context alone; every step below still
applies. An incident compaction dropped from your context is lost: for it, offer
the fallback of the user describing it in words to the handrail `add` skill
(`/handrail:add` in Claude Code, a `$` mention in Codex). Do not guess at a
transcript path.

Never read the harness's own record of hook results: it is not handrail's, and
its format is not stable.

## 3. Read the ruleset and its history

```sh
"$HANDRAIL" check --json --stats
```

`rules[]` is the effective ruleset: `rule`, `tier`, `event`, `kind`, `action`,
`enabled`, `trial`, `shadowed_by`, `path`, and `stats` (`matches`, `sessions`,
`first_seen`, `last_seen`). A rule is live when `enabled` is true and
`shadowed_by` and `dropped_by` are null. The top-level `stats` holds `oldest`, the time of the
oldest log line, and `unreadable[]`, counts per `tool` and `field` of calls
handrail could not read. Messages are not in the JSON, so open `path` to read a
rule you are about to propose beside. If `errors[]` is not empty, show the
errors first: they may hide coverage, and nothing can land until `check` is
clean.

**No grant.** When stderr says the Decision log is off for this project, the
stats count trial matches only. With no grant, or with `oldest` null, do the
transcript job (step 4) in full, conclude trials from what the trial lines show,
and say once that you could not see rule history.

When a proposal concerns one rule, pull that rule's lines, and tell the user how
many you pulled:

```sh
"$HANDRAIL" log --rule <name> --json
```

Each line is one evaluation: `time`, `session_id`, `kind`, `tool`, `outcome`,
`matched[]` (`rule`, `tier`, `action`, and `trial`: `"rule"` for the rule's own
`trial: true`, `"state"` for `handrail mode trial`, absent when enforcing), `unreadable`, and `payload`, the
canonical fields as handrail derived them, each a list of Candidates with their
`spellings`. A value ending in `[truncated]`, or a line with `"truncated": true`
and a null `payload`, is cut.

## 4. New rules

What counts (non-exhaustive):

- **Explicit corrections**: the user told you to stop, undo, or do it another
  way. "Don't ever run that", "not in that file", "I said use X".
- **Repeated instructions**: the same steer given more than once, in this
  session or as a follow-up to something you did anyway.
- **Manual reverts and interventions**: the user undid your edit, reset the
  branch, killed a command, or fixed your output by hand.
- **Near-misses**: a dangerous action that was approved, denied, or narrowly
  avoided. A denied permission prompt is evidence even though nothing broke.

What does not count. Be strict here, a bad rule fires forever:

- One-off task detail ("use port 3001 today"), not a standing constraint.
- Taste you inferred rather than the user stated.
- Anything you cannot express as a Matcher over canonical fields. A rule about
  intent, tone, or code quality has no field to match on.
- Anything a live rule already catches: replay the call through `test` (step 8)
  and skip it when a live rule matches. A rule that matched and was ignored is a
  tuning signal (step 5), not a new rule.

## 5. Tune existing rules

The one signal is the **override**: a rule matched (a log line, or a block or
ask in this session), and the user then asked the agent for the act anyway. Read
the rule, pull its lines, and propose the smallest change that stops it firing
on that act: usually one added `not_equals` exception. A Global or
Project-shared rule takes it in a Project-personal copy, as "Relax a Global rule
in one repository" in `../add/SKILL.md` writes it; a Project-personal rule takes
it in place.

A rule whose matcher missed a call it was plainly written to catch, seen in this
session, gets a proposal that widens it to that call.

Nothing else proposes a change:

- **Frequency alone** proposes nothing. The log records no non-matches, so a
  count has no rate.
- **A rule with no match** gets one line naming the window it was silent over
  (from `oldest` to now) and nothing more: a guardrail that never fires is
  usually working.

## 6. Conclude trials

For each rule with `trial: true`, report its matches from `stats` and its log
lines with `trial` `"rule"`, and make one proposal with two answers: **promote**
it (remove `trial: true`, so it enforces) or **drop** it (delete the file). Both
edit or delete a rule file, so step 7 applies. Say which the evidence favors: a
trial whose matches are all acts the user wanted stopped is ready; one that
fired on legitimate work needs tuning first, and step 5 applies.

## 7. The heavier bar for a weakening proposal

A proposal weakens when the act it performs is one of these, whatever you call
it:

- editing or deleting an existing rule file;
- a new rule whose name shadows a rule in a lower tier;
- any rule that sets `enabled: false`, `trial: true` or `agent_only: true`.

Such a proposal quotes its log lines verbatim, as `log --rule` printed them, and
is asked about alone, with no other proposal in the same question. Under a
prompt-injected agent, the party proposing a retirement may be the party that
produced the evidence for it.

Retiring an enforcing rule is first offered as a downgrade to `trial: true`,
when the rule is the user's to edit (Global or Project-personal). A Project-shared rule is the
repository's: it can only take a Project-personal `enabled: false` stub or a
wholesale copy with the change (`../add/SKILL.md`).

## 8. Propose, one at a time

Before proposing any rule, look up its field in `stats.unreadable` for the tool
it concerns. Where handrail has failed to read that field on that tool, carry the
caveat into the proposal: the rule cannot see those calls.

For each proposal, show:

1. **The behavior**, in one sentence.
2. **Evidence**: the quote from the session, with enough context to recognize
   it, and the tool call it is about, or the log lines.
3. **The complete draft**: the whole rule file, frontmatter, Examples and
   message, exactly as it would land, written per "Write the Matcher" in
   `../add/SKILL.md`. For an edit, the whole new file and what changed.
4. **Path and tier**, per "Pick the tier and the path" there. Default to
   Project-personal. Suggest Global only when the behavior is clearly
   project-agnostic ("never force-push"). Never propose Project-shared:
   committing a rule for the team stays a manual act.
5. **What it catches**, or for a tuning proposal what it stops catching. A
   matcher broader than the incident is a decision the user makes.

Then ask for approval on that rule alone (with `AskUserQuestion` where the
harness has it). Approving one is not approving the next, and nothing is written
before its own approval. Zero proposals is a valid result: say the session and
the log showed nothing worth a durable rule, and stop.

## 9. On approval: write, validate, replay

1. **Write** the file at the agreed path, following "Pick the tier and the path"
   in `../add/SKILL.md`, including its note on `sync` and `.git/info/exclude`.
   A new rule whose name is already in the effective ruleset takes another name,
   unless the shadow is the approved proposal. A rule-directory guard may raise
   an approval prompt on the write: that prompt is the user's consent working.
   **On Codex** the guard's `ask` is a block: when a rule blocks the write, print
   the file and its path for the user to save, and continue once they say it is
   saved.
2. **`"$HANDRAIL" check`** must pass, with the rule in the table.
3. **Replay the incident.** A new or widened rule must match it; a narrowed rule
   must no longer match the call the user overrode it for; a promoted trial must
   now report its action on its trial matches:

   ```sh
   "$HANDRAIL" test PreToolUse --kind shell --field command='<the command from the transcript>'
   ```

   Use the transcript's full tool input where the incident is in this session.
   Otherwise take the log line's `payload`: `--kind` from its `kind`, and one
   `--field` per field, from the `spellings` of the Candidate marked `whole` for
   `command`, else each Candidate's first Spelling. A cut payload with no copy in
   the transcript is not replayed: say so. **If the replay goes the wrong way,
   the rule is wrong**: fix it and replay again. Never leave a rule on disk that
   failed its own replay.
4. **Keep the proof.** Write the incident as a `match` Example, cut down to the
   smallest input that still matches and is still recognizably the incident,
   with no credential copied, and with a `kind:` where the replay's differs from
   the rule's, per step 5 of `../add/SKILL.md`. On a narrowing, the call the
   rule must stop matching goes under `no_match`. Run `check` again.

Report at the end: which rules landed or changed, which trials were promoted or
dropped, which proposals the user declined, and what you skipped as already
covered.
