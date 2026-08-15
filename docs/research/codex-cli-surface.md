# OpenAI Codex CLI: rule-enforcement surface for hookify

Date: 2026-08-15

Research question (hookify issue #3): What does OpenAI Codex CLI offer that hookify, a Claude Code plugin that enforces rules via lifecycle hooks (PreToolUse/PostToolUse, blocking or warning on tool calls based on user-defined rules), could enforce rules through?

Sources are primary only: the official docs site (developers.openai.com/codex, which serves its markdown from learn.chatgpt.com/docs, confirmed by the 308 redirect on `https://developers.openai.com/codex/llms.txt`), the [openai/codex](https://github.com/openai/codex) repo at commit `12933b6` (main, 2026-08-15), its releases, and its issue tracker.

## 1. Hooks and lifecycle events: they exist, and they are Claude-shaped

The headline finding: Codex shipped a full lifecycle hooks system in March 2026 and it is now stable and enabled by default.

- The original proposal, [issue #2109 "Event Hooks"](https://github.com/openai/codex/issues/2109) (690 reactions), was closed as completed on 2026-03-27. Maintainer etraut-openai: "Event hooks are now available as an experimental feature. Refer to [this documentation](https://developers.openai.com/codex/hooks) for details." An earlier comment (2026-02-03) confirmed hooks are integrated "into the core agent loop so all Codex surfaces will benefit."
- The feature flag is `[features] hooks`, registered as `Stage::Stable, default_enabled: true` in [codex-rs/features/src/lib.rs](https://github.com/openai/codex/blob/12933b69551394328319dcdd1bcee7907326dc85/codex-rs/features/src/lib.rs) (the enum variant is literally documented as "Enable Claude-style lifecycle hooks loaded from hooks.json files").
- The engine struct in the hooks crate is named `ClaudeHooksEngine` ([codex-rs/hooks/src/events/pre_tool_use.rs](https://github.com/openai/codex/blob/12933b69551394328319dcdd1bcee7907326dc85/codex-rs/hooks/src/events/pre_tool_use.rs)): the hook file format is deliberately Claude Code compatible.

### Events

Per the [hooks reference](https://developers.openai.com/codex/hooks) (served as [hooks.md](https://learn.chatgpt.com/docs/hooks.md)) and the [config reference](https://developers.openai.com/codex/config-reference): `PreToolUse`, `PermissionRequest`, `PostToolUse`, `PreCompact`, `PostCompact`, `UserPromptSubmit`, `SubagentStop`, `Stop`, `SessionStart`, `SubagentStart`, `SessionEnd`. Source files match one to one in [codex-rs/hooks/src/events/](https://github.com/openai/codex/tree/12933b69551394328319dcdd1bcee7907326dc85/codex-rs/hooks/src/events).

### Configuration and format

Per the [hooks reference](https://developers.openai.com/codex/hooks):

- User level: `~/.codex/hooks.json` or inline `[hooks]` tables in `~/.codex/config.toml`.
- Project level: `<repo>/.codex/hooks.json` or `<repo>/.codex/config.toml`.
- Plugin manifests bundling `hooks/hooks.json`, and managed `requirements.toml` for enterprise.
- JSON shape is the Claude Code shape: event name, matcher groups with regex `matcher`, handler objects `{ "type": "command", "command": ..., "timeout": ..., "async": ... }`.

### Payload and decision semantics

Hooks receive JSON on stdin (`session_id`, `transcript_path`, `cwd`, `hook_event_name`, `model`, `permission_mode`, plus `turn_id`, `tool_name`, `tool_input`, `tool_use_id` for tool events) and respond via exit codes and stdout JSON ([hooks reference](https://developers.openai.com/codex/hooks)):

- Exit code `2` blocks or denies, with stderr as the reason.
- JSON fields: `continue: false`, `stopReason`, `systemMessage`, `decision` (`"allow"`, `"deny"`, `"block"`) on `PreToolUse`, `PermissionRequest`, `PostToolUse`, `SubagentStop`, `Stop`, plus `permissionDecision`, `updatedInput` (PreToolUse rewrites tool arguments), and `additionalContext`.
- Source confirms the semantics: `PreToolUseOutcome` carries `should_block`, `block_reason`, `additional_contexts`, `updated_input`, and competing rewrites are resolved by completion order ([pre_tool_use.rs](https://github.com/openai/codex/blob/12933b69551394328319dcdd1bcee7907326dc85/codex-rs/hooks/src/events/pre_tool_use.rs)). `StopOutcome` carries `should_block`, `block_reason`, and `continuation_fragments`, so a Stop hook can force the agent to continue with injected prompt fragments ([stop.rs](https://github.com/openai/codex/blob/12933b69551394328319dcdd1bcee7907326dc85/codex-rs/hooks/src/events/stop.rs)).
- Default timeout 600 seconds per handler; `SessionEnd` is capped at 3 seconds.

### Trust model

Non-managed hooks require explicit trust review before execution (`/hooks` command); trust is recorded by hash and re-required on change; `--dangerously-bypass-hook-trust` exists for automation ([hooks reference](https://developers.openai.com/codex/hooks)). Admins can set `allow_managed_hooks_only = true` in `requirements.toml` to ignore user, project, and session hook configs ([docs/config.md](https://github.com/openai/codex/blob/12933b69551394328319dcdd1bcee7907326dc85/docs/config.md)).

### Open proposals and known gaps (issue tracker, August 2026)

- [#21753 "Full Claude Code Hook Parity (29+)"](https://github.com/openai/codex/issues/21753) (open, since 2026-05-08): umbrella tracker; states the goal that "every high-risk action has a blocking interception point" and tracks per-event parity.
- [#38135](https://github.com/openai/codex/issues/38135) (open): PostToolUse cannot yet replace tool output (`updatedToolOutput` requested).
- [#34289](https://github.com/openai/codex/issues/34289) (open): PostToolUse payload carries no failure signal; no PostToolUseFailure event.
- [#33630](https://github.com/openai/codex/issues/33630) (open): UserPromptSubmit cannot fail closed before model requests.
- [#35306](https://github.com/openai/codex/issues/35306) (open): project-level hooks are silently skipped when the project trust prompt never fires.
- [#26383](https://github.com/openai/codex/issues/26383) (open): `codex exec` does not dispatch repo hooks from `.codex/hooks.json`.
- [#34694](https://github.com/openai/codex/issues/34694) (open): async command hooks skipped for Claude-format plugin hooks.
- [#17148](https://github.com/openai/codex/issues/17148) (open): Pre/PostCompact hooks tracking (events now exist per the config reference; issue remains open).
- Recent releases keep landing hook fixes: [rust-v0.145.0](https://github.com/openai/codex/releases/tag/rust-v0.145.0) added SessionEnd hooks, permission-hook resolution of auto-review requests, and configurable context spill limits; [rust-v0.146.0](https://github.com/openai/codex/releases/tag/rust-v0.146.0) and [rust-v0.147.0](https://github.com/openai/codex/releases/tag/rust-v0.147.0) contain further hook fixes.

## 2. The config.toml model

Per the [config reference](https://developers.openai.com/codex/config-reference) (repo [docs/config.md](https://github.com/openai/codex/blob/12933b69551394328319dcdd1bcee7907326dc85/docs/config.md) is now a stub pointing at developers.openai.com/codex/config-basic, config-advanced, and config-reference):

- User level: `~/.codex/config.toml` (more precisely `$CODEX_HOME/config.toml`).
- Project level: `.codex/config.toml`, loaded only when the project is trusted, and it cannot override machine-local provider, auth, host-owned app request metadata, notification, configuration profile selection, or telemetry routing keys.
- Keys relevant here: `model`, `model_provider`, `approval_policy`, `sandbox_mode`, `sandbox_workspace_write.*`, `notify`, `mcp_servers.<id>` (with `command`/`args`/`env` or `url`, `enabled`, `required`, `enabled_tools`, `disabled_tools`, `default_tools_approval_mode` of `auto | prompt | writes | approve`), `hooks.<Event>`, `[features]` toggles (including `hooks`), `profiles`, `log_dir`, `project_doc_max_bytes`, `project_doc_fallback_filenames`.

## 3. Approvals, sandbox, permissions

- `approval_policy` values in the [config reference](https://developers.openai.com/codex/config-reference): `untrusted`, `on-request`, `never`, plus a `granular` table (`sandbox_approval`, `rules`, `mcp_elicitations`, `request_permissions`, `skill_approval`). In source, `AskForApproval` is `UnlessTrusted` (serialized `untrusted`), `OnRequest` (with serde alias `on-failure`, so the old on-failure value now maps to on-request), `Granular`, `Never` ([codex-rs/protocol/src/protocol.rs](https://github.com/openai/codex/blob/12933b69551394328319dcdd1bcee7907326dc85/codex-rs/protocol/src/protocol.rs)).
- `sandbox_mode` values: `read-only`, `workspace-write` (default), `danger-full-access`, enforced with platform-native mechanisms (Seatbelt on macOS, bubblewrap on Linux/WSL2) per the [security docs](https://developers.openai.com/codex/security) (served as [sandboxing.md](https://learn.chatgpt.com/docs/sandboxing.md)). "The sandbox defines technical boundaries. The approval policy decides when the agent must stop and ask before crossing them."
- External programs can now be consulted for approval decisions, in two sanctioned ways:
  1. The `PermissionRequest` hook runs in the approval path before the approval UI and can return a concrete allow or deny; decisions fold conservatively, any deny wins, otherwise the last allow wins, otherwise normal approval flow continues ([permission_request.rs](https://github.com/openai/codex/blob/12933b69551394328319dcdd1bcee7907326dc85/codex-rs/hooks/src/events/permission_request.rs)).
  2. Execpolicy rules files (section 8) decide allow/prompt/forbidden per command prefix.
- There is also an `approvals_reviewer` setting routing approvals to an automatic reviewer agent instead of the user ([sandboxing docs](https://learn.chatgpt.com/docs/sandboxing.md)).

## 4. The notify hook: legacy and fire-and-forget

- `notify` in config.toml is a command invoked for notifications, receiving a JSON payload ([config reference](https://developers.openai.com/codex/config-reference)).
- It is now implemented inside the hooks crate as a legacy shim: [codex-rs/hooks/src/legacy_notify.rs](https://github.com/openai/codex/blob/12933b69551394328319dcdd1bcee7907326dc85/codex-rs/hooks/src/legacy_notify.rs) serializes a `UserNotification::AgentTurnComplete` payload (`type: "agent-turn-complete"`, with `thread_id`, `turn_id`, `cwd`, `input_messages`, `last_assistant_message`) as the final argv argument.
- It is strictly fire-and-forget: the child process is spawned with stdin, stdout, and stderr set to null and the hook returns `HookResult::Success` without waiting. It cannot block or modify anything. For enforcement it is superseded by real hooks.

## 5. MCP support

- Codex as MCP client: `mcp_servers.<id>` tables in config.toml support stdio and HTTP servers, per-server `enabled_tools`/`disabled_tools` allowlists, and `default_tools_approval_mode` (`auto | prompt | writes | approve`) ([config reference](https://developers.openai.com/codex/config-reference); [MCP docs](https://learn.chatgpt.com/docs/extend/mcp.md)). MCP tool calls flow through the same hook events: PreToolUse stdin docs note that "MCP tools pass their resolved JSON arguments" as `tool_input` ([pre_tool_use.rs](https://github.com/openai/codex/blob/12933b69551394328319dcdd1bcee7907326dc85/codex-rs/hooks/src/events/pre_tool_use.rs)).
- Codex as MCP server: `codex mcp-server` exposes `codex` (start a session, accepts `approval-policy` and `sandbox` overrides) and `codex-reply` (continue by `threadId`) tools ([MCP server docs](https://learn.chatgpt.com/docs/mcp-server.md)).
- As an enforcement vector, MCP is weak: an MCP server can only govern its own tools, and it cannot intercept Codex's native shell or edit tools. Hooks make MCP-based interception unnecessary.

## 6. AGENTS.md: advisory only

Per the [AGENTS.md guide](https://developers.openai.com/codex/guides/agents-md) (served as [agents-md.md](https://learn.chatgpt.com/docs/agent-configuration/agents-md.md)):

- Global: `~/.codex/AGENTS.override.md` is checked first, then `~/.codex/AGENTS.md`; only the first non-empty file at this level is used.
- Project: from the Git root (or cwd) Codex walks toward the working directory, at each level checking `AGENTS.override.md`, then `AGENTS.md`, then configured fallback names (`project_doc_fallback_filenames`).
- Merge: files are concatenated root-down, joined with blank lines; files closer to the current directory override earlier guidance; default cap 32 KiB (`project_doc_max_bytes`).
- Nature: advisory prompt guidance to the model, not enforcement. Useful for hookify only as the soft layer that explains rules the hooks enforce.

## 7. Transcript and session accessibility

- Rollouts persist under `$CODEX_HOME/sessions` (constant `SESSIONS_SUBDIR: "sessions"`, plus `ARCHIVED_SESSIONS_SUBDIR: "archived_sessions"`) in the [codex-rs/rollout crate](https://github.com/openai/codex/blob/12933b69551394328319dcdd1bcee7907326dc85/codex-rs/rollout/src/lib.rs), with `$CODEX_HOME` defaulting to `~/.codex`.
- Format is JSONL: the crate ships a `reverse_jsonl_scanner`, and rollout filenames encode timestamp, thread ID, and rollout ID ([rollout_file_name.rs](https://github.com/openai/codex/blob/12933b69551394328319dcdd1bcee7907326dc85/codex-rs/rollout/src/rollout_file_name.rs)).
- Hooks receive `transcript_path` directly on stdin ([hooks reference](https://developers.openai.com/codex/hooks)), so post-hoc conversation analysis (hookify's conversation-analyzer style) is fully possible. `codex exec --ephemeral` opts out of persisting rollouts, and `codex exec resume --last` resumes from them ([non-interactive docs](https://developers.openai.com/codex/noninteractive)).

## 8. Other relevant surfaces

- Execpolicy rules: user-extensible Starlark `.rules` files govern which commands run outside the sandbox without prompting. Locations: `~/.codex/rules/default.rules` (user), `<repo>/.codex/rules/` (project, requires trust), and enterprise layers. `prefix_rule(pattern=..., decision=...)` yields `allow`, `prompt`, or `forbidden`; interactive approvals are persisted back into the user rules file; `codex execpolicy check --pretty --rules ~/.codex/rules/default.rules -- <command>` validates ([exec-policy docs](https://developers.openai.com/codex/exec-policy), repo stub [docs/execpolicy.md](https://github.com/openai/codex/blob/12933b69551394328319dcdd1bcee7907326dc85/docs/execpolicy.md); the [execpolicy crate](https://github.com/openai/codex/tree/12933b69551394328319dcdd1bcee7907326dc85/codex-rs/execpolicy) implements it).
- `codex exec` non-interactive mode: `--json` JSONL event stream, `-o/--output-last-message`, `--output-schema`, `--sandbox`, `--ephemeral`, `--ignore-user-config`, `--ignore-rules` (note: this flag lets a run bypass execpolicy rules, which matters for enforcement guarantees), `CODEX_API_KEY` for CI ([non-interactive docs](https://developers.openai.com/codex/noninteractive)).
- Feature flags in `[features]` include `hooks` (stable, default on), `code_mode`, `multi_agent`, `network_proxy`, and others ([config reference](https://developers.openai.com/codex/config-reference); [features crate](https://github.com/openai/codex/blob/12933b69551394328319dcdd1bcee7907326dc85/codex-rs/features/src/lib.rs)).
- A plugin system exists (plugin manifests can bundle `hooks/hooks.json` per the [hooks reference](https://developers.openai.com/codex/hooks); see also closed [issue #8512 "Implement Codex Plugins same as Claude Plugins"](https://github.com/openai/codex/issues/8512)), and an [external-agent-migration crate](https://github.com/openai/codex/tree/12933b69551394328319dcdd1bcee7907326dc85/codex-rs/external-agent-migration) detects and migrates Claude-format hook configurations.

## How hookify rules could be enforced on Codex

Ranked by fidelity to what hookify does on Claude Code:

1. Native lifecycle hooks (`~/.codex/hooks.json` or `[hooks]` in config.toml, plus `<repo>/.codex/hooks.json`). This is a near-direct port: PreToolUse and PostToolUse exist, matchers are regex on tool names, payload arrives as JSON on stdin, and blocking works via exit code 2 or `{"decision": "block", "reason": ...}`. PreToolUse can even rewrite tool input via `updatedInput`. The engine is Claude-format compatible by design (`ClaudeHooksEngine`), so hookify's generated hook commands could plausibly run unchanged, with porting work limited to settings-file layout and the hook trust flow ([hooks reference](https://developers.openai.com/codex/hooks)).
2. `PermissionRequest` hooks for approval-path enforcement: allow or deny sandbox-escape and permission requests programmatically, with deny-wins folding ([permission_request.rs](https://github.com/openai/codex/blob/12933b69551394328319dcdd1bcee7907326dc85/codex-rs/hooks/src/events/permission_request.rs)).
3. Execpolicy `.rules` files for command-prefix rules: hookify rules that are pure command matchers (block `git push --force`, forbid `rm -rf`) could compile to Starlark `prefix_rule` entries with `decision = "forbidden"` or `"prompt"`, enforced by Codex itself with no subprocess ([exec-policy docs](https://developers.openai.com/codex/exec-policy)). Caveat: `codex exec --ignore-rules` can bypass them.
4. Sandbox and approval policy as coarse guardrails: `sandbox_mode` plus `approval_policy` (including the `granular` table) restrict whole capability classes rather than individual patterns ([config reference](https://developers.openai.com/codex/config-reference)).
5. `Stop` and `UserPromptSubmit` hooks for turn-level rules: Stop can block completion and inject continuation fragments ([stop.rs](https://github.com/openai/codex/blob/12933b69551394328319dcdd1bcee7907326dc85/codex-rs/hooks/src/events/stop.rs)); UserPromptSubmit can add context (but see fail-open caveat below).
6. Post-hoc transcript analysis against `$CODEX_HOME/sessions` JSONL rollouts, or the `transcript_path` handed to every hook: detection and warning, not prevention ([rollout crate](https://github.com/openai/codex/blob/12933b69551394328319dcdd1bcee7907326dc85/codex-rs/rollout/src/lib.rs)).
7. AGENTS.md guidance as the advisory layer only ([AGENTS.md guide](https://developers.openai.com/codex/guides/agents-md)); `notify` and MCP are not viable enforcement vectors (fire-and-forget, and own-tools-only respectively).

## What is impossible today

Claude Code hook capabilities with no Codex equivalent yet, each tracked upstream:

- Replacing or rewriting tool output after execution: PostToolUse has no `updatedToolOutput`; requested in [#38135](https://github.com/openai/codex/issues/38135) (open).
- Reacting to tool failure: PostToolUse payload carries no failure signal and there is no failure-specific event ([#34289](https://github.com/openai/codex/issues/34289), open).
- Fail-closed prompt gating: UserPromptSubmit hooks cannot reliably block a prompt before the model request ([#33630](https://github.com/openai/codex/issues/33630), open).
- Uniform coverage across entry points: `codex exec` does not dispatch repo `.codex/hooks.json` hooks ([#26383](https://github.com/openai/codex/issues/26383), open), untrusted projects silently skip project hooks ([#35306](https://github.com/openai/codex/issues/35306), open), and async handlers in Claude-format plugin hooks are skipped ([#34694](https://github.com/openai/codex/issues/34694), open). A hookify port cannot yet guarantee its rules fire on every Codex run the way Claude Code hooks do.
- Guaranteed enforcement against opt-outs: `--dangerously-bypass-hook-trust` ([hooks reference](https://developers.openai.com/codex/hooks)) and `codex exec --ignore-rules` ([non-interactive docs](https://developers.openai.com/codex/noninteractive)) both exist; only enterprise `requirements.toml` managed layers close those holes ([docs/config.md](https://github.com/openai/codex/blob/12933b69551394328319dcdd1bcee7907326dc85/docs/config.md)).
- Full event parity: the umbrella tracker [#21753](https://github.com/openai/codex/issues/21753) (open) documents remaining gaps event by event, so any port should treat per-event behavior as still moving.
