# Claude Code extension surface (hooks, settings, permissions, plugins)

Date: 2026-08-15
Purpose: primary-source research for hookify issue #2, documenting the complete current Claude Code extension surface against the official docs at code.claude.com.

All claims below come from the official Claude Code documentation, fetched on 2026-08-15 as raw markdown from the `.md` endpoints listed in `https://code.claude.com/docs/llms.txt`. Primary pages used:

- Hooks reference: https://code.claude.com/docs/en/hooks.md
- Hooks guide: https://code.claude.com/docs/en/hooks-guide.md
- Settings: https://code.claude.com/docs/en/settings.md
- Permissions: https://code.claude.com/docs/en/permissions.md
- Permission modes: https://code.claude.com/docs/en/permission-modes.md
- Plugins (create): https://code.claude.com/docs/en/plugins.md
- Plugins reference: https://code.claude.com/docs/en/plugins-reference.md
- Plugin marketplaces: https://code.claude.com/docs/en/plugin-marketplaces.md
- Plugin dependencies: https://code.claude.com/docs/en/plugin-dependencies.md
- Subagents: https://code.claude.com/docs/en/sub-agents.md
- Skills (also serves the old slash-commands URL): https://code.claude.com/docs/en/skills.md

---

## 1. Hook events

Source for this whole section: https://code.claude.com/docs/en/hooks (the hooks reference).

### 1.1 Full event catalog

The hooks reference documents 31 events. Cadence: `SessionStart` and `SessionEnd` fire once per session; `UserPromptSubmit`, `Stop`, and `StopFailure` once per turn; `PreToolUse` and `PostToolUse` on every tool call (except `EndConversation` calls, which skip both).

| Event | When it fires |
| :-- | :-- |
| `SessionStart` | Session begins or resumes |
| `Setup` | `claude --init-only`, or `--init` / `--maintenance` with `-p`. One-time prep for CI/scripts |
| `UserPromptSubmit` | User submits a prompt, before Claude processes it |
| `UserPromptExpansion` | A user-typed command expands into a prompt (slash command or MCP prompt). Can block the expansion |
| `PreToolUse` | Before a tool call executes. Can block |
| `PermissionRequest` | When a tool call needs a permission decision |
| `PermissionDenied` | When auto mode denies a tool call |
| `PostToolUse` | After a tool call succeeds |
| `PostToolUseFailure` | After a tool call fails |
| `PostToolBatch` | After a full batch of parallel tool calls resolves, before the next model call |
| `Notification` | When Claude Code sends a notification |
| `MessageDisplay` | While assistant message text is displayed (display-only rewriting) |
| `SubagentStart` | When a subagent is spawned |
| `SubagentStop` | When a subagent finishes |
| `TaskCreated` | When a task is created via `TaskCreate` |
| `TaskCompleted` | When a task is being marked completed |
| `Stop` | When Claude finishes responding |
| `StopFailure` | Turn ends due to an API error |
| `TeammateIdle` | An agent-team teammate is about to go idle |
| `InstructionsLoaded` | A CLAUDE.md or `.claude/rules/*.md` file is loaded into context |
| `ConfigChange` | A configuration file changes during a session |
| `CwdChanged` | The working directory changes (for example a `cd`) |
| `DirectoryAdded` | A working directory is added mid-session via `/add-dir` or the SDK `register_repo_root` request |
| `FileChanged` | A watched file changes on disk (matcher names the files to watch) |
| `WorktreeCreate` | A worktree is being created (`--worktree`, `isolation: "worktree"`, background session). Replaces default git behavior |
| `WorktreeRemove` | A worktree is being removed |
| `PreCompact` | Before context compaction. Can block compaction |
| `PostCompact` | After compaction completes |
| `Elicitation` | An MCP server requests user input during a tool call |
| `ElicitationResult` | After the user responds to an MCP elicitation, before the response returns to the server |
| `SessionEnd` | Session terminates |

There is no `SubagentStart if it exists` question anymore: `SubagentStart` is a documented event with matcher support on agent type.

### 1.2 Common stdin JSON fields

Every event receives these (command hooks on stdin, HTTP hooks as the POST body):

| Field | Description |
| :-- | :-- |
| `session_id` | Current session identifier |
| `prompt_id` | UUID of the user prompt being processed (matches the OTel `prompt.id`). Absent until the first user input. Requires v2.1.196+ |
| `transcript_path` | Path to conversation JSONL. Written asynchronously; may lag the in-memory conversation. Use `last_assistant_message` on Stop/SubagentStop instead of parsing it |
| `cwd` | Working directory when the hook is invoked |
| `permission_mode` | `"default"`, `"plan"`, `"acceptEdits"`, `"auto"`, `"dontAsk"`, or `"bypassPermissions"` (Manual arrives as `"default"`). Not present on all events |
| `effort` | Object with `level`: `"low"`, `"medium"`, `"high"`, `"xhigh"`, or `"max"`. Present for tool-context events when the model supports effort. Also exposed as `$CLAUDE_EFFORT` |
| `hook_event_name` | Name of the event that fired |

Inside a subagent (or with `--agent`), two more fields appear: `agent_id` (unique subagent id, only inside a subagent call) and `agent_type` (agent name, e.g. `"Explore"` or `"security-reviewer"`).

Example `PreToolUse` stdin (copied from the reference):

```json
{
  "session_id": "abc123",
  "prompt_id": "550e8400-e29b-41d4-a716-446655440000",
  "transcript_path": "/home/user/.claude/projects/.../transcript.jsonl",
  "cwd": "/home/user/my-project",
  "permission_mode": "default",
  "hook_event_name": "PreToolUse",
  "tool_name": "Bash",
  "tool_input": {
    "command": "npm test",
    "description": "Run test suite",
    "timeout": 120000,
    "run_in_background": false
  },
  "tool_use_id": "toolu_01ABC123..."
}
```

Notes: only `SessionStart` can receive a `model` field, and it is not guaranteed. Claude Code strips `OTEL_*` exporter variables from every subprocess it spawns, including hooks. Hooks run without a controlling terminal; `/dev/tty` is unavailable (use `terminalSequence` output instead).

### 1.3 Exit code semantics

Source: https://code.claude.com/docs/en/hooks (sections "Exit code output", "Exit code 2 behavior per event").

- **Exit 0**: success. JSON on stdout (first non-whitespace char `{`) is parsed for structured control; anything else is plain text. Plain-text stdout is added to Claude's context only on `UserPromptSubmit`, `UserPromptExpansion`, and `SessionStart`; on other events it goes to the debug log. Stderr on exit 0 goes to the debug log only; Claude never sees it.
- **Exit 2**: blocking error, on events that can block. Blocks whether or not JSON was printed; even a JSON `permissionDecision: "allow"` cannot override it. The blocking message is the JSON blocking reason if one was made, else stderr. On `Elicitation`/`ElicitationResult` an exit-2 hook's `hookSpecificOutput` is ignored.
- **Other exit codes (including 1)**: do NOT block on most events. With valid schema-passing JSON on stdout, Claude Code ignores the exit code and honors the JSON fields. With invalid/absent JSON, it is a non-blocking error: the action proceeds and the transcript shows a `<hook name> hook error` notice with the first stderr line prefixed `Failed with non-blocking status code:`. A hook that cannot start (missing file, not executable, exit 127) lands in the same bucket, so a mistyped path leaves a policy gate silently disabled. Exception: `WorktreeCreate` fails on any nonzero exit.
- **Timeouts**: a timed-out `command`/`http`/`mcp_tool` hook is canceled, its output discarded, and it renders no decision; a timed-out `PreToolUse` hook does not block the call (Agent SDK callback hooks are the exception: their PreToolUse timeout blocks).

