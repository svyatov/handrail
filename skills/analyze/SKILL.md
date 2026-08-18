---
name: analyze
description: Mine the current session for behaviors worth preventing and propose handrail Rules for them, with transcript evidence and per-rule approval. Use when the user wants to turn this session's corrections into guardrails, or says "analyze this session", "what should I have blocked", "make that a rule so it never happens again".
---

# Analyze this session for guardrails

You are the Analyzer. You read this session, propose **new** Rules for behaviors
worth preventing, and write nothing without approval. You never propose edits to
existing rules: tuning a rule that fired is out of scope.

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
missing): say so, and offer the fallback: describe the incident in words to
`/handrail:add`, which writes a rule from a plain-language description. Do not
guess at a transcript path.

## 3. Read what is already covered

```sh
"$HANDRAIL" check --json
```

`rules[]` is the effective ruleset: `rule`, `tier`, `event`, `kind`, `action`,
`enabled`, `shadowed_by`, `path`. A rule is live only when `enabled` is true and
`shadowed_by` is null; a shadowed entry is listed but not in force. Messages are
not in the JSON, so open `path` to read the message of any rule that looks
adjacent to what you are about to propose.

Skip any behavior a live rule already catches. A rule that exists but was
ignored is not a proposal: it is a report, mention it and move on. If `errors[]`
is non-empty, show the errors first: they may hide coverage, and nothing can
land until `check` is clean.

## 4. Mine for signals

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
- Anything already covered (step 3).

Aim for the few that are worth living with. Zero proposals is a valid result:
say the session showed nothing worth a durable rule, and stop.

## 5. Propose, one at a time

For each candidate, show:

1. **The behavior**, in one sentence.
2. **Evidence**: the quote from the session that motivated it, with enough
   context to recognize it, plus the tool call it is about.
3. **The complete draft**: the whole rule file, frontmatter and message, exactly
   as it would land, written per "Write the Matcher" in `../add/SKILL.md`.
4. **Path and tier**, per "Pick the tier and the path" there. Default to
   Project-personal. Suggest Global only when the behavior is clearly
   project-agnostic ("never force-push") rather than tied to this repo. Never
   propose Project-shared: committing a rule for the team stays a deliberate
   manual act, and you can point that out as an option the user can take later.
5. **What it would have caught, and what else it will catch.** State the blast
   radius honestly; a matcher that is broader than the incident is a decision
   the user makes, not one you make quietly.

Then ask for approval on that rule. Per rule, not in a batch: approving one is
not approving the next. Approved means write it; anything else means drop it and
move to the next candidate. Never write a file before its own approval.

## 6. On approval: write, validate, replay, advise

1. **Write** the file at the agreed path, following "Pick the tier and the path"
   in `../add/SKILL.md`, including its note on `sync` and `.git/info/exclude`.
   If the name is already in the effective ruleset, pick another; shadowing an
   existing rule by accident is a bug.
2. **`"$HANDRAIL" check`** must pass, and the new rule must appear in the table.
   Fix and re-run until clean ("Validate" there).
3. **Replay the incident.** Rebuild the motivating event as a synthetic payload
   and prove the rule catches it:

   ```sh
   "$HANDRAIL" test PreToolUse --kind shell --field command='<the command from the transcript>'
   ```

   Use the real values from the evidence, not a paraphrase. The rule must appear
   in the matched list; exit 2 confirms a block. **If the replay does not match,
   the rule is wrong**: fix the Matcher and replay again before moving on. Never
   leave a rule on disk that failed its own replay.
4. **Relay the Advisor** for block rules, scoped to the harness you are running
   in ("Relay the Advisor" there). An empty array is normal.

Report at the end: which rules landed, which the user declined, and any behavior
you skipped as already covered.
