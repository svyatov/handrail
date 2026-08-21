# The v2 event model: which harness events earn a place

Date: 2026-08-21
Purpose: primary-source research for handrail issue #92 (wayfinder ticket T9), mapping
Claude Code's full hook event surface against handrail's six-event core, checking each
candidate against Codex CLI and Gemini CLI, and recommending the event ring for v2. Also
captures the exact `if`-field schema for T4, the `PermissionRequest` model change for T10,
and the `updatedInput` rewrite question for T11.

All Claude Code claims below come from the official documentation, fetched on 2026-08-21 as
raw markdown from the `.md` endpoints listed in `https://code.claude.com/docs/llms.txt`.
Primary pages used:

- Hooks reference: https://code.claude.com/docs/en/hooks.md
- Hooks guide: https://code.claude.com/docs/en/hooks-guide.md
- Permissions: https://code.claude.com/docs/en/permissions.md

Codex CLI and Gemini CLI claims cite their own repositories and documentation and are marked
per section. Where a fact could not be established from a primary source, this document says
so rather than inferring it.

## 0. The event count

Issue #92 is titled "which of Claude Code's 27 hook events earn a place in the model". The
live hooks reference documents **31** events, not 27
(https://code.claude.com/docs/en/hooks.md#hook-events, enumerated in the "How hooks work"
table at https://code.claude.com/docs/en/hooks-guide.md#how-hooks-work). handrail's own
earlier research paper, `docs/research/claude-code-extension-surface.md` on
`research/claude-code-surface`, already recorded 31 on 2026-08-15.

The ticket's own body is consistent with 31. It names 25 events as outside handrail's ring
today, and handrail's ring holds 6, which totals 31. Only the title's number is wrong. Treat
31 as the count.

The 31 events, verbatim from the reference:

`SessionStart`, `Setup`, `InstructionsLoaded`, `UserPromptSubmit`, `UserPromptExpansion`,
`MessageDisplay`, `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PostToolUseFailure`,
`PostToolBatch`, `PermissionDenied`, `Notification`, `SubagentStart`, `SubagentStop`,
`TaskCreated`, `TaskCompleted`, `Stop`, `StopFailure`, `TeammateIdle`, `ConfigChange`,
`CwdChanged`, `DirectoryAdded`, `FileChanged`, `WorktreeCreate`, `WorktreeRemove`,
`PreCompact`, `PostCompact`, `SessionEnd`, `Elicitation`, `ElicitationResult`.

## 1. The full Claude Code event surface

The three capabilities that decide whether an event is worth anything to a guardrail are:
can it block, can it inject context, and can it rewrite. The reference states each of these
in three separate tables: exit code 2 behaviour per event
(https://code.claude.com/docs/en/hooks.md#exit-code-2-behavior-per-event), decision control
(https://code.claude.com/docs/en/hooks.md#decision-control), and the four content-rewrite
events listed under that same section.

| Event | Fires on | Block? | Inject context? | Rewrite? | `if` accepted? |
| :--- | :--- | :--- | :--- | :--- | :--- |
| `SessionStart` | session begins or resumes | No | Yes, `additionalContext` | No | No |
| `Setup` | `--init-only`, or `--init`/`--maintenance` in `-p` | No | Yes | No | No |
| `InstructionsLoaded` | a CLAUDE.md or `.claude/rules/*.md` loads | No | No | No | No |
| `UserPromptSubmit` | prompt submitted, before Claude sees it | Yes | Yes | No, cannot replace the prompt | No |
| `UserPromptExpansion` | a typed command expands into a prompt | Yes | Yes | No | No |
| `MessageDisplay` | assistant text streams to the screen | No | No | Yes, `displayContent`, display only | No |
| `PreToolUse` | before a tool call executes | Yes | Yes | Yes, `updatedInput` | Yes |
| `PermissionRequest` | Claude Code is about to ask for permission | No, exit 2 not honoured | No | Yes, `decision.updatedInput` | Yes |
| `PostToolUse` | after a tool call succeeds | No | Yes | Yes, `updatedToolOutput` | Yes |
| `PostToolUseFailure` | after a tool that started execution fails | No | Yes | No | Yes |
| `PostToolBatch` | after a parallel batch resolves | Yes | Yes | No | No |
| `PermissionDenied` | auto mode denies a tool call | No | No, `retry: true` only | No | Yes |
| `Notification` | Claude Code sends a notification | No | No | No | No |
| `SubagentStart` | a subagent is spawned | No | Yes, into the subagent | No | No |
| `SubagentStop` | a subagent finishes | Yes | Yes | No | No |
| `TaskCreated` | a task is created via `TaskCreate` | Yes | No | No | No |
| `TaskCompleted` | a task is marked completed | Yes | No | No | No |
| `Stop` | Claude finishes responding | Yes | Yes | No | No |
| `StopFailure` | turn ends on an API error | No | No | No | No |
| `TeammateIdle` | an agent-team teammate is about to idle | Yes | No | No | No |
| `ConfigChange` | a configuration file changes mid-session | Yes, except `policy_settings` | No | No | No |
| `CwdChanged` | working directory changes | No | No | No, `watchPaths` only | No |
| `DirectoryAdded` | `/add-dir` or SDK `register_repo_root` | No | No | No | No |
| `FileChanged` | a watched file changes on disk | No | No | No, `watchPaths` only | No |
| `WorktreeCreate` | a worktree is being created | Yes, non-zero fails creation | No | Replaces git behaviour wholesale | No |
| `WorktreeRemove` | a worktree is being removed | No | No | No | No |
| `PreCompact` | before context compaction | Yes | No | No | No |
| `PostCompact` | after compaction completes | No | No | No | No |
| `SessionEnd` | session terminates | No | No | No | No |
| `Elicitation` | an MCP server requests user input | Yes | No | Yes, `content` on accept | No |
| `ElicitationResult` | after the user answers an elicitation | Yes | No | Yes, `content` override | No |

Two structural facts constrain everything below.

**The hook path is never a boundary.** A `PreToolUse` hook that times out does not block:
"A timed-out `PreToolUse` hook doesn't block the tool call ... don't count on a stalled hook
to act as a gate" (https://code.claude.com/docs/en/hooks.md#timeouts). A hook that cannot
start lands in the same non-blocking bucket, so "a mistyped path in `settings.json` leaves
the gate silently disabled" (https://code.claude.com/docs/en/hooks.md#other-exit-codes).
This is decision D1 on the map issue restated by the vendor: the hook path is best-effort by
construction, and only the permission system is a boundary. The permissions page says so
directly: "use the [permission system] rather than a hook to enforce a hard allow or deny"
(https://code.claude.com/docs/en/hooks.md#common-fields).

**Hooks can tighten but not loosen.** "`PreToolUse` hooks fire before any permission-mode
check, in every permission mode, including `dontAsk`. A hook that returns
`permissionDecision: "deny"` blocks the tool even in `bypassPermissions` mode or with
`--dangerously-skip-permissions`. ... The reverse is not true: a hook returning `"allow"`
doesn't bypass deny rules from settings"
(https://code.claude.com/docs/en/hooks-guide.md#hooks-and-permission-modes). For handrail
this is load-bearing: a rule whose Action is block is honoured in every permission mode,
which is the strongest guarantee the hook path offers and the reason `PreToolUse` stays the
spine of the model.

## 2. handrail's six-event core, checked against the reference

`docs/spec.md` section 1 fixes the core at `PreToolUse`, `PostToolUse`, `UserPromptSubmit`,
`SessionStart`, `SessionEnd`, `Stop`. Every one of the six exists in Claude Code under the
same name. Their capabilities as documented:

| handrail event | Claude Code capability | Fit with spec section 1 |
| :--- | :--- | :--- |
| `PreToolUse` | block, ask, defer, allow, inject, rewrite | Exceeds the model. handrail uses block and warn only. |
| `PostToolUse` | inject only, `decision: "block"` adds a reason next to the result but the tool already ran | Matches warn. Cannot block, so a `block` Action here degrades. |
| `UserPromptSubmit` | block, inject | Matches both Actions exactly. |
| `SessionStart` | inject only | Matches warn. A `block` Action degrades. |
| `SessionEnd` | nothing; "Claude Code discards their JSON output fields" | Matches neither Action. Side effects only. |
| `Stop` | block, meaning prevent stopping, plus inject | The block here is not a guardrail block. See below. |

Three notes the spec does not currently record.

`PostToolUse`'s `decision: "block"` is not a block. The reference: "`"block"` adds the
`reason` next to the tool result. Claude still sees the original output"
(https://code.claude.com/docs/en/hooks.md#posttooluse-decision-control), under a warning that
"the tool has already run by the time the hook fires, so any files written, commands
executed, or network requests sent have already taken effect". handrail's Outcome vocabulary
should not treat this value as producing a block Outcome. It is a warn with a stronger
presentation.

`Stop`'s block is an inversion. Exit 2 on `Stop` "prevents Claude from stopping, continues
the conversation"
(https://code.claude.com/docs/en/hooks.md#exit-code-2-behavior-per-event). A rule that blocks
on `Stop` makes the agent keep working rather than stopping it. The spec's Action vocabulary,
where block means "deny, with the rule's message as the reason", does not describe this. If
`Stop` stays in the ring, v2 needs to say which of the two meanings a `block` Action carries
there, or restrict `Stop` to warn.

`Stop` also carries a cap. "Claude Code overrides a Stop hook after it blocks eight times in
a row without progress", raisable with `CLAUDE_CODE_STOP_HOOK_BLOCK_CAP`, and the hook is
expected to read `stop_hook_active` from its input and exit early when true
(https://code.claude.com/docs/en/hooks-guide.md#stop-hook-hits-the-block-cap). A handrail
`Stop` rule that blocks must surface `stop_hook_active` as a canonical field or it will loop.

`SessionEnd` earns its place only as a side-effect seam. It has a 1.5-second shared budget
across all `SessionEnd` hooks of any type, raisable to 60 seconds through a per-hook
`timeout` in a settings file, though "timeouts set on plugin-provided hooks don't raise the
budget" (https://code.claude.com/docs/en/hooks.md#sessionend). A handrail plugin shipping a
`SessionEnd` hook therefore gets 1.5 seconds regardless of what it asks for.

## 3. The `if` field, exact schema (for T4)

The `if` field is a hook-handler field, sibling to `type`, `timeout`, `statusMessage`, and
`once`, not a matcher-group field
(https://code.claude.com/docs/en/hooks.md#common-fields).

**Type and syntax.** A string holding exactly one permission rule, in the same syntax as
`permissions.allow` / `deny` / `ask` entries: `"Bash(git *)"`, `"Edit(*.ts)"`. "The `if`
field holds exactly one permission rule. There is no `&&`, `||`, or list syntax for combining
rules; to apply multiple conditions, define a separate hook handler for each"
(https://code.claude.com/docs/en/hooks.md#common-fields). Alternation exists only at the
`matcher` level, which filters by tool name only
(https://code.claude.com/docs/en/hooks-guide.md#filter-by-tool-name-and-arguments-with-the-if-field).

**Which events accept it.** Exactly five, all of them tool events: `PreToolUse`,
`PostToolUse`, `PostToolUseFailure`, `PermissionRequest`, `PermissionDenied`. "On other
events, a hook with `if` set never runs"
(https://code.claude.com/docs/en/hooks.md#common-fields); the guide restates it as "Adding it
to any other event prevents the hook from running"
(https://code.claude.com/docs/en/hooks-guide.md#filter-by-tool-name-and-arguments-with-the-if-field).
This is a silent-failure trap: an `if` on `UserPromptSubmit` disables the hook without any
error.

**Bash matching semantics**, verbatim from the reference's table
(https://code.claude.com/docs/en/hooks.md#bash-if-matching). Leading `VAR=value` assignments
are stripped before matching.

| `if` pattern | Bash command | Hook runs? | Why |
| :--- | :--- | :--- | :--- |
| `Bash(git *)` | `FOO=bar git push` | yes | leading assignments are stripped; `git push` matches |
| `Bash(git *)` | `npm test && git push` | yes | each subcommand is checked; `git push` matches |
| `Bash(rm *)` | `echo $(rm -rf /)` | yes | commands inside `$()` and backticks are checked; `rm -rf /` matches |
| `Bash(rm *)` | `echo $(date)` | no | no subcommand matches `rm *` |
| `Bash(git push *)` | `echo $(date)` | yes | patterns that specify more than the command name run the hook anyway on `$()`, backticks, or `$VAR` |

**What happens when it cannot parse a command.** This is the question the ticket asks
directly. "The filter also fails open, running your hook regardless of pattern, when the Bash
command can't be parsed. Because the `if` filter is best-effort, use the permission system
rather than a hook to enforce a hard allow or deny"
(https://code.claude.com/docs/en/hooks.md#common-fields). Fail open means the hook runs, not
that the tool call proceeds unchecked, so `if` is safe as an optimisation and unsafe as a
gate. A handrail rule whose selection logic depends on `if` alone would be a gate; a handrail
rule that uses `if` to avoid spawning the engine process and re-evaluates its own conditions
inside the process is an optimisation. Only the second is sound.

**File-tool path semantics.** "In an `if` condition for a file tool, a single-segment
directory pattern like `"Edit(src/**)"` matches only the `src` directory in the working
directory and the files under it. To match a directory named `src` at any depth, write
`"Edit(**/src/**)"`. Before v2.1.214, `"Edit(src/**)"` matched a directory named `src` at any
depth" (https://code.claude.com/docs/en/hooks.md#common-fields). A compiler emitting `if`
patterns has to know the target version or emit the `**/` form unconditionally.

**Consequence for T4.** `if` compiles a handrail matcher's cheap half into the settings file
so the handrail process is not spawned on every tool call. It cannot compile a Condition
list, because it holds one rule with no boolean combination; the natural compilation is the
single most selective Term, with the rest re-checked in-process. It cannot be used at all on
the three non-tool events in handrail's core. And because it fails open on unparseable Bash,
the in-process re-check is not optional. The 50 ms budget question the map issue raises is
therefore about how often handrail is spawned at all, which `if` genuinely improves for
`Bash(...)`-shaped and `Edit(...)`-shaped rules, and not at all for prompt-level or
session-level rules.

## 4. `PermissionRequest`: what it changes about the model (for T10)

`PermissionRequest` "runs when Claude Code is about to ask you for permission"
(https://code.claude.com/docs/en/hooks.md#permissionrequest). It is a different gate from
`PreToolUse`: "PreToolUse hooks run before every tool call, whether or not it needs
permission. PermissionRequest hooks run only when Claude Code is about to ask you for
permission, or when it would otherwise auto-deny a call that can't prompt. Neither event
fires for `EndConversation`" (https://code.claude.com/docs/en/hooks.md#permissionrequest-input).

**Input.** Common fields plus `tool_name`, `tool_input`, and an optional
`permission_suggestions` array carrying the "always allow" options the dialog would show.
There is no `tool_use_id`.

**Output.** A `decision` object nested inside `hookSpecificOutput`
(https://code.claude.com/docs/en/hooks.md#permissionrequest-decision-control):

| Field | Meaning |
| :--- | :--- |
| `behavior` | `"allow"` or `"deny"`. Deny and ask rules are still evaluated, so `"allow"` does not override a matching deny rule |
| `updatedInput` | allow only; replaces the entire input object; the modified input is re-evaluated against deny and ask rules |
| `updatedPermissions` | allow only; array of permission update entries |
| `message` | deny only; tells Claude why |
| `interrupt` | deny only; if true, stops Claude |

**Exit code 2 is not honoured.** "A hook that exits 2 without a `decision` object leaves the
permission flow unchanged, and its stderr is discarded. Only the `decision` object can grant
or deny the request." The per-event table repeats it: "Exit code 2 isn't honored for this
event and the permission flow proceeds unchanged"
(https://code.claude.com/docs/en/hooks.md#exit-code-2-behavior-per-event).

**Where `ask` would live.** handrail's spec section 11 lists an `ask` action as a v1
non-goal. There are two distinct places it could land, and they are not equivalent.

1. `PreToolUse` with `permissionDecision: "ask"`. This *creates* a prompt where none would
   otherwise appear. It works in every permission mode and "forces a permission prompt in
   auto mode: the classifier can still deny the tool call, but it can't approve the call
   silently" (https://code.claude.com/docs/en/hooks.md#pretooluse-decision-control). The
   prompt carries a source label such as `[User]`, `[Project]`, `[Plugin]`, or `[Local]`.
2. `PermissionRequest` with `decision.behavior`. This *answers* a prompt that Claude Code
   was already going to show. It cannot create one.

For handrail, `ask` as a rule Action means "escalate this to the human", which is meaning 1.
`PreToolUse`'s `"ask"` is the correct compilation target and `PermissionRequest` is not.
`PermissionRequest` is the opposite operation: an auto-approver.

**Three sharp edges if `PermissionRequest` enters the ring anyway.**

- It has no prompt to answer in most non-interactive runs. "In non-interactive mode with the
  `-p` flag, that prompt only exists when the Agent SDK's `canUseTool` callback supplies it.
  In plain `-p` runs or with `--permission-prompt-tool`, use `PreToolUse` hooks for automated
  permission decisions instead" (https://code.claude.com/docs/en/hooks-guide.md#limitations).
- It defaults to deny where it cannot prompt. "In sessions that can't show a prompt, such as
  background subagents in non-interactive mode, Claude Code still runs these hooks, and if no
  hook returns a decision, it denies the tool call"
  (https://code.claude.com/docs/en/hooks.md#permissionrequest). A handrail hook registered on
  this event and silent by design would be indistinguishable from a hook that failed, and the
  call is denied either way. That is fail-closed behaviour handrail does not currently model.
- `updatedPermissions` writes to the user's settings files. Entry types are `addRules`,
  `replaceRules`, `removeRules`, `setMode`, `addDirectories`, `removeDirectories`, and the
  `destination` may be `session`, `localSettings` (`.claude/settings.local.json`),
  `projectSettings` (`.claude/settings.json`), or `userSettings` (`~/.claude/settings.json`)
  (https://code.claude.com/docs/en/hooks.md#permission-update-entries). handrail's Advisor is
  defined in `CONTEXT.md` as recommend-only, with accepted entries becoming the user's own
  harness config. `updatedPermissions` is the mechanism that would let the Advisor stop being
  recommend-only. That is a policy decision, not a capability gap, and it should be recorded
  as one.

**Recommendation.** `PermissionRequest` does not belong in the v2 core event model as a rule
target. It belongs in the Claude Code Adapter as a *promotion and advisory* surface: it is
where an accepted Advisor recommendation could be applied without the user hand-editing a
settings file, and it is where a future "auto-answer this prompt" feature would live. Adding
it as a rule event would give rule authors a fail-closed, interactive-only gate whose
semantics no other harness matches.

## 5. `PreToolUse` `updatedInput`: input rewrite (for T11)

Spec section 1 says "No input rewrite in v1." The capability exists and is well specified.

**Schema.** `hookSpecificOutput.updatedInput`, an object. "Modifies the tool's input
parameters before execution. Replaces the entire input object, so include unchanged fields
alongside modified ones. Combine with `"allow"` to auto-approve, or `"ask"` to show the
modified input to the user. For `"defer"`, ignored"
(https://code.claude.com/docs/en/hooks.md#pretooluse-decision-control).

**The concurrency hazard is documented and unfixable from handrail's side.** "When multiple
`PreToolUse` hooks return `updatedInput` to rewrite a tool's arguments, the last one to
finish takes effect. Since hooks run in parallel, the order is non-deterministic. Avoid
having more than one hook modify the same tool's input"
(https://code.claude.com/docs/en/hooks-guide.md#limitations). handrail can guarantee that its
own rules produce one rewrite per tool call by resolving them in-process before returning,
but it cannot prevent a second, non-handrail hook from racing it. A rewrite feature has to be
documented as best-effort in the presence of other hooks, in the same register as D1.

**Rewrite is also available at two other points**, both after the fact:
`PermissionRequest`'s `decision.updatedInput` (allow only, re-evaluated against deny and ask
rules) and `PostToolUse`'s `updatedToolOutput`, which "only changes what Claude sees. The
tool has already run" and whose value "must match the tool's output shape", with values that
do not match silently ignored for built-in tools
(https://code.claude.com/docs/en/hooks.md#posttooluse-decision-control). The reference gives
the pairing explicitly: "For redaction or transformation use cases, intercept at `PreToolUse`
for outbound tool inputs and `PostToolUse` for inbound tool results"
(https://code.claude.com/docs/en/hooks.md#decision-control).

**What `UserPromptSubmit` cannot do.** "`UserPromptSubmit`: can't replace the prompt; it only
injects `additionalContext` alongside it"
(https://code.claude.com/docs/en/hooks.md#decision-control). So prompt-level redaction is not
available at all on the Claude Code path. If v2 wants a redaction story, it covers tool input
and tool output, not user prompts.

**Recommendation.** Input rewrite is worth reopening, but as a fourth Action rather than a
new event, and it is the one place where handrail's "exactly three Outcomes" invariant
(`CONTEXT.md`, Outcome) genuinely has to change or the feature has to be modelled as
something other than an Outcome. The smallest shape that does not break the invariant is a
rewrite that is a *property of a warn*: the rule proceeds, injects its message, and hands
back a modified input. That keeps allow / warn / block as the three Outcomes and makes
rewrite an orthogonal axis. The alternative, a fourth Outcome, forces every Adapter's
capability matrix to grow a row that only Claude Code can satisfy today.

## 6. Content injection: the `additionalContext` contract

handrail's warn Action is defined as "proceed, but inject the rule's message into the agent's
context" (`CONTEXT.md`, Action). The mechanism is `additionalContext`, and its contract has
four properties a v2 spec should state
(https://code.claude.com/docs/en/hooks.md#add-context-for-claude).

- **Delivery point varies by event.** `SessionStart`, `Setup`, `SubagentStart` deliver at the
  start of the conversation; `UserPromptSubmit` and `UserPromptExpansion` alongside the
  prompt; `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `PostToolBatch` next to the tool
  result; `Stop` and `SubagentStop` at the end of the turn, continuing the conversation.
- **10,000 characters.** Output strings including `additionalContext` are capped; over the
  cap, the text is written to a file and Claude gets a path and a preview.
- **Replay on resume.** "Claude Code saves the injected text in the session transcript. For
  mid-session events like `PostToolUse` or `UserPromptSubmit`, when you resume with
  `--continue` or `--resume`, Claude Code replays the saved text rather than re-running the
  hook for past turns". A handrail warn message is therefore durable, and anything
  time-varying in it goes stale.
- **Phrasing matters.** "Write the text as factual statements rather than imperative system
  instructions. ... Text framed as out-of-band system commands can trigger Claude's
  prompt-injection defenses, which causes Claude to surface the text to you instead of
  treating it as context." This is direct guidance for how handrail should render a rule's
  message.

## 7. Codex CLI equivalence

Sources: the official docs, which moved during the life of this project.
`https://developers.openai.com/codex/llms.txt` now returns a 308 permanent redirect to
`https://learn.chatgpt.com/docs/llms.txt`, so the Codex documentation lives under
`learn.chatgpt.com/docs`. Pages used: https://learn.chatgpt.com/docs/hooks.md,
https://learn.chatgpt.com/docs/config-file/config-reference.md,
https://learn.chatgpt.com/docs/import.md. Repository claims cite
`github.com/openai/codex` at commit `536f86e5cc9ec1ff38457d099bf320b9d08eeeba` (`main`,
2026-08-21); latest release at that time was `rust-v0.149.0`, 2026-08-20.

**Eleven events**, canonical spellings from `HOOK_EVENT_NAMES` in
`codex-rs/hooks/src/lib.rs` L23-L35 and from `HookEventsToml` in
`codex-rs/config/src/hook_config.rs` L36-L59:

`PreToolUse`, `PermissionRequest`, `PostToolUse`, `PreCompact`, `PostCompact`,
`SessionStart`, `SessionEnd`, `UserPromptSubmit`, `SubagentStart`, `SubagentStop`, `Stop`.

Nine of the eleven honour `matcher`; `UserPromptSubmit` and `Stop` do not
(`HOOK_EVENT_NAMES_WITH_MATCHERS`, `codex-rs/hooks/src/lib.rs` L42-L52).

**Claude shape, not a Claude-compatibility promise.** The internal engine type is named
`ClaudeHooksEngine` (`codex-rs/hooks/src/engine/mod.rs` L160); the generated schemas describe
divergences against Claude as the baseline, for example "Claude requires `reason` when
`decision` is `block`; we enforce that semantic rule during output parsing rather than in the
JSON schema" (`codex-rs/hooks/schema/generated/stop.command.output.schema.json`); plugin
hooks receive `CLAUDE_PLUGIN_ROOT` and `CLAUDE_PLUGIN_DATA` "for compatibility with existing
plugin hooks" (https://learn.chatgpt.com/docs/hooks.md); and `/import` migrates Claude Code
hooks into Codex hooks (https://learn.chatgpt.com/docs/import.md). No published sentence says
"Claude-compatible by design", so that phrase is not established from a primary source.

**Capabilities, per https://learn.chatgpt.com/docs/hooks.md:**

| Capability | Codex events |
| :--- | :--- |
| Block or deny | `PreToolUse` (`permissionDecision: "deny"`, legacy `decision: "block"`, or exit 2); `PermissionRequest` (`decision.behavior: "deny"`); `UserPromptSubmit` (`decision: "block"` or exit 2); `PostToolUse` (`decision: "block"`, replaces the result, cannot undo side effects); `PreCompact` / `PostCompact` (`continue: false`) |
| Inject context | `SessionStart`, `SubagentStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`. `additionalContext` exists only in these five hook-specific output types |
| Rewrite input | `PreToolUse` only, via `permissionDecision: "allow"` plus `updatedInput` |
| Continue the loop | `Stop`, `SubagentStop` (`decision: "block"` plus `reason`) |

**Three divergences that matter to handrail's capability matrix.**

`PreToolUse`'s `"ask"` is parsed but not supported. The wire enum accepts
`"allow" | "deny" | "ask"`
(`codex-rs/hooks/schema/generated/pre-tool-use.command.output.schema.json`), but the docs
state that `"ask"`, legacy `"approve"`, `continue: false`, `stopReason`, and `suppressOutput`
are parsed and not yet supported for `PreToolUse`: Codex marks the run failed, reports the
error, and continues the tool call. So an `ask` Action degrades on Codex today, and degrades
loudly rather than silently.

`PermissionRequest` has no `"ask"` at all: `decision.behavior` is `"allow" | "deny"` only
(`codex-rs/hooks/schema/generated/permission-request.command.output.schema.json`). To defer to
the normal approval prompt, a hook returns no decision. Resolution across hooks is "any deny
wins, otherwise an allow proceeds, otherwise the normal approval flow runs". Its
`updatedInput`, `updatedPermissions`, and `interrupt` fields exist in the schema but are
documented as reserved and **fail closed** when present.

`Stop`'s `decision: "block"` is not a rejection. Per the docs, it "creates a continuation
prompt using `reason` as the new user prompt text". That is a third distinct meaning of
`block` on `Stop`, different again from Claude Code's, and it strengthens the case for
restricting `Stop` to warn in handrail's model.

**No `if` field, and `if` is actively rejected on import.** A Codex `MatcherGroup` has exactly
two fields, `matcher` and `hooks` (`codex-rs/config/src/hook_config.rs` L139-L145), where
`matcher` is a regex string. The Claude Code importer skips any matcher group that carries an
`if` key, or any key other than `matcher` and `hooks`, silently:

```rust
if group_object.contains_key("if")
    || group_object.keys().any(|key| !matches!(key.as_str(), "matcher" | "hooks"))
{
    continue;
}
```

(`codex-rs/external-agent-migration/src/hooks_cla.rs` L118-L124). Only the top-level
`HooksFile` struct sets `#[serde(deny_unknown_fields)]`
(`codex-rs/config/src/hook_config.rs` L10-L17), so an `if` key written by hand into a Codex
matcher group is dropped by the deserializer rather than rejected. That last point is inferred
from the serde attributes; no documentation statement covers it.

**Codex says the same thing Claude Code says about enforcement.** "Some specialized tool paths
can opt out of the default hook path. Treat tool hooks as a useful guardrail, not a complete
enforcement boundary" (https://learn.chatgpt.com/docs/hooks.md). Hosted tools such as
`WebSearch` run no hooks at all. Two independent vendors now state D1 in their own words.

**Tool coverage** (https://learn.chatgpt.com/docs/hooks.md): shell and unified exec match as
`Bash`; `apply_patch` is matchable as `apply_patch`, `Edit`, or `Write` while the input always
reports `tool_name: "apply_patch"`; MCP tools appear as `mcp__server__tool`; `spawn_agent`
also matches `Agent`. This is directly relevant to the Tool kind mapping in `CONTEXT.md`: the
Codex Adapter's `file_edit` kind has one tool name on the wire and three matchable aliases.

## 8. Gemini CLI equivalence

Sources: `https://geminicli.com/llms.txt` exists and lists a "Custom hooks" feature;
`https://geminicli.com/llms-full.txt` returns 404. Pages used:
https://geminicli.com/docs/hooks/, https://geminicli.com/docs/hooks/reference,
https://geminicli.com/docs/hooks/writing-hooks, https://geminicli.com/docs/hooks/best-practices,
https://geminicli.com/docs/reference/policy-engine, https://geminicli.com/docs/reference/configuration.
Repository claims cite `google-gemini/gemini-cli` at commit
`30573d2e4d85bdc2c0ae8218c377cd410336da77` (2026-08-20); latest stable release `v0.56.0`,
2026-08-19. Note that the published docs site carries last-updated stamps of April 2026 while
its event list matches repository HEAD exactly.

**Eleven events**, from the `HookEventName` enum in
`packages/core/src/hooks/types.ts` L42-L54 and the table at https://geminicli.com/docs/hooks/:

`BeforeTool`, `AfterTool`, `BeforeAgent`, `AfterAgent`, `BeforeModel`, `AfterModel`,
`BeforeToolSelection`, `SessionStart`, `SessionEnd`, `PreCompress`, `Notification`.

**Hooks are not experimental.** No preview, beta, or experimental marking anywhere in
`docs/hooks/*.md` or in the non-test source; `hooksConfig.enabled` defaults to `true`
(https://geminicli.com/docs/reference/configuration). The sidebar entry carries no experiment
badge, unlike sibling entries that do.

**`docs/spec.md` section 12's paper-check map is confirmed.** Section 12 records
`PreToolUse` to `BeforeTool`, `PostToolUse` to `AfterTool`, `UserPromptSubmit` to
`BeforeAgent`, `Stop` to `AfterAgent`, and `SessionStart` / `SessionEnd` verbatim. All five
mappings hold against the primary sources. The paper check was right.

**Capabilities**, from https://geminicli.com/docs/hooks/reference and the firing sites:

| Capability | Gemini events |
| :--- | :--- |
| Block | `BeforeTool` (blocks the tool, turn continues), `AfterTool` (hides or replaces the result), `BeforeAgent` (blocks the turn, discards the prompt), `AfterAgent` (rejects the response, forces a retry turn), `BeforeModel`, `AfterModel` |
| Inject context | `SessionStart`, `BeforeAgent`, `AfterTool`, all via `hookSpecificOutput.additionalContext` |
| Rewrite tool input | `BeforeTool` only, via `hookSpecificOutput.tool_input`, which merges over and overrides the model's arguments |
| Advisory only | `SessionEnd`, `PreCompress`, `Notification` |

**The decision vocabulary, and the one thing Gemini cannot do.** `HookDecision` in
`packages/core/src/hooks/types.ts` L130-L136 is
`'ask' | 'block' | 'deny' | 'approve' | 'allow' | undefined`. The published docs mention only
`"allow"` and `"deny"` (alias `"block"`). From the source: `'block'` and `'deny'` are exact
synonyms; `'ask'` is honoured on `BeforeTool` only, where it forces `PolicyDecision.ASK_USER`
(`packages/core/src/scheduler/hook-utils.ts` L76-L79 into
`packages/core/src/scheduler/scheduler.ts` L652-L657), and it is undocumented on the docs
site; `'approve'` is consumed nowhere; `'allow'` is only ever written as the aggregator's
default and no code branches on it.

The consequence is sharp: **a Gemini CLI hook cannot auto-approve.** The scheduler hard-codes
`forcedDecision: hookDecision === 'ask' ? 'ask_user' : undefined`
(`packages/core/src/scheduler/scheduler.ts` L691-L693); no path produces `'allow'`. A hook can
deny, or escalate to a prompt, and nothing else. This is a capability-matrix row where the
three harnesses genuinely disagree, which is exactly the condition `docs/spec.md` section 4
names: "A third Adapter that disagrees is what turns a row into a flag."

**The real allow/deny/ask surface is the Policy Engine**, not hooks
(https://geminicli.com/docs/reference/policy-engine). `PolicyDecision` is
`ALLOW | DENY | ASK_USER`; rules are TOML `[[rule]]` arrays; priority tiers are Default 1,
Extension 2, Workspace 3, User 4, Admin 5 with `final = tier + (toml_priority / 1000)`; a
global `deny` rule with no `argsPattern` strips the tool from the model's context entirely,
which is the same behaviour as a bare-tool-name deny in Claude Code; and `ask_user` in
non-interactive mode becomes `deny`. This is the Gemini Adapter's Promotion target, the
counterpart to a Claude Code permission deny and a Codex execpolicy prefix rule. One caveat
the docs record themselves: the workspace tier is documented as currently non-functional.

**No `if` field, and a matcher with three separate traps.** `HookDefinition` has exactly
three fields, `matcher`, `sequential`, `hooks`
(`packages/core/src/hooks/types.ts` L112-L116). Matcher evaluation lives in
`packages/core/src/hooks/hookPlanner.ts` L74-L120 and behaves as follows.

- Only five events consult it: `BeforeTool` and `AfterTool` as a **regex** against
  `tool_name`; `SessionStart`, `SessionEnd`, and `PreCompress` as **exact string** equality
  against the source, reason, or trigger. On `BeforeAgent`, `AfterAgent`, `Notification`,
  `BeforeModel`, `AfterModel`, and `BeforeToolSelection` the matcher is **silently ignored and
  the hook always runs**. A narrowing matcher on those six is a no-op with no warning.
- On regex parse failure the planner silently degrades to exact string equality:
  `try { new RegExp(matcher).test(toolName) } catch { return matcher === toolName }`
  (`hookPlanner.ts` L104-L113). No throw, no log, and not match-all.
- Regexes are unanchored, because `regex.test()` is a substring search, so `matcher: "shell"`
  matches `run_shell_command`. The settings-schema description at
  `packages/cli/src/config/settingsSchema.ts` L3450-L3454 claims support for a slash-delimited
  `/pattern/` form, which is not implemented.

The last two points are established from source and are documented nowhere, so treat them as
source-derived rather than as a vendor contract.

**Events with no handrail or Claude Code analogue.** `BeforeModel`, `AfterModel`, and
`BeforeToolSelection` expose the model request and response directly: request rewriting,
synthetic response mocking, per-chunk redaction, and dynamic filtering of the tool space via
`toolConfig.allowedFunctionNames`. `AfterTool`'s `tailToolCallRequest` queues a follow-up tool
call whose result replaces the original response. None of these have a Claude Code or Codex
counterpart, and none of them should enter a cross-harness core.

**Events Gemini lacks.** No subagent events at all: no `SubagentStart`, `SubagentStop`, or
equivalent exists, and sub-agents are gated by the Policy Engine's `subagent` rule field
instead. No post-compaction event. `Notification` exists but has exactly one
`notification_type`, `ToolPermission`, and is observability only.

**Trust.** Project hooks are fingerprinted by `name:command` in `~/.gemini/trusted_hooks.json`
and are skipped entirely in untrusted folders. Gemini also injects `CLAUDE_PROJECT_DIR` into
the hook environment "For compatibility"
(`packages/core/src/hooks/hookRunner.ts` L348-L356), alongside its own `GEMINI_*` variables.
All three harnesses now ship per-hook trust keyed to the hook's own content.

## 9. Cross-harness intersection

The ticket's selection criterion is cross-harness, not Claude-only. Laid out against all
three harnesses, with the question "what would a guardrail actually do with it" answered per
row:

| Event | Claude Code | Codex CLI | Gemini CLI | What a guardrail does with it |
| :--- | :--- | :--- | :--- | :--- |
| `PreToolUse` | yes | yes | `BeforeTool` | Everything. Block a command, warn on a path, rewrite an argument. The spine. |
| `PostToolUse` | yes | yes | `AfterTool` | Warn after the fact, redact a result. Cannot undo. |
| `UserPromptSubmit` | yes | yes | `BeforeAgent` | Block a prompt, attach standing context. |
| `SessionStart` | yes | yes | yes | Inject the effective ruleset as context, once. |
| `SessionEnd` | yes | yes | yes | Side effects only. Cleanup, audit write. |
| `Stop` | yes | yes | `AfterAgent` | Keep the agent working until a condition holds. Not a guardrail block. |
| `PreCompact` | yes | yes | `PreCompress` | Nothing a guardrail wants. Blocking compaction is a context-management concern. |
| `PostCompact` | yes | yes | absent | Re-inject the ruleset after a compaction drops it. Real, but 2 of 3. |
| `SubagentStart` | yes | yes | absent | Propagate the ruleset into a subagent's context. Real, but 2 of 3. |
| `SubagentStop` | yes | yes | absent | Same as `Stop`, scoped to a subagent. 2 of 3. |
| `PermissionRequest` | yes | yes | absent as an event | Auto-answer a prompt. Fail-closed, interactive-only. See section 4. |
| `Notification` | yes | absent | yes, one type, observability only | Nothing. No capability on either harness that has it. |
| `PostToolUseFailure` | yes | absent | absent | Codex fires `PostToolUse` on non-zero-exit Bash and Gemini puts `error` in `tool_response`, so the signal exists elsewhere. |
| `PostToolBatch` | yes | absent | absent | Inject context that depends on the whole batch. Claude-only. |
| `PermissionDenied` | yes | absent | absent | Log auto-mode denials. Claude-only, auto mode only. |
| `UserPromptExpansion` | yes | absent | absent | Gate a slash command. Claude-only. |
| `Setup`, `InstructionsLoaded`, `MessageDisplay`, `TaskCreated`, `TaskCompleted`, `TeammateIdle`, `ConfigChange`, `CwdChanged`, `DirectoryAdded`, `FileChanged`, `WorktreeCreate`, `WorktreeRemove`, `Elicitation`, `ElicitationResult`, `StopFailure` | yes | absent | absent | Claude-only, 15 events. |
| `BeforeModel`, `AfterModel`, `BeforeToolSelection` | absent | absent | yes | Gemini-only, 3 events. |

**The intersection of all three harnesses is handrail's existing six events plus
`PreCompact`.** That is the headline result. handrail's v1 event model was already the
cross-harness core, and widening it against a third harness adds exactly one event, which is
the one event on the list with no guardrail use.

Two rows are worth naming as capability-matrix flags rather than event-model changes.

**`ask` is a three-way disagreement.** Claude Code's `PreToolUse` supports
`permissionDecision: "ask"` and forces a prompt in every mode
(https://code.claude.com/docs/en/hooks.md#pretooluse-decision-control). Codex accepts `"ask"`
in the wire enum but does not support it: the run is marked failed and the tool call
continues. Gemini supports `'ask'` on `BeforeTool` but the value is undocumented, and Gemini
compensates by making `'ask'` the *only* escalation available, since no hook can auto-approve.
Any v2 `ask` Action needs a matrix row with three different values.

**Auto-approve is a two-way disagreement.** Claude Code's `PreToolUse` `"allow"` and
`PermissionRequest` `decision.behavior: "allow"` both skip a prompt. Codex's
`PermissionRequest` `"allow"` proceeds without surfacing the prompt. Gemini has no path that
produces an allow at all. handrail does not currently express allow as an Action
(`CONTEXT.md`: "Allow is the absence of a match, not an action"), so this disagreement costs
nothing today. It becomes a flag the moment a rule can auto-approve.

## 10. Recommendation

**Keep the six-event core unchanged.** The intersection over three harnesses is the six plus
`PreCompact`, and `PreCompact` earns nothing. Adding Claude-only events would give rule
authors a vocabulary that degrades to nothing on two of three harnesses, which is the
condition Degradation exists to report, not a condition to design into the core.

**Add a documented second tier, not a wider core.** `SubagentStart`, `SubagentStop`, and
`PostCompact` are the only events outside the core that (a) exist on more than one harness,
and (b) do something a guardrail wants: propagate the ruleset into a subagent, keep a subagent
working, and re-inject the ruleset after compaction drops it. All three are absent from
Gemini. If v2 wants them, the honest shape is an event tier whose Adapter support is declared
in the capability matrix and whose absence degrades to skip, announced at Sync time. That is
the mechanism `docs/spec.md` section 4 already defines; it does not need a new one.

**Reject `PermissionRequest` as a rule event.** It is fail-closed where it cannot prompt, it
does not exist in plain `-p` runs, it has no Gemini counterpart, and on Codex its interesting
fields are reserved and fail closed. Its real value is as the Claude Code Adapter's mechanism
for applying an accepted Advisor recommendation, through `updatedPermissions`, without the
user hand-editing a settings file. Record that as an Advisor question, not an event-model one.

**Put `ask` on `PreToolUse`, not on `PermissionRequest`.** `PreToolUse`'s `"ask"` creates a
prompt; `PermissionRequest`'s decision answers one. handrail's `ask` means escalate to the
human, which is the first. Claude Code supports it fully, Gemini supports it on `BeforeTool`,
Codex does not support it yet and fails loudly. That is a clean matrix row.

**Model input rewrite as an axis on warn, not a fourth Outcome.** All three harnesses support
rewriting tool input at exactly one place, the pre-tool event, and nowhere else that matters:
Claude Code's `hookSpecificOutput.updatedInput`, Codex's `updatedInput` paired with
`permissionDecision: "allow"`, Gemini's `hookSpecificOutput.tool_input`. A rewrite that
accompanies a warn keeps `CONTEXT.md`'s "exactly three Outcomes" invariant intact and needs no
capability row that only one harness can satisfy. Two caveats belong in the spec text: the
last hook to finish wins when several rewrite the same input, and hooks run in parallel
(https://code.claude.com/docs/en/hooks-guide.md#limitations), so a rewrite is best-effort in
the presence of foreign hooks; and Claude Code's `updatedInput` replaces the entire input
object, so an Adapter must echo unchanged fields.

**Fix three definitional problems the current spec carries.**

1. `PostToolUse`'s `decision: "block"` produces a warn, not a block, on every harness that has
   it. The tool has already run. Say so, so a `block` Action on `PostToolUse` degrades to warn
   rather than appearing to work.
2. `block` on `Stop` means three different things: Claude Code continues the conversation,
   Codex creates a continuation prompt from the `reason` text, Gemini forces a retry turn.
   None of them is "deny with the rule's message as the reason". Restrict `Stop` to warn, or
   define a distinct Action for it.
3. `SessionEnd` has no decision control on any of the three harnesses and, on Claude Code, a
   1.5-second shared budget that a plugin-provided `timeout` cannot raise. It belongs in the
   model as a side-effect seam only.

**Treat `if` as an optimisation, never as a selector.** It holds one permission rule with no
boolean combination, it works on five Claude Code tool events and nowhere else, it fails open
on unparseable Bash, and neither Codex nor Gemini has any equivalent (Codex's importer
explicitly drops any matcher group carrying it). A handrail `sync` may emit `if` to reduce
process spawns for `Bash(...)`- and `Edit(...)`-shaped rules, and must re-evaluate every
Condition in-process regardless. That is the answer to T4 and it bounds what the 50 ms budget
revision can assume.

**Add `stop_hook_active` to the canonical payload if `Stop` rules can act.** Claude Code
overrides a `Stop` hook after eight consecutive blocks
(https://code.claude.com/docs/en/hooks-guide.md#stop-hook-hits-the-block-cap) and expects the
hook to read the flag; Codex and Gemini both carry the same field on their turn-end events.
Without it a `Stop` rule loops.

**The dormant network-condition row stays dormant.** The map issue records ADR 0005 carrying a
row that compiles domain conditions to `WebFetch(domain:)` denies, waiting on a canonical URL
field, and marks it as depending on T5 and T9. T9's answer is that the field cannot be defined
cross-harness today. Claude Code has a first-class domain rule syntax with precise wildcard
semantics (https://code.claude.com/docs/en/permissions.md#webfetch). Codex states that hosted
tools such as `WebSearch` do not run hooks at all
(https://learn.chatgpt.com/docs/hooks.md), so there is no event to carry a URL. Gemini's
primary sources name no URL or domain field on any hook payload. A canonical `url` or `domain`
field would be a Claude-only field in a cross-harness payload. It should stay reserved.

**One further note for the threat model.** Decision D1 says the hook path is documented as
best-effort because `disableAllHooks` means it can never be a boundary. Two more vendors now
say the same thing in their own words: Claude Code's "use the permission system rather than a
hook to enforce a hard allow or deny" (https://code.claude.com/docs/en/hooks.md#common-fields)
and Codex's "Treat tool hooks as a useful guardrail, not a complete enforcement boundary"
(https://learn.chatgpt.com/docs/hooks.md). D1 can cite all three rather than reasoning from
`disableAllHooks` alone.

## 11. What could not be established from a primary source

- **Claude Code's `if` behaviour on non-Bash, non-file tools.** The reference documents Bash
  matching and file-tool path matching. It does not say how `if` evaluates against an MCP tool
  or an arbitrary built-in with no specifier syntax.
- **Whether a Claude Code `PreToolUse` `updatedInput` is re-checked against deny and ask
  rules.** The reference states this explicitly for `PermissionRequest`'s `updatedInput` ("The
  modified input is re-evaluated against deny and ask rules") and says nothing either way for
  `PreToolUse`'s.
- **The maturity tier of Codex CLI Hooks.** Codex publishes a feature-maturity vocabulary
  (https://learn.chatgpt.com/docs/feature-maturity.md), and neither the hooks page nor the
  `features.hooks` config entry carries a label, unlike sibling entries that do. Only that
  hooks are enabled by default is established.
- **Codex behaviour for an `if` key written by hand into a matcher group.** `MatcherGroup` does
  not set `deny_unknown_fields`, so serde would ignore it, but no documentation covers this.
  Treat "silently ignored" as source-derived.
- **Codex's `mcp_tool` handler type.** It exists on `main` with a live executor
  (`codex-rs/core/src/hook_mcp_executor.rs`) and appears nowhere in the published docs.
- **Gemini CLI's settings precedence for hooks.** https://geminicli.com/docs/hooks/ orders
  project above user above system above extensions; https://geminicli.com/docs/reference/configuration
  states that system settings take precedence over all other settings files. The two primary
  pages contradict each other and neither can be used to resolve the other.
- **Whether Gemini CLI hooks fire inside sub-agent execution contexts.** No event exists for it
  and no documentation states either way.
- **Whether Gemini's `'ask'`, `'approve'`, and `'allow'` decision values are intentionally
  undocumented or are drift.** The docs mention only `"allow"` and `"deny"` / `"block"`; the
  source shows `'ask'` functional on `BeforeTool` and `'approve'` dead.
- **Whether Gemini's unanchored-regex matcher and its silent fallback to string equality on an
  invalid regex are intended.** Both are established from source and documented nowhere.
- **Any harness's stated per-event hook latency.** All three publish timeouts; none publishes a
  latency budget, so the 50 ms figure in handrail's spec has no vendor counterpart to check
  against.