Per-event exit-2 behavior (full table from the reference):

| Event | Can block? | Effect of exit 2 |
| :-- | :-- | :-- |
| `PreToolUse` | Yes | Blocks the tool call |
| `PermissionRequest` | No | Exit 2 not honored; deny via the `decision` object instead |
| `UserPromptSubmit` | Yes | Blocks prompt processing, erases the prompt |
| `UserPromptExpansion` | Yes | Blocks the expansion |
| `Stop` | Yes | Prevents stopping, continues the conversation |
| `SubagentStop` | Yes | Prevents the subagent from stopping |
| `TeammateIdle` | Yes | Teammate keeps working |
| `TaskCreated` | Yes | Rolls back the task creation |
| `TaskCompleted` | Yes | Task not marked completed |
| `ConfigChange` | Yes | Blocks the config change (except `policy_settings`) |
| `StopFailure` | No | Output and exit code ignored (except `terminalSequence`) |
| `PostToolUse` | No | Shows stderr to Claude; tool already ran |
| `PostToolUseFailure` | No | Shows stderr to Claude; tool already failed |
| `PostToolBatch` | Yes | Stops the agentic loop before the next model call |
| `PermissionDenied` | No | Exit code and stderr ignored; only JSON `retry` matters |
| `Notification` | No | Ignored |
| `SubagentStart` | No | Stderr shown to user only (in the subagent transcript) |
| `SessionStart` | No | Stderr shown to user only |
| `Setup` | No | Stderr shown to user only |
| `SessionEnd` | No | Stderr shown to user only |
| `CwdChanged` | No | Stderr shown to user only |
| `DirectoryAdded` | No | Stderr to debug log; add already happened |
| `FileChanged` | No | Stderr shown to user only |
| `PreCompact` | Yes | Blocks compaction |
| `PostCompact` | No | Stderr shown to user only |
| `Elicitation` | Yes | Denies the elicitation |
| `ElicitationResult` | Yes | Blocks the response (becomes decline) |
| `WorktreeCreate` | Yes | Any nonzero exit fails worktree creation |
| `WorktreeRemove` | No | Failures logged in debug mode only |
| `InstructionsLoaded` | No | Exit code ignored |
| `MessageDisplay` | No | Original text displayed |

The docs warn explicitly: "For most hook events, exit code 2 is the only exit code that blocks through the code alone. Without valid JSON on stdout, Claude Code treats exit code 1 as a non-blocking error and proceeds with the action."

### 1.4 Stdout JSON schema (common fields)

Universal fields, accepted by every event (some events discard them; each event section says so):

```json
{
  "continue": true,
  "stopReason": "shown to user when continue is false, not shown to Claude",
  "suppressOutput": false,
  "systemMessage": "warning shown to the user",
  "terminalSequence": "allowlisted OSC/BEL escape sequence emitted on your behalf"
}
```

- `continue: false` stops Claude entirely and takes precedence over event-specific decision fields.
- `suppressOutput` is documented as having no effect (accepted but not acted on).
- `terminalSequence` is restricted to OSC 0/1/2/9/99/777 and BEL; anything else causes the field to be ignored. It works even on events that discard `systemMessage` (e.g. `Notification`, `StopFailure`).
- Hook output strings (`additionalContext`, `systemMessage`, plain stdout) are capped at 10,000 characters; overflow is written to a file and replaced with a preview and path.
- `additionalContext` travels inside `hookSpecificOutput` with a required `hookEventName` and is injected as a system reminder. On resume, mid-session injected text is replayed rather than re-running the hook; `SessionStart` hooks do run again on resume (`source: "resume"` or `"fork"`).

### 1.5 Decision control per event

