# Research: agent harness landscape sweep

Resolves issue #4. Snapshot date: 2026-08-15. Scope: major agent harnesses other than Claude Code and Codex CLI (covered by issues #2 and #3). All claims verified against each harness's own docs, source repos, or first-party APIs by parallel research subagents; a source is cited per claim. Star and download counts are live API snapshots taken on the date above.

No prior research notes existed in this repo, so this file establishes `docs/research/` as the location for research findings.

## Summary table

| Harness | Lifecycle hooks | Can block | Transcript to hooks | Config (global / project) | Adoption signal |
|---|---|---|---|---|---|
| Gemini CLI | Yes, 11 events, released | Yes, plus input rewrite | Yes, `transcript_path` | `~/.gemini/settings.json` / `.gemini/settings.json` | 106.5k stars, 396k npm dl/wk, Google |
| Cursor | Yes, ~20 events, released | Yes, `permission` field or exit 2 | Yes, `transcript_path` on every hook | `~/.cursor/hooks.json` / `.cursor/hooks.json` | $2B ARR (press), acquired by SpaceX |
| GitHub Copilot CLI | Yes, 14 events, GA | Yes, `permissionDecision` + `modifiedArgs` | No (session state on disk only) | `~/.copilot/hooks/` / `.github/hooks/` | 2.0M npm dl/wk, GitHub/Microsoft |
| Qwen Code | Yes, 19 events, released | Yes, Claude Code-compatible protocol | Yes, `transcript_path` | `~/.qwen/settings.json` / `.qwen/settings.json` | 27k stars, 74k npm dl/wk, Alibaba |
| Kiro | Yes, 11 triggers, released | Yes, exit 2 on 3 events | No, tool payloads only | `~/.kiro/hooks/` / `.kiro/hooks/` | AWS, 100k+ devs in first 5 days of preview |
| Windsurf (Cascade) | Yes, 12 events, released | Yes, exit 2 on pre-hooks only | Yes, on one dedicated event | `~/.codeium/windsurf/hooks.json` / `.windsurf/hooks.json` | Acquired by Cognition, $82M ARR at acquisition |
| Factory Droid | Yes, 9 events, released | Yes, exit 2 or `permissionDecision` | Yes, `transcript_path` | `~/.factory/hooks.json` / `.factory/hooks.json` | Closed source, $50M Series B |
| Cline | Yes, ~9 events, released (toggle) | Yes, `cancel: true` | No | `~/Documents/Cline/Hooks` / `.clinerules/hooks/` | 5.0M marketplace installs, 66k stars |
| OpenHands | Yes, Claude Code-compatible | Yes, blocking `PreToolUse` | Not documented | `.openhands/hooks.json` | 84k stars |
| opencode | JS/TS plugin API, not shell hooks | Yes, throw or `permission.ask` deny | Yes, via server API | `~/.config/opencode/` / `opencode.json` | 197k stars, 2.1M npm dl/wk |
| Amp | TS/JS plugin API, not shell hooks | Yes, `reject-and-continue` | Partial, live thread only | `~/.config/amp/` / `.amp/settings.json` | Closed source, 20k npm dl/wk, Sourcegraph |
| Crush | Preliminary, `PreToolUse` only | Yes, exit 2 or JSON deny | No, tool input only | `~/.config/crush/crushrc` / `.crushrc` | 27k stars, Charm |
| Augment (Auggie) | Yes, incl. blocking `PreToolUse` | Yes | Not documented | `~/.augment/settings.json` / `.augment/settings.json` | Closed source, 39k npm dl/wk |
| Amazon Q Dev CLI | Yes, 5 triggers | Yes, exit 2 on `preToolUse` | No | `~/.aws/amazonq/cli-agents/` / `.amazonq/cli-agents/` | Maintenance mode, superseded by Kiro CLI |
| Zed | No hooks | n/a (tool permissions only) | SQLite thread DB, Markdown export | `~/.config/zed/settings.json` / `.zed/settings.json` | 88.6k stars, $42M+ funding |
| Roo Code | No hooks (proposals open) | n/a (auto-approve config only) | JSON task files in globalStorage | `~/.roo/rules/` / `.roo/rules/` | 1.9M marketplace installs, 24k stars |