Summary table (from the reference's "Decision control"):

| Events | Pattern | Key fields |
| :-- | :-- | :-- |
| UserPromptSubmit, UserPromptExpansion, PostToolUse, PostToolUseFailure, PostToolBatch, Stop, SubagentStop, ConfigChange, PreCompact | Top-level `decision` | `decision: "block"`, `reason` |
| TeammateIdle, TaskCompleted | Exit code or `continue: false` | Exit 2 blocks with stderr feedback; `{"continue": false, "stopReason": "..."}` stops the teammate |
| TaskCreated | Exit code or `decision` | Exit 2 or `decision: "block"` cancels the task; `continue: false` ignored |
| PreToolUse | `hookSpecificOutput` | `permissionDecision` (allow/deny/ask/defer), `permissionDecisionReason`, `updatedInput`, `additionalContext` |
| PermissionRequest | `hookSpecificOutput` | `decision.behavior` (allow/deny), `decision.updatedInput`, `decision.updatedPermissions`, `decision.message`, `decision.interrupt` |
| PermissionDenied | `hookSpecificOutput` | `retry: true` |
| WorktreeCreate | Path return | Command hook prints path on stdout; HTTP hook returns `hookSpecificOutput.worktreePath` |
| Elicitation | `hookSpecificOutput` | `action` (accept/decline/cancel), `content` |
| ElicitationResult | `hookSpecificOutput` | `action`, `content` (override) |
| MessageDisplay | `hookSpecificOutput` | `displayContent` (display-only replacement) |
| SessionStart, Setup, SubagentStart | Context only | `additionalContext`; SessionStart also `initialUserMessage`, `sessionTitle`, `watchPaths`, `reloadSkills`. No blocking |
| WorktreeRemove, Notification, SessionEnd, PostCompact, InstructionsLoaded, StopFailure, CwdChanged, DirectoryAdded, FileChanged | None | Side effects only |

Content-rewriting fields: `PreToolUse` `updatedInput` (replaces the whole tool input before it runs), `PermissionRequest` `decision.updatedInput`, `PostToolUse` `updatedToolOutput` (replaces the result Claude sees; also `updatedMCPToolOutput` for MCP tools), and `UserPromptSubmit` can only inject `additionalContext`, not replace the prompt.

#### PreToolUse decision (exact schema)

```json
{
  "hookSpecificOutput": {
    "hookEventName": "PreToolUse",
    "permissionDecision": "allow",
    "permissionDecisionReason": "My reason here",
    "updatedInput": { "field_to_modify": "new value" },
    "additionalContext": "Current environment: production. Proceed with caution."
  }
}
```

- Multiple-hook precedence: `deny` > `defer` > `ask` > `allow`.
- Deny and ask permission rules are still evaluated regardless of what the hook returns; a hook `allow` cannot override a matching deny rule.
- A hook `"ask"` forces a prompt even in auto mode (v2.1.211+); the prompt is labeled with the hook's source (`[User]`, `[Project]`, `[Plugin]`, `[Local]`).
- `"defer"` is honored only in `-p` non-interactive mode; the process exits with `stop_reason: "tool_deferred"` and a `deferred_tool_use` payload (`id`, `name`, `input`), and the call is re-presented on `--resume`. Only works when the turn has a single tool call.
- Top-level `decision`/`reason` with `"approve"`/`"block"` values are deprecated for PreToolUse (mapped to allow/deny).
- `AskUserQuestion` and `ExitPlanMode` need `"allow"` plus `updatedInput` to be satisfied non-interactively; `"allow"` alone is not sufficient. MCP tools marked `_meta["anthropic/requiresUserInteraction"]` cannot be allowed by a hook at all (v2.1.199+).

#### PermissionRequest decision (exact schema)

Input includes `tool_name`, `tool_input` (no `tool_use_id`), and optional `permission_suggestions` (the "always allow" options). Output:

```json
{
  "hookSpecificOutput": {
    "hookEventName": "PermissionRequest",
    "decision": {
      "behavior": "allow",
      "updatedInput": { "command": "npm run lint" }
    }
  }
}
```

`decision.updatedPermissions` takes an array of permission update entries, the same shape as `permission_suggestions`: types `addRules`, `replaceRules`, `removeRules` (`rules: [{toolName, ruleContent?}]`, `behavior: allow|deny|ask`), `setMode` (`mode`), `addDirectories`/`removeDirectories` (`directories`), each with `destination`: `session` (in-memory), `localSettings` (`.claude/settings.local.json`), `projectSettings` (`.claude/settings.json`), or `userSettings` (`~/.claude/settings.json`). In sessions that cannot show a prompt, PermissionRequest hooks still run, and if none decides, the call is denied.

#### PostToolUse decision (exact schema)

Input adds `tool_response` (the tool's structured output object) and optional `duration_ms`. Output:

```json
{
  "hookSpecificOutput": {
    "hookEventName": "PostToolUse",
    "additionalContext": "Additional information for Claude",
    "updatedToolOutput": {
      "stdout": "[redacted]",
      "stderr": "",
      "interrupted": false,
      "isImage": false
    }
  }
}
```

Top-level `decision: "block"` + `reason` adds the reason next to the tool result (Claude still sees the original output). `updatedToolOutput` must match the tool's output shape for built-in tools or it is ignored; MCP tool output is passed through without schema validation.

#### UserPromptSubmit decision (exact schema)

Input adds `prompt`. Output:

```json
{
  "decision": "block",
  "reason": "Explanation for decision",
  "hookSpecificOutput": {
    "hookEventName": "UserPromptSubmit",
    "additionalContext": "My additional context here",
    "sessionTitle": "My session title"
  }
}
```

Also supports `suppressOriginalPrompt: true` (when blocking, omit the original prompt from the block message). Default timeout on this event is 30 seconds for command/http/mcp_tool hooks.

#### Stop / SubagentStop decision (exact schema)

Stop input adds `stop_hook_active`, `last_assistant_message`, `background_tasks[]` (`id`, `type`, `status`, `description`, `command?`, `agent_type?`, `server?`, `tool?`, `name?`), and `session_crons[]` (`id`, `schedule`, `recurring`, `prompt`), both arrays v2.1.145+. SubagentStop adds `agent_id`, `agent_type`, `agent_transcript_path`, `last_assistant_message`. Output:

```json
{ "decision": "block", "reason": "Must be provided when Claude is blocked from stopping" }
```

or non-error feedback that continues the conversation without a hook-error notice:

```json
{
  "hookSpecificOutput": {
    "hookEventName": "Stop",
    "additionalContext": "Please run the test suite before finishing"
  }
}
```

Loop protection: `stop_hook_active` input flag plus a hard cap of 8 consecutive blocks, after which Claude Code ends the turn anyway.

#### SessionStart decision (exact schema)

Input adds `source` (`startup|resume|clear|compact|fork`), optional `model`, `agent_type`, `session_title`. Only `command` and `mcp_tool` hook types are supported on SessionStart and Setup. Output fields: `additionalContext`, `initialUserMessage` (first user turn in `-p` mode), `sessionTitle`, `watchPaths` (absolute paths for FileChanged), `reloadSkills` (re-scan skill/command dirs after the hook). `CLAUDE_ENV_FILE` is available on SessionStart, Setup, CwdChanged, and FileChanged: `export` lines appended to it persist env vars into subsequent Bash commands.

#### Other events, condensed input fields

- `Setup`: `trigger` (`"init"` or `"maintenance"`); `additionalContext` output only.
- `InstructionsLoaded`: `file_path`, `memory_type` (`User|Project|Local|Managed`), `load_reason` (`session_start|nested_traversal|path_glob_match|include|compact`), optional `globs`, `trigger_file_path`, `parent_file_path`. Observability only; all JSON output discarded.
- `UserPromptExpansion`: `expansion_type` (`slash_command|mcp_prompt`), `command_name`, `command_args`, `command_source`, `prompt`. Covers direct `/skillname` typing, which bypasses PreToolUse on the Skill tool.
- `MessageDisplay`: `turn_id`, `message_id`, `index`, `final`, `delta`; output `displayContent` replaces the delta on screen only. Default timeout 10s.
- `PostToolUseFailure`: `tool_name`, `tool_input`, `tool_use_id`, `error` (string, format varies; Bash gives `Exit code N` first line), `is_interrupt?`, `duration_ms?`; output `additionalContext`.
- `PostToolBatch`: `tool_calls[]` of `{tool_name, tool_input, tool_use_id, tool_response}` where `tool_response` is the serialized `tool_result` content the model sees (shape differs from PostToolUse); output `additionalContext`, `decision: "block"`/`continue: false` stops the loop.
- `PermissionDenied`: `tool_name`, `tool_input`, `tool_use_id`, `reason`; output `{"hookSpecificOutput": {"hookEventName": "PermissionDenied", "retry": true}}`. Fires only in auto mode.
- `Notification`: `message`, `title?`, `notification_type`; types: `permission_prompt` (~6s no-typing gate), `idle_prompt` (~60s), `auth_success`, `elicitation_dialog`, `elicitation_url_dialog`, `elicitation_complete`, `elicitation_response`, `agent_needs_input`, `agent_completed` (last two v2.1.198+, agent view only).
- `TaskCreated` / `TaskCompleted`: `task_id`, `task_subject`, `task_description?`, `teammate_name?`, `team_name?` (deprecated).
- `TeammateIdle`: `teammate_name`, `team_name` (deprecated).
- `StopFailure`: `error` (`rate_limit|overloaded|authentication_failed|oauth_org_not_allowed|billing_error|invalid_request|model_not_found|server_error|max_output_tokens|unknown`), `error_details?`, `last_assistant_message?`. No decision control.
- `ConfigChange`: `source` (`user_settings|project_settings|local_settings|policy_settings|skills`), `file_path?`. Can block all but `policy_settings`. The blocking `reason` is accepted but never shown.
- `CwdChanged`: `old_cwd`, `new_cwd`; output `watchPaths`.
- `DirectoryAdded`: `directory`, `source` (`slash_command|register_repo_root`). Runs in the background; the add completes immediately.
- `FileChanged`: `file_path`, `event` (`change|add|unlink`); the matcher builds the watch list (split on `|`, each segment a literal filename); output `watchPaths`.
- `WorktreeCreate`: `name`; a command hook prints the worktree path as the last non-empty stdout line, an HTTP hook returns `hookSpecificOutput.worktreePath`. Symlink/`..` paths are refused (v2.1.216+).
- `WorktreeRemove`: `worktree_path`. No decision control.
- `PreCompact`: `trigger` (`manual|auto`), `custom_instructions`. Exit 2 or `decision: "block"` blocks compaction.
- `PostCompact`: `trigger`, `compact_summary`. No decision control.
- `SessionEnd`: `reason` (`clear|resume|logout|prompt_input_exit|bypass_permissions_disabled|other`). No decision control. Shared 1.5-second budget, raised to the largest configured per-hook timeout up to 60s, or `CLAUDE_CODE_SESSIONEND_HOOKS_TIMEOUT_MS`.
- `Elicitation`: `mcp_server_name`, `message`, `mode?` (`form|url`), `url?`, `elicitation_id?`, `requested_schema?`; output `action` (`accept|decline|cancel`) + `content`.
- `ElicitationResult`: `mcp_server_name`, `action`, `content?`, `mode?`, `elicitation_id?`; output overrides the user's response.

---

## 2. Hook configuration

Source: https://code.claude.com/docs/en/hooks (Configuration section) and https://code.claude.com/docs/en/settings.

### 2.1 Where hooks are defined

| Location | Scope | Shareable |
| :-- | :-- | :-- |
| `~/.claude/settings.json` | All your projects | No |
| `.claude/settings.json` | Single project | Yes (committed) |
| `.claude/settings.local.json` | Single project | No (gitignored when Claude Code writes it) |
| Managed policy settings | Organization-wide | Admin-controlled |
| Plugin `hooks/hooks.json` | While the plugin is enabled | Yes, bundled with the plugin |
| Skill frontmatter | Rest of the session once the skill is invoked (`once: true` limits to one run) | Yes |
| Subagent frontmatter | While that subagent runs (`Stop` is converted to `SubagentStop`) | Yes |

Hook entries merge across settings levels rather than replacing each other. Hooks from settings, managed settings, and plugins also run inside subagents. Duplicate identical handlers across settings files run once; a plugin's or skill's copy stays separate. Cloud (web) sessions read hooks from the repo and server-managed settings, not from local `~/.claude/settings.json`.

Enterprise `allowManagedHooksOnly: true` blocks user/project/local/plugin hooks (except plugins force-enabled via managed `enabledPlugins`), narrows `statusLine`/`fileSuggestion`/`subagentStatusLine` to managed settings, and disables command-source plugins unless `disableCommandPluginSources` is explicitly `false`. HTTP hook allowlists apply to hooks from every source: `allowedHttpHookUrls` (URL allowlist with `*` wildcards) and `httpHookAllowedEnvVars` (env var interpolation allowlist).

### 2.2 Configuration structure

Three nesting levels: event, matcher group, hook handlers.

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "if": "Bash(rm *)",
            "command": "${CLAUDE_PROJECT_DIR}/.claude/hooks/block-rm.sh",
            "args": []
          }
        ]
      }
    ]
  }
}
```

Five handler types: `command`, `http`, `mcp_tool`, `prompt`, `agent`. All matching hooks run in parallel.

Common handler fields: `type` (required), `if` (one permission rule; tool events only; a hook with `if` on other events never runs), `timeout` (seconds; defaults 600 for command/http/mcp_tool, 30 for prompt, 60 for agent; UserPromptSubmit lowers command-family default to 30, MessageDisplay to 10, SessionEnd shares a 1.5s budget), `statusMessage` (custom spinner text), `once` (skill frontmatter only).

The `if` filter is best-effort and fails open when the Bash command cannot be parsed; the docs say to use the permission system, not a hook, to enforce a hard allow or deny. Bash `if` matching checks each subcommand, strips leading `VAR=` assignments, and inspects `$()`/backticks; patterns more specific than a command name run the hook anyway on substitutions.

Command hook fields: `command` (required), `args` (switches to exec form), `async` (background), `asyncRewake` (background + wake Claude on exit 2, implies async), `shell` (`bash` or `powershell`).

- **Exec form** (`args` present): `command` resolved on PATH and spawned directly, no shell; placeholders substituted as plain strings into `command` and each `args` element. Preferred whenever a path placeholder is used.
- **Shell form** (`args` absent): the string goes to `sh -c` (macOS/Linux), Git Bash (Windows), or PowerShell when Git Bash is absent.
- Both forms export `CLAUDE_PROJECT_DIR`, `CLAUDE_PLUGIN_ROOT`, `CLAUDE_PLUGIN_DATA` to the spawned process. Plugin hooks additionally substitute `${user_config.*}` in exec form only; a shell-form plugin hook referencing `${user_config.*}` errors instead of running (read `$CLAUDE_PLUGIN_OPTION_<KEY>` instead).

HTTP hook fields: `url` (required), `headers` (env var interpolation via `$VAR`/`${VAR}` restricted to `allowedEnvVars`), `allowedEnvVars`. JSON input is POSTed with `Content-Type: application/json`; the response body uses the same JSON output schema. HTTP hooks cannot block via status codes; only a 2xx response with decision JSON blocks. Non-2xx, connection failure, and non-JSON 2xx bodies are non-blocking errors.

MCP tool hook fields: `server` (a connected MCP server; plugin-bundled servers use `plugin:<plugin-name>:<server-name>`), `tool`, `input` (supports `${path}` substitution from the hook input, e.g. `"${tool_input.file_path}"`). SessionStart/Setup usually fire before servers connect, so expect "not connected" there.

Prompt hooks (`type: "prompt"`): single LLM call (Haiku-class by default), `prompt` with `$ARGUMENTS` placeholder, optional `model`, `timeout` (default 30), `continueOnBlock`. The model must answer `{"ok": true|false, "reason": "...", "impossible": true|false}`. Per-event `ok: false` behavior is documented (e.g. Stop feeds the reason back and continues; `impossible: true` lets the turn end).

Agent hooks (`type: "agent"`): experimental; spawns a verifier subagent with Read/Grep/Glob for up to 50 turns; default timeout 60; response `{"ok": ...}`; blocks handled like a prompt hook with `continueOnBlock: true`.

Event/type support: 13 events support all five types (PreToolUse, PostToolUse, PostToolUseFailure, PostToolBatch, PermissionRequest, PermissionDenied, UserPromptSubmit, UserPromptExpansion, Stop, SubagentStop, TaskCreated, TaskCompleted, TeammateIdle). Most lifecycle/observability events support command/http/mcp_tool only. SessionStart and Setup support command and mcp_tool only.

### 2.3 Async hooks

`"async": true` (command hooks only) runs the hook in the background; Claude continues immediately. Async hooks cannot block: `decision`, `permissionDecision`, `continue` have no effect. On completion, `additionalContext` and `systemMessage` are delivered to Claude on the next conversation turn (not shown to the user). `asyncRewake` hooks that exit 2 wake Claude immediately even when idle, with stderr (or stdout) shown as a system reminder. In `-p` mode, still-running async hooks are killed at teardown with outcome `cancelled`.

### 2.4 Matcher syntax

Source: https://code.claude.com/docs/en/hooks#matcher-patterns.

| Matcher value | Evaluated as |
| :-- | :-- |
| `"*"`, `""`, or omitted | Match all occurrences of the event |
| Only letters, digits, `_`, `-`, spaces, `,`, `\|` | Exact string, or a `\|`/`,`-separated list of exact strings |
| Any other character | JavaScript regular expression, unanchored (`RegExp.prototype.test`) |

- Because regexes are unanchored, `Edit.*` matches `NotebookEdit` too; anchor with `^...$` for whole-string matches.
- Comma separators require v2.1.191+; hyphens in the exact-match set require v2.1.195+ (earlier, `code-reviewer` was a regex and also matched `senior-code-reviewer`).
- `FileChanged` and `StopFailure` use a narrower exact set (letters, digits, `_`, `|` only).
- Adding a `matcher` to an event without matcher support is silently ignored.

What each event matches on: tool events match `tool_name`; `SessionStart` matches `startup|resume|clear|compact|fork`; `Setup` matches `init|maintenance`; `SessionEnd` matches the exit reason; `Notification` matches the notification type; `SubagentStart`/`SubagentStop` match agent type; `PreCompact`/`PostCompact` match `manual|auto`; `ConfigChange` matches the config source; `DirectoryAdded` matches `slash_command|register_repo_root`; `FileChanged` matches literal filenames; `StopFailure` matches the error type; `InstructionsLoaded` matches the load reason; `UserPromptExpansion` matches the command name; `Elicitation`/`ElicitationResult` match the MCP server name. No matcher support: `UserPromptSubmit`, `PostToolBatch`, `Stop`, `TeammateIdle`, `TaskCreated`, `TaskCompleted`, `WorktreeCreate`, `WorktreeRemove`, `MessageDisplay`, `CwdChanged`.

MCP tool naming: `mcp__<server>__<tool>` (e.g. `mcp__memory__create_entities`). To match a whole server, `mcp__memory__.*`; the `.*` is required because a bare `mcp__memory` is exact-match characters only and matches no tool. Plugin-bundled servers get scoped names: `mcp__plugin_<plugin-name>_<server-name>__<tool>`, so the matcher for all tools of plugin `my-plugin` server `db` is `mcp__plugin_my-plugin_db__.*`.

### 2.5 Path placeholders and env vars

- `${CLAUDE_PROJECT_DIR}`: project root (also set in the environment of stdio MCP servers and plugin LSP servers).
- `${CLAUDE_PLUGIN_ROOT}`: the plugin's installation directory; changes on every plugin update.
- `${CLAUDE_PLUGIN_DATA}`: persistent plugin data directory surviving updates.
- `$CLAUDE_ENV_FILE`: writable env-persistence file, available in SessionStart, Setup, CwdChanged, FileChanged hooks only.
- `$CLAUDE_CODE_REMOTE` is `"true"` in remote web environments; `$CLAUDE_CODE_BRIDGE_SESSION_ID` set during Remote Control (v2.1.199+).
- On Windows PowerShell shell-form hooks, `${CLAUDE_PROJECT_DIR}`-style placeholders are rewritten to `${env:NAME}` (v2.1.198+); the bare `$CLAUDE_PROJECT_DIR` spelling resolves to `$null` and is not rewritten.

### 2.6 Managing and debugging

- `/hooks` opens a read-only browser of configured hooks with source labels (`User Settings`, `Project Settings`, `Local Settings`, `Plugin Hooks`, `Session Hooks`, `Built-in Hooks`).
- `disableAllHooks: true` disables non-managed hooks; settings precedence applies, so a project `false` overrides a user `true`; `--settings '{"disableAllHooks": true}'` wins over project/local. There is no way to disable a single hook without removing it.
- Debug: `claude --debug-file <path>` or `claude --debug` (log at `~/.claude/debug/<session-id>.txt`); `CLAUDE_CODE_DEBUG_LOG_LEVEL=verbose` adds matcher detail.
- Workspace trust: interactive sessions hold back all settings-file hooks until the trust dialog is accepted; `-p`/SDK sessions never show the dialog and treat the folder as trusted, so committed `.claude/settings.json` hooks run there. Frontmatter hooks in project subagents require trusting the exact folder (v2.1.218+); skill frontmatter hooks follow the settings-file rule.

---

## 3. Settings files and precedence

Source: https://code.claude.com/docs/en/settings.

### 3.1 File locations

- User: `~/.claude/settings.json`.
- Project shared: `.claude/settings.json` (committed).
- Project local: `.claude/settings.local.json`. Read and written at the git repository root (resolved through worktrees to the main checkout) since v2.1.211; stays in the starting directory outside a repo, when the repo root is `$HOME`, or in SDK sessions. Claude Code adds `**/.claude/settings.local.json` to the global git excludes file when it saves a setting there. Permanent "don't ask again" approvals are saved to this file.
- Managed settings, four delivery mechanisms (same JSON format):
  - Server-managed (remote at sign-in, claude.ai admin console or self-hosted gateway).
  - MDM/OS policies: macOS `com.anthropic.claudecode` managed preferences domain; Windows `HKLM\SOFTWARE\Policies\ClaudeCode` registry `Settings` value (REG_SZ/REG_EXPAND_SZ JSON); Windows user-level `HKCU\SOFTWARE\Policies\ClaudeCode` (lowest policy priority).
  - File-based `managed-settings.json` (+ `managed-mcp.json`): macOS `/Library/Application Support/ClaudeCode/`; Linux and WSL `/etc/claude-code/`; Windows `C:\Program Files\ClaudeCode\` (the legacy `C:\ProgramData\ClaudeCode\` path is unsupported since v2.1.75). A drop-in dir `managed-settings.d/*.json` merges alphabetically on top of the base file.
  - A `policyHelper` executable (configured from MDM or system managed-settings only) that emits `{"managedSettings": {...}}` and then becomes the only managed source.
- Other state (`~/.claude.json`): OAuth session, user/local MCP servers, per-project trust state, caches. Project MCP servers live in `.mcp.json`.
- Official JSON schema: `https://json.schemastore.org/claude-code-settings.json`.
- Settings files are watched; most keys (including `permissions` and `hooks`) reload live, and each change fires the `ConfigChange` hook. `model` and `outputStyle` need a restart or `/model`.

### 3.2 Precedence

Highest first:

1. Managed settings (server-managed, MDM/OS policies, managed files). Within the managed tier: remote > MDM/OS > managed files (base + drop-ins) > HKCU; the first non-empty source wins, with a short list of cross-source keys honored from any admin source.
2. Command line arguments (`--settings <file-or-json>` merges by key).
3. `.claude/settings.local.json`.
4. `.claude/settings.json`.
5. `~/.claude/settings.json`.

Array-valued settings (e.g. `permissions.allow`, `sandbox.filesystem.allowWrite`) concatenate and deduplicate across scopes instead of replacing; exceptions are `fallbackModel` (whole value from the highest scope) and a managed `availableModels` (applied as-is). A handful of security keys honor a restrictive value from any scope (e.g. `disableClaudeAiConnectors: true`). Managed settings parse tolerantly (invalid entries stripped, rest enforced, with fail-closed defaults for keys like `allowManagedHooksOnly`); user/project/local files are strict and rejected whole on validation failure.

For permission rules specifically: a deny at any level cannot be overridden by any other level, including `--allowedTools`. `/status` shows which settings sources loaded.

---

## 4. Permissions system

Source: https://code.claude.com/docs/en/permissions, plus https://code.claude.com/docs/en/permission-modes and https://code.claude.com/docs/en/settings#permission-settings.

### 4.1 Rule types and evaluation order

- **Allow**: use without approval. **Ask**: always prompt. **Deny**: block.
- Evaluation order: deny, then ask, then allow. First match wins; specificity does not change the order, so a broad deny beats a narrow allow, and a matching ask prompts even when a narrower allow also matches.
- A bare tool-name deny (`Bash`, or `Bash(*)`) removes the tool from Claude's context entirely; a scoped deny (`Bash(rm *)`) leaves the tool available and blocks matching calls. `EndConversation` cannot be removed while any other tool remains.
- Rules are enforced by Claude Code, not the model; CLAUDE.md instructions shape behavior but grant nothing.

### 4.2 Settings keys

Under `permissions` in any settings file: `allow`, `ask`, `deny` (arrays of rules), `additionalDirectories`, `defaultMode` (`default|acceptEdits|plan|auto|dontAsk|bypassPermissions|manual`-alias; `auto` only takes effect from user settings), `disableAutoMode: "disable"`, `disableBypassPermissionsMode: "disable"` (disables `--dangerously-skip-permissions`; works from any scope, typically managed), `skipDangerousModePermissionPrompt` (ignored in project settings). Example from the settings page:

```json
{
  "permissions": {
    "allow": ["Bash(npm run lint)", "Bash(npm run test *)", "Read(~/.zshrc)"],
    "deny": ["Bash(curl *)", "Read(./.env)", "Read(./.env.*)", "Read(./secrets/**)"]
  }
}
```

### 4.3 Rule pattern language per tool

Format: `Tool` or `Tool(specifier)`.

**Bash**: glob-style `*` at any position; one `*` spans spaces (`Bash(git *)` matches `git log --oneline --all`). Trailing ` *` enforces a word boundary (`Bash(ls *)` matches `ls -la`, not `lsof`; `Bash(ls*)` matches both). `:*` is an equivalent trailing-wildcard spelling (`Bash(ls:*)` == `Bash(ls *)`), recognized only at the end of a pattern. Compound commands are split on `&&`, `||`, `;`, `|`, `|&`, `&`, and newlines, and every subcommand must match independently. A built-in wrapper list is stripped before matching (`timeout`, `time`, `nice`, `nohup`, `stdbuf`, `command`, `builtin`, `noglob`, bare `xargs`), plus leading known-safe env assignments (deny/ask rules match past any leading assignment). Environment runners (`npx`, `devbox run`, `docker exec`, ...) are not stripped. Exec wrappers (`watch`, `setsid`, `ionice`, `flock`) and `find -exec/-delete` cannot be prefix-approved. A built-in, non-configurable read-only command set (`ls`, `cat`, `grep`, `cd`, read-only `git`, ...) runs without prompting in every mode. The docs explicitly warn that Bash patterns constraining arguments (e.g. URL restriction for curl) are fragile and recommend deny rules on network commands plus `WebFetch(domain:...)`, or a PreToolUse hook.

**PowerShell**: same shape as Bash; aliases are canonicalized (`PowerShell(Get-ChildItem *)` matches `gci`, `ls`, `dir`); case-insensitive; AST-based compound splitting on `|`, `;`, `&&`, `||`.

**Read and Edit**: gitignore syntax. `Edit` rules cover all built-in file-editing tools; `Read` rules cover reading tools, `@file` mentions, and IDE context, best-effort. A `Read` deny also blocks Edit/Write on the same path (v2.1.208+/v2.1.228+); NotebookEdit is not covered by Read deny, so add an `Edit` deny. Path rules for `Write`/`NotebookEdit`/`Glob`/`MultiEdit` are accepted but never consulted (startup warning; use `Edit(...)`/`Read(...)`). Four anchor shapes:

| Pattern | Meaning |
| :-- | :-- |
| `//path` | Absolute from filesystem root |
| `~/path` | From home directory |
| `/path` | Relative to the settings source (project root for project settings, `~/.claude` for user settings, original cwd for local settings/CLI/session rules) |
| `path` or `./path` | Relative to current directory |

Bare filenames match at any depth (`Read(.env)` == `Read(**/.env)`). A single-segment relative directory pattern (`src/**`) matches only `<cwd>/src` as an allow rule but a `src` directory at any depth as a deny/ask rule. Windows paths normalize to POSIX (`//c/**/.env`). Symlinks: allow rules require both the link and its target to match; deny rules fire when either matches.

**WebFetch**: `WebFetch(domain:example.com)`; `domain:*.example.com` matches subdomains at any depth but not the apex; wildcards elsewhere match only between dots (`example.*` cannot become `evil.com`); case-insensitive; `domain:*` equals bare `WebFetch`.

**MCP**: `mcp__server` (whole server), `mcp__server__tool` (one tool), `mcp__server__*` (wildcard, same as whole server). Deny/ask rules accept tool-name globs anywhere (`"*"` denies everything, `"mcp__*"` all MCP tools); allow rules accept globs only after a literal `mcp__<server>__` prefix (unanchored allow globs are skipped with a warning).

**Agent**: `Agent(AgentName)` controls which subagents may be spawned (e.g. `deny: ["Agent(Explore)"]`).

**Parameter matching** (deny/ask only): `Tool(param:value)` against a top-level scalar input parameter, exact or with `*` (`Agent(model:opus)`, `Bash(run_in_background:true)`). One parameter per rule; omitted params never match; the primary content fields (`command`, `file_path`, `path`, `url`, `notebook_path`) are excluded and such rules are ignored with a startup warning.

**Cd**: `Cd(<path-pattern>)` rules govern the user-invoked `/cd` command only (not model-invocable); any Cd allow rule switches `/cd` to allowlist mode.

### 4.4 Modes, directories, trust

Modes: `default` (Manual), `acceptEdits`, `plan`, `auto` (classifier-reviewed auto-approval), `dontAsk` (auto-deny unless pre-approved), `bypassPermissions` (skips prompts except explicit ask rules, org-forced connector asks, `requiresUserInteraction` MCP tools, and the root/home `rm -rf` circuit breaker). `additionalDirectories` extends file access but does not load most `.claude/` configuration from those directories (skills/commands/agents and two settings keys load from `--add-dir` directories only, not from settings-file entries).

Workspace trust: `permissions.allow` rules and `additionalDirectories` in a project's `.claude/settings.json` apply only after the trust dialog is accepted; deny and ask rules apply regardless. `-p`/SDK sessions never show the dialog: allow rules are skipped (stderr warning) but hooks, `env` blocks, and helper commands from project settings still run. The permissions page documents the mitigation flags for scripting over untrusted repos: `--setting-sources user`, `--bare`, `--settings '{"disableAllHooks": true}'`, `disabledMcpjsonServers`.

### 4.5 Permission rules vs hooks

Documented interaction (https://code.claude.com/docs/en/permissions#extend-permissions-with-hooks):

- PreToolUse hooks run before the permission prompt for every tool except `EndConversation`; they can deny, force a prompt, or skip it.
- Hook decisions do not bypass permission rules: deny and ask rules are evaluated regardless of hook output, preserving deny-first precedence including managed denies.
- A blocking hook takes precedence over allow rules: exit 2 stops the call before rules are evaluated. The documented pattern for "allow everything except X" is a broad allow rule plus a PreToolUse hook that rejects X.
- The hooks reference states the `if` filter is best-effort and fails open on unparseable commands: "use the permission system rather than a hook to enforce a hard allow or deny".
- What only hooks can do: rewrite tool input (`updatedInput`), rewrite tool output (`updatedToolOutput`), inject context, auto-answer permission prompts with `updatedPermissions`, gate on runtime state (file contents, env, external services), act on non-tool lifecycle events. What only rules can do: remove a tool from Claude's context entirely (bare-name deny), guarantee enforcement independent of a subprocess starting successfully, and express path/domain/prefix constraints declaratively.
- On latency: the docs do not state an explicit "in-process vs subprocess" latency comparison. The closest documented statements are that the `if` field exists to avoid "the process spawn overhead" of running the hook script when the condition does not match, and repeated guidance to keep frequently-firing hooks (SessionStart, UserPromptSubmit, MessageDisplay) fast because they block processing.

---

## 5. Plugin system

Sources: https://code.claude.com/docs/en/plugins-reference, https://code.claude.com/docs/en/plugin-marketplaces, https://code.claude.com/docs/en/plugins, https://code.claude.com/docs/en/plugin-dependencies, https://code.claude.com/docs/en/settings#plugin-configuration.

### 5.1 Plugin directory structure

Standard layout (from the plugins reference):

```text
enterprise-plugin/
├── .claude-plugin/           # Metadata directory (optional)
│   └── plugin.json           # plugin manifest
├── skills/                   # Skills (<name>/SKILL.md)
├── commands/                 # Skills as flat .md files (legacy; use skills/)
├── agents/                   # Subagent definitions
├── workflows/                # Workflow scripts
├── output-styles/
├── themes/                   # Experimental
├── monitors/                 # monitors.json (experimental background monitors)
├── hooks/
│   ├── hooks.json            # Main hook config
│   └── security-hooks.json   # Additional hooks
├── bin/                      # Plugin executables added to PATH
│   └── my-tool               # Invokable as bare command in Bash tool
├── settings.json             # Default settings (only `agent` and `subagentStatusLine` keys)
├── .mcp.json                 # MCP server definitions
├── .lsp.json                 # LSP server configurations
└── scripts/                  # Hook and utility scripts
```

Only `.claude-plugin/` holds the manifest; all component directories sit at the plugin root. A root `CLAUDE.md` is NOT loaded as context. A single `SKILL.md` at the plugin root works as a one-skill plugin.

### 5.2 plugin.json manifest

The manifest is optional (components auto-discovered, name from directory). If present, `name` is the only required field. Complete schema fields (from the reference): `name`, `displayName`, `version`, `description`, `author` (`name`/`email`/`url`), `homepage`, `repository`, `license`, `keywords`, `metadata` (free-form), `defaultEnabled` (v2.1.154+), component paths `skills` (adds to default `skills/` scan), `commands`/`agents`/`workflows`/`outputStyles` (replace defaults), `hooks`/`mcpServers`/`lspServers` (path, array, or inline object), `experimental.themes`, `experimental.monitors`, `userConfig` (enable-time prompts; values substituted as `${user_config.KEY}` and exported as `CLAUDE_PLUGIN_OPTION_<KEY>`), `channels`, `dependencies` (other plugins, semver constraints). Unrecognized top-level fields are ignored (warnings from `claude plugin validate`; `--strict` promotes them to errors). All paths must be plugin-root-relative starting with `./`.

Plugin hooks: `hooks/hooks.json` uses the exact same `{"hooks": {...}}` schema as settings hooks plus an optional top-level `description`. When the plugin is enabled, its hooks merge with user and project hooks. All hook events are available to plugins. Plugin agents may not use `hooks`, `mcpServers`, or `permissionMode` frontmatter (ignored for security).

### 5.3 Marketplace structure

`.claude-plugin/marketplace.json` at the marketplace repo root:

```json
{
  "name": "company-tools",
  "owner": { "name": "DevTools Team", "email": "devtools@example.com" },
  "plugins": [
    { "name": "code-formatter", "source": "./plugins/formatter", "version": "2.1.0" },
    { "name": "deployment-tools", "source": { "source": "github", "repo": "company/deploy-plugin" } }
  ]
}
```

Required: `name`, `owner.name`, `plugins[]` (each with `name` + `source`). Optional: `metadata.pluginRoot`, `renames`, `allowCrossMarketplaceDependenciesOn`, per-entry metadata plus `category`, `tags`, `strict`, `relevance`, `defaultEnabled`, and component overrides. A list of marketplace names is reserved for Anthropic.

Plugin source types (plugin entries inside marketplace.json):

| Source | Fields | Notes |
| :-- | :-- | :-- |
| Relative path string | `"./my-plugin"` | Inside the marketplace repo; resolved against the marketplace root |
| `github` | `repo`, `ref?`, `sha?` | `sha` is a full 40-char commit pin; wins over `ref` |
| `url` | `url`, `ref?`, `sha?` | Any git URL |
| `git-subdir` | `url`, `path`, `ref?`, `sha?` | Sparse partial clone of a monorepo subdirectory |
| `npm` | `package`, `version?`, `registry?` | Installed via `npm install` (with lifecycle scripts for the package itself) |
| `archive` | `url` (HTTPS only), `sha256?` | Zip download; v2.1.224+; 256 MiB cap; digest verified when pinned |
| `command` | `command`, `timeout?`, `mode?` | A local command prints the plugin directory path; v2.1.229+; user must accept the exact command string; `copy` or `link` mode |

Marketplace sources (how the catalog itself is registered, via `/plugin marketplace add` or `extraKnownMarketplaces` in settings): `github`, `git`, `url` (direct marketplace.json URL, optional `headers`), `file`, `directory`, `hostPattern`, and inline `settings`. Git marketplace sources support `ref` but not `sha`. `extraKnownMarketplaces` in a repo's `.claude/settings.json` takes effect only after the workspace trust dialog.

### 5.4 Install, enable, scopes, updates

- Install writes to `enabledPlugins` (`"plugin@marketplace": true|false`) at a scope: `user` (`~/.claude/settings.json`, default), `project` (`.claude/settings.json`), `local` (`.claude/settings.local.json`), or `managed` (read-only). Project settings beat user settings for enablement; opt out locally via `settings.local.json`. Managed `enabledPlugins` can force-enable and cannot be overridden.
- CLI: `claude plugin install|uninstall|enable|disable|update|list|details|validate|init|prune|tag`, with `-s/--scope`, `--config key=value` (userConfig), `--yes`, `--keep-data`.
- Version pinning/updates: the plugin version is the cache key. Resolution order: `plugin.json` `version` > marketplace entry `version` > git commit SHA (git-based and relative-path sources) > archive SHA-256 digest > `unknown` (npm without version, non-git local dirs). Explicit version = updates only when you bump it; omitted version = updates on every new commit/digest. `command` sources always version by content hash. Git plugin sources additionally support exact `sha` pins in the marketplace entry, and marketplaces can define release channels and per-plugin dependency version constraints (semver, https://code.claude.com/docs/en/plugin-dependencies).
- Caching: marketplace plugins are copied into `~/.claude/plugins/cache`, one directory per installed version; previous versions are swept ~14 days after orphaning. Copied plugins cannot reference files outside their directory (`../` breaks after install); symlinks are preserved within the plugin, dereferenced within the marketplace, skipped outside it.
- Dev/testing: `claude --plugin-dir ./my-plugin` (also accepts a `.zip`), `claude --plugin-url https://.../plugin.zip`, and skills-directory plugins (`~/.claude/skills/<name>/.claude-plugin/plugin.json` loads in place as `<name>@skills-dir` with no install).