Also swept, no hooks and no meaningful guardrail hook surface: Goose (permission modes only, 52.8k stars), Aider (lint/test commands only, 48k stars), Continue (CLI tool permissions only, 35.5k stars), Trae Agent (MCP allowlist only, 12k stars).

## Per-harness findings

### Gemini CLI (Google)

- Hooks: shipped in v0.19.0 (2025-12-02) and v0.20.0, on by default (`hooksConfig.enabled` defaults to `true`). Events: `SessionStart`, `SessionEnd`, `BeforeAgent`, `AfterAgent`, `BeforeModel`, `AfterModel`, `BeforeToolSelection`, `BeforeTool`, `AfterTool`, `PreCompress`, `Notification`. Own vocabulary, not Claude Code's, and it uniquely has model-level events. `BeforeTool` blocks via `"decision": "deny"` and can rewrite tool args via `hookSpecificOutput.tool_input`. Protocol: stdin JSON (`session_id`, `transcript_path`, `cwd`, `hook_event_name`, `timestamp`), stdout JSON, exit 2 blocks with stderr as reason. Sources: https://github.com/google-gemini/gemini-cli/blob/main/docs/hooks/reference.md , https://github.com/google-gemini/gemini-cli/releases/tag/v0.19.0
- Config: user `~/.gemini/settings.json`, project `.gemini/settings.json`, system `/etc/gemini-cli/settings.json` (Linux) with system > workspace > user precedence; extensions can contribute hooks. Source: https://geminicli.com/docs/reference/configuration
- Permissions: `tools.core` / `tools.exclude` / `tools.allowed` / `tools.confirmationRequired`, approval modes (`default`, `auto_edit`, `plan`, YOLO flag), sandbox (Docker/Podman/Seatbelt), plus a newer Policy Engine and Trusted Folders. Sources: https://geminicli.com/docs/reference/configuration , https://geminicli.com/docs/reference/policy-engine
- Transcripts: JSONL at `~/.gemini/tmp/<project_id>/chats/<session>.jsonl`; every hook receives `transcript_path`. Sources: https://github.com/google-gemini/gemini-cli/blob/main/packages/core/src/services/chatRecordingService.ts , docs/hooks/reference.md
- Adoption: 106,523 stars; `@google/gemini-cli` at 396,002 npm downloads/week; weekly stable releases. Sources: GitHub API, api.npmjs.org

### Cursor

- Hooks: released, JSON-over-stdio in both directions. Agent events: `sessionStart`, `sessionEnd`, `preToolUse`, `postToolUse`, `postToolUseFailure`, `subagentStart`, `subagentStop`, `beforeShellExecution`, `afterShellExecution`, `beforeMCPExecution`, `afterMCPExecution`, `beforeReadFile`, `afterFileEdit`, `beforeSubmitPrompt`, `preCompact`, `stop`, `afterAgentResponse`, `afterAgentThought`, plus Tab and workspace events. Permission-type hooks return `"permission": "allow" | "deny" | "ask"`; exit 2 equals deny; other nonzero exit codes fail open. Command-based and prompt-based (LLM-evaluated) hook types. Source: https://cursor.com/docs/hooks.md
- Config: project `.cursor/hooks.json`, user `~/.cursor/hooks.json`, enterprise system paths; precedence Enterprise > Team > Project > User. CLI permissions in `~/.cursor/cli-config.json` and `.cursor/cli.json`. Sources: https://cursor.com/docs/hooks.md , https://cursor.com/docs/cli/reference/configuration.md
- Permissions: CLI `permissions.allow/deny` with `Shell(git)`, `Read(glob)`, `Mcp(server:tool)` syntax, deny beats allow; IDE run modes (Auto-review default, Allowlist, Run Everything); OS sandbox (Seatbelt, Landlock+seccomp, AppArmor). Docs call these "best-effort guardrails rather than a hard security boundary". Sources: https://cursor.com/docs/cli/reference/permissions.md , https://cursor.com/docs/agent/security.md
- Transcripts: every hook receives `transcript_path` (null if transcripts disabled) plus full tool payloads. On-disk chat history location is not documented. Source: https://cursor.com/docs/hooks.md
- Adoption: acquired by SpaceX (announced 2026-08-14); Bloomberg-reported $2B recurring revenue (2026-03-02, republished by Cursor). Sources: https://cursor.com/blog/joining-spacex , https://cursor.com/blog

### GitHub Copilot CLI

- Hooks: GA since 2026-02-25. Events (camelCase with PascalCase aliases): `sessionStart`, `sessionEnd`, `userPromptSubmitted`, `userPromptTransformed`, `preToolUse`, `postToolUse`, `postToolUseFailure`, `agentStop`, `subagentStart`, `subagentStop`, `errorOccurred`, `preCompact`, `permissionRequest`, `notification`. `preToolUse` returns `permissionDecision: allow|deny|ask` and can rewrite args via `modifiedArgs`; exit 2 denies (fail-closed), but hook timeouts are always fail-open, a documented guardrail gap. Hook types: `command` (separate `bash`/`powershell` keys), `http`, `prompt`. Sources: https://docs.github.com/en/copilot/reference/hooks-reference , https://github.blog/changelog/2026-02-25-github-copilot-cli-is-now-generally-available/
- Config: admin policy `/etc/github-copilot/policy.d/`, repo `.github/hooks/*.json`, user `~/.copilot/hooks/`, inline blocks in `.github/copilot/settings.json` and `~/.copilot/settings.json`. Source: https://docs.github.com/en/copilot/reference/copilot-cli-reference/cli-config-dir-reference
- Permissions: `--allow-tool` / `--deny-tool` with `shell(git:*)` patterns, deny wins over everything including saved approvals in `permissions-config.json`; interactive approval by default; no OS sandbox documented. Source: https://docs.github.com/en/copilot/how-tos/copilot-cli/use-copilot-cli/allowing-tools
- Transcripts: session event logs at `~/.copilot/session-state/<session>/events.jsonl`; hook payloads carry `sessionId`/`toolName`/`toolArgs` but no transcript path. Sources: cli-config-dir-reference, hooks-reference above
- Adoption: `@github/copilot` at 2,004,492 npm downloads/week; 11,095 stars on github/copilot-cli; GitHub/Microsoft. Sources: api.npmjs.org, GitHub API

### Qwen Code (Alibaba)

- Hooks: shipped v0.13.0 (2026-03-23). Despite being a Gemini CLI fork, it uses Claude Code-compatible naming: `PreToolUse`, `PostToolUse`, `PostToolUseFailure`, `UserPromptSubmit`, `SessionStart`, `SessionEnd`, `SessionDelete`, `MessageDisplay`, `Stop`, `StopFailure`, `SubagentStart`, `SubagentStop`, `PreCompact`, `PostCompact`, `Notification`, `PermissionRequest`, `PermissionDenied`, `TodoCreated`, `TodoCompleted`. `PreToolUse` blocks via `hookSpecificOutput.permissionDecision: deny|ask`, rewrites input via `updatedInput`. Three hook types: command, HTTP (with SSRF protection), prompt. Exit 2 blocks. Source: https://github.com/QwenLM/qwen-code/blob/main/docs/users/features/hooks.md
- Config: user `~/.qwen/settings.json`, project `.qwen/settings.json`, system paths; extension system can install Claude Code plugins (converts `claude-plugin.json`) and Gemini CLI extensions. Sources: https://github.com/QwenLM/qwen-code/blob/main/docs/users/configuration/settings.md , docs/users/extension/introduction.md
- Permissions: `tools.core/exclude/allowed`; five approval modes (`plan`, `default`, `auto-edit`, `auto`, `yolo`); sandbox profiles; trusted folders. Source: https://github.com/QwenLM/qwen-code/blob/main/docs/users/features/approval-mode.md
- Transcripts: JSONL at `~/.qwen/projects/<project-id>/chats/<sessionId>.jsonl`; hooks receive `transcript_path` and `agent_transcript_path` on `SubagentStop`. Source: https://github.com/QwenLM/qwen-code/blob/main/packages/core/src/config/config.ts
- Adoption: 27,036 stars; 74,279 npm downloads/week; multiple releases per week. Sources: GitHub API, api.npmjs.org

### Kiro (AWS)