### 5.5 Can a plugin ship or bootstrap a platform-specific compiled binary (e.g. Go)?

What the docs actually say, mechanism by mechanism:

1. **Shipping executables directly is a documented layout.** The plugin directory structure documents `bin/`: "Plugin executables added to PATH. Files here are invokable as bare commands in any Bash tool call while the plugin is enabled" (https://code.claude.com/docs/en/plugins-reference#plugin-directory-structure and the file locations table). `${CLAUDE_PLUGIN_ROOT}` is documented as the way to reference "Scripts, binaries, and config files bundled with the plugin", and MCP server examples invoke a bundled binary directly (`"command": "${CLAUDE_PLUGIN_ROOT}/servers/db-server"`). So a plugin can ship one or more compiled binaries and run them from hooks, MCP configs, or the Bash PATH.
2. **No install-time script hook exists.** There is no documented postinstall mechanism. The only code that runs at install time is: (a) for `npm` plugin sources, `npm install` of the package itself "with lifecycle scripts enabled"; (b) the automatic Node.js dependency install inside the cached copy, which explicitly runs `npm ci --ignore-scripts` / `bun install --frozen-lockfile --ignore-scripts` with frozen resolution and a 60-second timeout, precisely so "no code from the plugin or its packages executes during it" (https://code.claude.com/docs/en/plugins-reference#node-js-package-dependencies). Native modules that compile in lifecycle scripts therefore do not build during this install; the docs direct such cases to a hook that installs into the persistent data directory.
3. **Documented bootstrap pattern: install on first use from a hook.** The Setup event section states: "Because Setup doesn't fire on every launch, a plugin that needs a dependency installed can't rely on Setup alone. The practical pattern is to check for the dependency on first use and install on miss, for example a hook or skill that tests for `${CLAUDE_PLUGIN_DATA}/node_modules` and runs `npm install` if absent" (https://code.claude.com/docs/en/hooks#setup). The plugins reference documents a concrete SessionStart hook that diffs a manifest copy in `${CLAUDE_PLUGIN_DATA}` and reinstalls when it changed (https://code.claude.com/docs/en/plugins-reference#persistent-data-directory). Nothing prevents that same pattern from downloading a platform-specific binary instead of running `npm install`; the docs simply never show a binary-download example.
4. **Persistent storage for downloaded artifacts exists.** `${CLAUDE_PLUGIN_DATA}` resolves to `~/.claude/plugins/data/{id}/`, survives updates (unlike `${CLAUDE_PLUGIN_ROOT}`, which changes each update), is created on first reference, and is deleted on last-scope uninstall unless `--keep-data`.
5. **Precedent for external binaries: LSP plugins.** The docs are explicit that LSP plugins do not bundle their language server: "You must install the language server binary separately" (https://code.claude.com/docs/en/plugins-reference#lsp-servers). This is the documented stance for large platform-specific binaries in the official marketplace: configure the connection, require the user to install the binary.
6. **Distribution channels that could carry binaries**: git sources carry whatever is committed; `archive` (zip over HTTPS, sha256-pinnable, 256 MiB cap) and `npm` sources also work; `command` sources let a locally installed tool produce the plugin directory per platform (explicitly motivated by "an IDE that renders its plugin for the currently selected toolchain"), which is the closest documented mechanism to platform-specific packaging.
7. **Multi-platform selection**: not documented. There is no documented per-OS/per-arch source selection, no `os`/`cpu` style manifest field, and no documented pattern for shipping several binaries and picking one at runtime. A plugin would have to do its own platform dispatch in a wrapper script or a `command` source.
8. **Executable permission bits**: not documented. The docs never state whether the execute bit survives the copy into the plugin cache, zip extraction for `archive` sources, or npm packing. The troubleshooting sections tell users to `chmod +x` hook scripts, which addresses authored files, not installed plugin artifacts.

Bottom line per the docs: shipping a compiled binary inside a plugin is supported (dedicated `bin/` directory, `${CLAUDE_PLUGIN_ROOT}` references), and bootstrapping via a SessionStart/first-use hook into `${CLAUDE_PLUGIN_DATA}` is the documented install-on-miss pattern; but there is no postinstall hook, no platform-selection mechanism, and no statement about permission-bit preservation. See Gaps.

---

## 6. Subagents and skills (adjacent extension surface)

Sources: https://code.claude.com/docs/en/sub-agents, https://code.claude.com/docs/en/skills.

- Subagent definition locations, priority high to low: managed settings, `--agents` CLI JSON, `.claude/agents/` (project, walked up to the repo root), `~/.claude/agents/`, plugin `agents/`. Frontmatter fields: `name`, `description` (both required), `tools`, `disallowedTools`, `model`, `permissionMode`, `maxTurns`, `skills`, `mcpServers`, `hooks`, `memory`, `background`, `effort`, `isolation` (`worktree`), `color`, `initialPrompt`. Plugin agents ignore `hooks`, `mcpServers`, `permissionMode`.
- Subagent frontmatter hooks run only while that agent is active; frontmatter `Stop` converts to `SubagentStop`. Project-level agent frontmatter hooks require exact-folder trust (v2.1.218+).
- `SubagentStart`/`SubagentStop` matchers use the agent's frontmatter `name`, or the plugin-scoped `plugin:agent` id (colon puts it on the regex path; anchor with `^...$`).
- The old slash-commands page now serves "Extend Claude with skills": custom slash commands are skills (`.claude/skills/<name>/SKILL.md`, `~/.claude/skills/`, enterprise via managed settings, plugin `skills/`; legacy `.claude/commands/*.md` still works, skill wins on name conflicts). Skill frontmatter relevant to guardrails: `disable-model-invocation`, `allowed-tools` (turn-scoped pre-approval that still flows through the permission system), `disallowed-tools`, `model`, `context: fork` + `agent` + `background`, and `hooks` (session-persistent once invoked; `once: true` for single-run).

---

## 7. Recent additions as of mid-2026

Things the current docs flag as new or version-gate to 2026 releases:

- New hook events beyond the classic set: `Setup`, `InstructionsLoaded`, `UserPromptExpansion`, `MessageDisplay`, `PermissionRequest`, `PermissionDenied`, `PostToolUseFailure`, `PostToolBatch`, `TaskCreated`, `TaskCompleted`, `TeammateIdle` (agent teams), `StopFailure`, `ConfigChange`, `CwdChanged`, `DirectoryAdded`, `FileChanged`, `WorktreeCreate`/`WorktreeRemove`, `PostCompact`, `Elicitation`/`ElicitationResult` (https://code.claude.com/docs/en/hooks).
- New hook handler types: `http`, `mcp_tool`, `prompt`, and experimental `agent` hooks, alongside `command`.
- Async hooks (`async`, `asyncRewake`), exec form (`args`), per-handler `if` permission-rule filter, `statusMessage`, `once`, `shell: powershell`.
- `Stop`/`SubagentStop` `background_tasks` and `session_crons` inputs (v2.1.145+); `prompt_id` (v2.1.196+); comma matchers (v2.1.191+); hyphen exact-match (v2.1.195+); `/goal` as a built-in session-scoped prompt Stop hook.
- Plugins: `archive` zip sources (v2.1.224+), `command` sources with copy/link mode (v2.1.229+), automatic lockfile-driven Node dependency installs with `--ignore-scripts`, persistent data directory `${CLAUDE_PLUGIN_DATA}`, `bin/` PATH executables, LSP servers, background monitors, themes, channels, `userConfig`, plugin dependency semver constraints and release channels, skills-directory plugins, `--plugin-url`.
- Permissions: parameter matching `Tool(param:value)` in deny/ask rules, tool-name globs, `Cd` rules, auto mode as a permission mode plus `PermissionDenied` retry, `dontAsk` mode, read-deny covering writes (v2.1.228).
- Weekly changelog pages exist under https://code.claude.com/docs/en/whats-new/ (latest at research time: week 32, Aug 3-7 2026: cross-session messaging, self-hosted cloud sessions, auto mode default).
- `claude plugin eval` and `/skill-doctor` are referenced in tooling but have no page on code.claude.com (probed `plugin-evals.md` and `plugin-eval.md`: 404, and llms.txt lists no eval page), so they are undocumented on the primary source as of 2026-08-15.

---

## 8. Gaps / unknowns

Things the primary docs do not answer:

1. **Executable permission bits.** No statement anywhere on whether the execute bit is preserved when a plugin is copied into `~/.claude/plugins/cache`, extracted from an `archive` zip, or installed from npm. Hookify would need to verify empirically or `chmod +x` defensively from a SessionStart hook.
2. **Platform-specific binary selection.** No `os`/`arch` manifest fields, no per-platform sources, no documented pattern for shipping multiple binaries and dispatching. The only platform-aware mechanism documented is a `command` source, where a locally installed tool renders the plugin per toolchain.
3. **Binary download on first run.** The install-on-miss pattern is documented only for `npm install` into `${CLAUDE_PLUGIN_DATA}`; downloading a compiled binary is never shown, and no integrity/verification guidance exists for hook-downloaded artifacts (the `sha256` pin applies to `archive` plugin sources, not to files a hook fetches).
4. **Whether `bin/` entries need a shebang or specific format**, and whether `bin/` works on Windows (no statement).
5. **Hook execution latency.** No quantitative or explicit qualitative comparison between in-process permission-rule evaluation and hook subprocess spawn. Only indirect signals: the `if` field exists to avoid "process spawn overhead", and per-event guidance to keep blocking hooks fast.
6. **`suppressOutput`** is documented as a no-op, so there is no supported way to suppress a hook's stdout from the debug log.
7. **Per-hook disable.** Documented as impossible: "There is no way to disable an individual hook while keeping it in the configuration."
8. **`claude plugin eval` / `/skill-doctor`.** No page on code.claude.com; status and behavior cannot be cited from primary docs.
9. **Plugin cache location override.** No documented setting to relocate `~/.claude/plugins/cache` or `~/.claude/plugins/data` (other than the general `CLAUDE_CONFIG_DIR` mention in the permissions page, whose effect on the plugin cache is not spelled out).