- Hooks: since IDE 1.0 / CLI 3.0, a unified JSON format (`.kiro/hooks/*.json`) covering file events (`PostFileSave`, `PostFileCreate`, `PostFileDelete`), lifecycle (`SessionStart`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Stop`), and task events (`PreTaskExec`, `PostTaskExec`, IDE only). Actions are shell commands or agent prompts. Blocking: exit 2 on `PreToolUse`, `UserPromptSubmit`, `PreTaskExec`; Stop can return `decision: block`. Stdin JSON with `tool_name`/`tool_input`/`tool_response`. Sources: https://kiro.dev/docs/hooks.md , https://kiro.dev/docs/hooks/types.md
- Config: global `~/.kiro/` (hooks, `settings/permissions.yaml`, agents, steering), project `.kiro/`. Workspace permissions deliberately live outside the repo at `~/.kiro/workspace-roots/<hash>/permissions.yaml` so "A cloned repo cannot inject permission rules". Source: https://kiro.dev/docs/configuration.md
- Permissions: capability-based YAML rules (`fs_read`, `fs_write`, `shell`, `mcp`, ...) with deny > ask > allow, most-restrictive-wins across scopes; compound shell commands are split and evaluated per sub-command; hardcoded guardrails (writes to `.kiro/settings/` always denied). Source: https://kiro.dev/docs/permissions.md
- Transcripts: hooks get tool payloads but no transcript path; no on-disk chat history location documented. Source: https://kiro.dev/docs/hooks/types.md
- Adoption: AWS product; own retrospective claims 100,000+ developers in the first 5 days of the July 2025 preview, doubling over quarters; CLI 2.0 headless CI/CD, Web and iOS clients. Source: https://kiro.dev/blog/one-year/

### Windsurf / Cascade (Cognition)

- Hooks: 12 events. Pre-hooks (blocking via exit 2): `pre_read_code`, `pre_write_code`, `pre_run_command`, `pre_mcp_tool_use`, `pre_user_prompt`. Post-hooks: `post_read_code`, `post_write_code`, `post_run_command`, `post_mcp_tool_use`, `post_cascade_response`, `post_cascade_response_with_transcript`, `post_setup_worktree`. Exit-code-only protocol, no JSON permission field; stdin JSON carries a per-event `tool_info` payload. Hooks do not load in Restricted Mode. Source: https://docs.windsurf.com/windsurf/cascade/hooks.md
- Config: workspace `.windsurf/hooks.json`, user `~/.codeium/windsurf/hooks.json`, system paths; three levels merge. Same source.
- Permissions: auto-execution levels (Disabled, Allowlist Only, Auto, Turbo) with `windsurf.cascadeCommandsAllowList` / `DenyList` (deny wins); team-enforced caps; no OS sandbox documented. Source: https://docs.windsurf.com/windsurf/terminal.md
- Transcripts: `post_cascade_response_with_transcript` passes `tool_info.transcript_path`, a JSONL at `~/.windsurf/transcripts/{trajectory_id}.jsonl`. Source: hooks doc above.
- Adoption: acquired by Cognition (July 2025); $82M ARR and "hundreds of thousands of daily active users" per Cognition's announcement. Brand risk note: windsurf.com now redirects to devin.ai/desktop and docs are served from docs.devin.ai. Source: https://cognition.com/blog/windsurf

### Factory Droid CLI

- Hooks: released, Claude Code-style. Events: `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `Notification`, `Stop`, `SubagentStop`, `PreCompact`, `SessionStart`, `SessionEnd`. Blocks via exit 2 or `hookSpecificOutput.permissionDecision: deny`. Stdin JSON includes `transcript_path`, `permission_mode`, tool payloads, plus a `commandRegex` matcher for shell strings. Source: https://docs.factory.ai/harness/hooks.md
- Config: user `~/.factory/hooks.json`, project `.factory/hooks.json`, enterprise-managed; settings in `~/.factory/settings.json` and `.factory/settings.local.json`. Source: https://docs.factory.ai/droid-cli/settings.md
- Permissions: `commandAllowlist` / `commandDenylist` / `commandBlocklist` (blocklist can never run, takes precedence); autonomy levels Off/Low/Medium/High with enterprise caps; OS-level sandbox; Droid Shield secret detection on git push. Sources: settings.md, https://docs.factory.ai/autonomy-and-safety/sandbox.md
- Transcripts: hooks receive `transcript_path`; sessions mirror to Factory web by default (`cloudSessionSync`). Source: hooks.md
- Adoption: closed source; $50M Series B (NEA, Sequoia, NVIDIA, J.P. Morgan), 2025-09-25. Source: https://factory.ai/news/series-b

### Cline

- Hooks: released (v3.36.0, behind a Settings toggle). Extension hook files, one executable per event: `TaskStart`, `TaskResume`, `TaskCancel`, `UserPromptSubmit`, `PreToolUse`, `PostToolUse`, `Notification` (plus `TaskComplete` and `PreCompact` "coming soon"). Stdin JSON, stdout `{"cancel": bool, "contextModification": ..., "errorMessage": ...}`; `cancel: true` blocks. PreToolUse cannot modify tool parameters. The newer SDK/CLI plugin system has a richer lifecycle (`tool_call_before`, `fail_closed` policies). Sources: https://github.com/cline/cline/blob/main/.clinerules/hooks/README.md , https://docs.cline.bot/sdk/plugins
- Config: global `~/.cline/` and `~/Documents/Cline/Hooks`; project `.clinerules/hooks/`. Source: https://docs.cline.bot/getting-started/config
- Permissions: auto-approve toggles per category (read/edit/execute/browser/MCP) with dynamic command safety classification, no fixed allowlist; experimental YOLO mode. Source: https://docs.cline.bot/features/auto-approve
- Transcripts: plain JSON per task under VS Code globalStorage (`saoudrizwan.claude-dev/tasks/<taskId>/api_conversation_history.json`); CLI sessions in `~/.cline/data/sessions/`. Source: apps/vscode/src/core/storage/disk.ts
- Adoption: 4,990,137 VS Code Marketplace installs; 66,225 stars; $32M seed + Series A. Sources: Marketplace gallery API, GitHub API, https://cline.bot/blog

### opencode (Anomaly, formerly SST)

- Hooks: no shell hooks; a released JS/TS plugin API. Key hooks: `tool.execute.before` / `tool.execute.after`, `permission.ask`, `chat.message`, `chat.params`, `command.execute.before`, `event` (bus events), plus experimental message transforms. Blocks by throwing in `tool.execute.before` or setting `output.status = "deny"` in `permission.ask`. Sources: https://opencode.ai/docs/plugins/ , https://github.com/anomalyco/opencode/blob/dev/packages/plugin/src/index.ts
- Config: project `opencode.json(c)`, global `~/.config/opencode/`, plugins in `.opencode/plugins/` or npm packages. Source: https://opencode.ai/docs/config/
- Permissions: `permission` block with allow/ask/deny per tool and bash wildcard patterns (`"git *": "allow"`); per-agent overrides. Source: https://opencode.ai/docs/permissions/
- Transcripts: sessions as files under `~/.local/share/opencode/project/<slug>/storage/`; plugins get full conversation access via the server SDK. Source: https://opencode.ai/docs/troubleshooting/
- Adoption: 197,677 stars (repo moved to anomalyco/opencode); npm `opencode-ai` at 2,138,728 downloads/week; multiple releases per week. Sources: GitHub API, api.npmjs.org

### Amp (Sourcegraph)

- Hooks: TS/JS plugin API, documented as core. Events: `session.start`, `agent.start`, `tool.call`, `tool.result`, `agent.end`. `tool.call` returns `allow`, `reject-and-continue`, `modify`, or `synthesize`. Source: https://ampcode.com/manual
- Config: global `~/.config/amp/settings.json(c)`, project `.amp/settings.json(c)`, enterprise managed paths. Same source.
- Permissions: allow-by-default ("Amp does not ask for approval before running tools"); `amp.permissions` rules (now labeled legacy) with allow/reject/ask/delegate; `amp.tools.disable`; guarded files allowlist; recommended modern gate is a plugin `tool.call` handler. Source: https://ampcode.com/manual/appendix/legacy-permissions-rules.txt
- Transcripts: threads are server-backed on ampcode.com; plugins interact with the live thread but no bulk transcript read documented. Source: https://ampcode.com/manual
- Adoption: closed source; npm `@sourcegraph/amp` at 20,160 downloads/week, continuous release cadence. Source: registry.npmjs.org

### Crush (Charm)

- Hooks: preliminary and explicitly Claude Code-compatible shell hooks, currently only `PreToolUse`. Exit 2 blocks (stderr as reason), exit 49 halts the turn, JSON stdout supports `decision: allow|deny`, `updated_input` rewrite, context injection. Input via env vars and stdin JSON. Only fires for the top-level agent. Source: https://github.com/charmbracelet/crush/blob/main/docs/hooks/README.md
- Config: current format is `crushrc` (Bash with builtins) at `./.crushrc` or `~/.config/crush/crushrc`; legacy `crush.json` deprecated. Source: https://github.com/charmbracelet/crush/blob/main/docs/config/README.md
- Permissions: ask-by-default; `permissions allow`/`deny` builtins; `--yolo`. Source: https://github.com/charmbracelet/crush/blob/main/README.md
- Transcripts: per-project SQLite at `.crush/crush.db`; hooks do not receive transcript access. Source: internal/db/connect.go
- Adoption: 27,395 stars; roughly weekly releases; Charm-backed. Sources: GitHub API, releases

### Zed

- Hooks: none released. Proposals existed (issues #57890, #60565) but no user-facing hooks feature resulted. Closest equivalents: pattern-based `agent.tool_permissions` (regex `always_allow`/`always_deny`/`always_confirm`, immutable built-in blocks), agent profiles, OS sandboxing, and ACP for hosting external agents. Sources: https://zed.dev/docs/ai/tool-permissions , https://zed.dev/docs/ai/external-agents
- Config: `~/.config/zed/settings.json` global, `.zed/settings.json` project. Source: https://zed.dev/docs/configuring-zed
- Transcripts: SQLite `threads.db` in the Zed data dir; Markdown export command. Source: crates/agent/src/db.rs
- Adoption: 88,642 stars; $32M Series B led by Sequoia (2025-08-20). Sources: GitHub API, https://zed.dev/blog/sequoia-backs-zed
- Strategic note: Zed is reachable indirectly, hookify rules applied to Claude Code, Codex, or Gemini CLI still apply when those agents run inside Zed via ACP.

### Roo Code

- Hooks: none, released or beta; open enhancement issues only (#12206, #11504, #12025). Closest equivalents: granular auto-approve with a command allowlist/denylist (longest-prefix matching, deny wins), custom modes with per-mode tool groups and `fileRegex` restrictions. Sources: https://github.com/RooCodeInc/Roo-Code/issues/12206 , https://docs.roocode.com/features/auto-approving-actions
- Config: global `~/.roo/rules/`, workspace `.roo/rules/`, `.roomodes`. Source: https://docs.roocode.com/features/custom-instructions
- Transcripts: JSON task files under VS Code globalStorage; Markdown export in-product. Source: src/core/task-persistence/
- Adoption: 1,927,444 Marketplace installs; 24,341 stars. Sources: gallery API, GitHub API

### Amazon Q Developer CLI

- Hooks: 5 triggers in agent configs (`agentSpawn`, `userPromptSubmit`, `preToolUse`, `postToolUse`, `stop`); exit 2 blocks `preToolUse`. But the project is in maintenance mode: the README states it is no longer actively maintained and is superseded by Kiro CLI. Not a v1 candidate. Sources: https://github.com/aws/amazon-q-developer-cli/blob/main/docs/hooks.md , repo README

### Sweep: other harnesses checked

- OpenHands (All Hands AI): released Claude Code-compatible shell hooks in `.openhands/hooks.json` with blocking `PreToolUse`, `UserPromptSubmit`, `Stop`, working across Cloud, CLI, GUI; confirmation policies with risk scoring; Docker sandbox. 84,108 stars. Source: https://docs.openhands.dev/openhands/usage/customization/hooks.md
- Augment Code CLI (Auggie): released hooks including blocking `PreToolUse` and `Stop`, plus `toolPermissions` rules (most-restrictive-wins) in `~/.augment/settings.json` and `.augment/settings.json`, immutable `/etc/augment/settings.json`. Closed source; 38,802 npm downloads/week. Sources: https://docs.augmentcode.com/cli/hooks.md , https://docs.augmentcode.com/cli/permissions.md
- Goose (Block / Linux Foundation): no hooks; permission modes (`auto`, `approve`, `smart_approve`, `chat`) and an admin extension allowlist. 52,836 stars. Source: https://goose-docs.ai/docs/guides/managing-tools/goose-permissions
- Aider: no hooks, no allow/deny system; `--lint-cmd` / `--test-cmd` loops only. 48,245 stars. Source: https://aider.chat/docs/usage/lint-test.html
- Continue: no hooks; `cn` CLI tool permissions (`--allow`/`--ask`/`--exclude`, `~/.continue/permissions.yaml`). 35,491 stars. Source: https://docs.continue.dev/cli/tool-permissions
- Trae Agent (ByteDance): no hooks, no approval system; MCP allowlist only, sandbox is a roadmap item. 12,022 stars. Source: https://github.com/bytedance/trae-agent

## Cross-cutting observations

1. The Claude Code hook protocol is becoming a de facto standard. Qwen Code, Factory Droid, OpenHands, Augment, and Crush all deliberately clone it (snake_case stdin fields, exit code 2 to block, `permissionDecision`), and Copilot CLI ships PascalCase compatibility aliases. An adapter targeting that wire format covers six harnesses nearly for free.
2. Two distinct extension models exist: shell-command hooks (Gemini, Cursor, Copilot, Qwen, Kiro, Windsurf, Factory, Cline, OpenHands, Crush, Augment, Amazon Q) and in-process JS/TS plugin APIs (opencode, Amp). The latter need a thin plugin shim rather than a hooks.json adapter.
3. Transcript access divides the field: Gemini, Cursor, Qwen, and Factory pass `transcript_path` to hooks; Windsurf only on one dedicated event; Copilot, Kiro, Cline, Crush, and Amazon Q give hooks tool payloads only.
4. Fail-open vs fail-closed semantics differ and matter for guardrails: Cursor fails open on hook errors, Copilot CLI fails open on hook timeouts even for `preToolUse`, while Copilot's exit-2 path and Cline's SDK `fail_closed` policy are fail-closed.
5. Every harness with hooks supports both a global (home directory) and a project-level config location, and several add an enterprise/system tier (Cursor, Copilot, Windsurf, Gemini, Qwen, Factory, Augment).

## Ranking: v1-candidate list

Ranked by hook-API maturity (event coverage, blocking, input rewrite, transcript access, released status) combined with adoption. Claude Code and Codex CLI are out of scope here (issues #2, #3).

### Tier 1: v1 candidates

1. Gemini CLI: complete hook API (block + rewrite + transcript), on by default, and the largest open source adoption of any harness with shell hooks (106.5k stars, 396k npm dl/wk, Google backing).
2. Cursor: the widest event surface (~20 events), JSON permission protocol, transcript on every hook, enterprise config tiers, and the largest commercial adoption in the field. Caveat: fail-open on hook errors.
3. GitHub Copilot CLI: 14 GA events, `permissionDecision` + `modifiedArgs`, three hook types, 2M npm dl/wk and Microsoft distribution. Caveat: no transcript path, fail-open timeouts.
4. Qwen Code: Claude Code wire-compatible hooks (lowest adapter cost of all), 19 events, transcript access, HTTP hooks; adoption is solid (27k stars, 74k dl/wk) though an order of magnitude below the top three.

### Tier 2: fast follows

5. Kiro: real lifecycle hooks with blocking, the strongest permission model in the field (capability YAML, anti-injection design), AWS backing; held back by no transcript access and a younger CLI.
6. Factory Droid: near-perfect Claude Code protocol clone including `transcript_path`; held back only by closed source and smaller adoption.
7. Cline: huge adoption (5M installs) and released hooks, but the hook protocol is nonstandard (`cancel` JSON, no input rewrite), behind a feature toggle, and mid-migration to the SDK plugin lifecycle.
8. Windsurf: 12 events but exit-code-only blocking and post-acquisition brand churn (docs now serve from docs.devin.ai).

### Tier 3: needs a different adapter shape or more maturity

9. opencode: enormous adoption (197k stars, 2.1M dl/wk) and a blocking plugin API, but it requires a JS plugin shim, not a hooks-config adapter.
10. OpenHands: Claude Code-compatible hooks, strong stars (84k); agent platform rather than daily-driver CLI, adoption signal for hooks usage unclear.
11. Amp: blocking plugin API but closed source, server-backed threads, small distribution.
12. Crush: deliberately Claude Code-compatible but only `PreToolUse` so far; watch for the open PR adding more lifecycle hooks.
13. Augment (Auggie): hooks + permissions exist, but closed source and modest adoption.

### Not viable for hooks v1

- Zed and Roo Code: no hook mechanism (permissions only; Roo has open proposals). Revisit if hooks ship.
- Amazon Q Developer CLI: hooks exist but the project is in maintenance mode, superseded by Kiro CLI.
- Goose, Aider, Continue, Trae: no hook surface at all.
