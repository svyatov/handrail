# Google Gemini CLI: adapter surface for handrail

Date: 2026-08-21

Research question (handrail issue #93): `docs/spec.md` section 12 records a paper check, performed at spec assembly against the 2026-08-15 landscape research, that concluded "the Adapter interface accommodates Gemini CLI with no interface changes." Verify each of its six claims against Gemini CLI's own current documentation and source, then go beyond it: the full event list (for T9), tool names and tool-kind classification, whether a Promotion target exists, and known bypasses.

Sources are primary only: the [google-gemini/gemini-cli](https://github.com/google-gemini/gemini-cli) repository at commit `30573d2e4d85bdc2c0ae8218c377cd410336da77` (main, 2026-08-20), which is the tree behind the latest stable release [v0.56.0](https://github.com/google-gemini/gemini-cli/releases/tag/v0.56.0) (2026-08-19); the in-repo documentation under `docs/`, which is what the docs site serves; and the project's issue tracker. Gemini CLI publishes no `llms.txt` or `llms-full.txt`, so the in-repo markdown is the documentation of record. Where the docs and the source disagree, this note says so and treats the source as authoritative.

## 1. Verdict on the six section-12 claims

| # | Section 12 claim | Verdict |
| :- | :--- | :--- |
| 1 | All six core events map onto Gemini's vocabulary | **Holds** at the name level, with two behavioural caveats (section 2) |
| 2 | `BeforeTool` blocks via `decision: deny` or exit 2 | **Holds**, but understates: every non-zero exit code except 1 blocks (section 3) |
| 3 | Hooks are on by default | **Holds** (section 6) |
| 4 | One user-level entry per event fits `~/.gemini/settings.json` | **Holds** (section 6) |
| 5 | The stdin envelope covers `session_id`, `transcript_path`, `cwd` | **Holds**, but `transcript_path` can be the empty string (section 4) |
| 6 | Transcript access enables Analyzer parity | **Partly wrong.** Transcript exists and is JSONL, but it is not addressable by session id, and the payload field can be empty (section 10) |
| - | **Conclusion: no interface changes** | **Does not survive.** Two capability-matrix flags that section 4 of the spec declares global must become per-event or per-failure-mode (section 11) |

## 2. The event model

Gemini CLI has eleven hook events, declared in the `HookEventName` enum in [packages/core/src/hooks/types.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/hooks/types.ts):

`BeforeTool`, `AfterTool`, `BeforeAgent`, `Notification`, `AfterAgent`, `SessionStart`, `SessionEnd`, `PreCompress`, `BeforeModel`, `AfterModel`, `BeforeToolSelection`.

The mapping claimed by section 12 is correct as a naming map:

| handrail core event | Gemini event |
| :--- | :--- |
| `PreToolUse` | `BeforeTool` |
| `PostToolUse` | `AfterTool` |
| `UserPromptSubmit` | `BeforeAgent` |
| `Stop` | `AfterAgent` |
| `SessionStart` | `SessionStart` |
| `SessionEnd` | `SessionEnd` |

Two behavioural caveats sit under that map:

- `AfterAgent` is not fired unconditionally. In [packages/core/src/core/client.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/core/client.ts), `fireAfterAgentHookSafe` returns early when the hook state's `activeCalls !== 1` and no stop hook is active, and again when the turn still has pending tool calls. Users report it never firing at all from `settings.json`: [#27712 "bug: hooks.AfterAgent is never executed in settings.json configuration"](https://github.com/google-gemini/gemini-cli/issues/27712) (open, 2026-06-06).
- `SessionStart` has its own open reliability report: [#28160 "SessionStart hook not observed on startup, clear, or resume in v0.47.0"](https://github.com/google-gemini/gemini-cli/issues/28160) (open, 2026-06-26).

The five events beyond handrail's core are `Notification`, `PreCompress`, `BeforeModel`, `AfterModel`, `BeforeToolSelection`. For T9 this is the useful part of the comparison: `PreCompress` is Gemini's name for the compaction event that Claude Code calls `PreCompact` and Codex splits into `PreCompact`/`PostCompact`, so a compaction event is the one candidate with a counterpart on all three harnesses. `BeforeModel`/`AfterModel` and `BeforeToolSelection` are Gemini-only among the three, and `Notification` exists on Claude Code but not Codex.

## 3. Blocking, decisions and exit codes

`HookDecision` is `'ask' | 'block' | 'deny' | 'approve' | 'allow' | undefined`, and `isBlockingDecision()` returns true for `'block'` and `'deny'` ([types.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/hooks/types.ts)). So `decision: "deny"` on `BeforeTool` blocks, as claimed.

Exit codes are where the documentation and the source part company. The exit-code table in [docs/hooks/index.md](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/docs/hooks/index.md) gives the Claude-style three-way split: `0` success, `2` "System Block", and `Other` labelled "Warning" with the impact "Non-fatal failure. A warning is shown, but the interaction proceeds using original parameters." The source in [packages/core/src/hooks/hookRunner.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/hooks/hookRunner.ts) does the opposite for codes above 2:

```ts
if (exitCode === EXIT_CODE_SUCCESS) { return { decision: 'allow', systemMessage: text }; }
else if (exitCode === EXIT_CODE_NON_BLOCKING_ERROR) {
  return { decision: 'allow', systemMessage: `Warning: ${text}` };
} else {
  // All other non-zero exit codes (including 2) are blocking
  return { decision: 'deny', reason: text };
}
```

`EXIT_CODE_SUCCESS` is 0 and `EXIT_CODE_NON_BLOCKING_ERROR` is 1. The text handed to the model is `stdout.trim() || stderr.trim()`. Consequence for an adapter: a hook binary that exits 1 warns, and a hook binary that dies with any other non-zero status blocks the tool call. handrail's own exit-code contract must therefore be exact on Gemini in a way it need not be on Claude Code, where only 2 is special.

Ordering matters too. In `_processToolCall` in [packages/core/src/scheduler/scheduler.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/scheduler/scheduler.ts) the `BeforeTool` hook check runs at step 1 and the policy and security check at step 2, so a hook deny wins even in `yolo` mode. Blocking surfaces to the model as `ToolErrorType.POLICY_VIOLATION` ([packages/core/src/scheduler/hook-utils.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/scheduler/hook-utils.ts)).

Multiple hooks on one event fold with an "any block wins" rule in [packages/core/src/hooks/hookAggregator.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/hooks/hookAggregator.ts): blocking decisions beat `ask`, reasons and system messages are joined with newlines, and `additionalContext` values are concatenated.

## 4. Context injection is per-event, not global

This is the finding that breaks section 12's conclusion.

`docs/spec.md` section 4 declares context injection a global flag on the capability matrix, one value per harness. On Gemini CLI it is a property of the individual event:

- `SessionStart` injects: the non-interactive path in [packages/cli/src/gemini.tsx](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/cli/src/gemini.tsx) wraps `additionalContext` in `<hook_context>` tags before handing it to the model.
- `BeforeAgent` injects: it returns `{ additionalContext }` in [packages/core/src/core/client.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/core/client.ts).
- `AfterTool` injects: [packages/core/src/core/coreToolHookTriggers.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/core/coreToolHookTriggers.ts) appends `"\n\n<hook_context>" + additionalContext + "</hook_context>"` to `toolResult.llmContent`.
- `BeforeTool` does **not** inject. `evaluateBeforeToolHook` in [packages/core/src/scheduler/hook-utils.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/scheduler/hook-utils.ts) reads only `shouldStopExecution()`, `getBlockingError()`, `isAskDecision()` and `getModifiedToolInput()`. It never calls `getAdditionalContext()`. The one message it can surface, `systemMessage`, is read only on the `ask` path, and [packages/core/src/hooks/hookEventHandler.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/hooks/hookEventHandler.ts) marks `systemMessage` explicitly: "Handle systemMessage - show to user in transcript mode (not to agent)".

So on Gemini CLI a `warn` outcome on `PreToolUse`, which the spec defines as injecting the message into the agent's context, has no injection channel and must take the spec's documented fallback: degrade to a user-visible message. On `PostToolUse` the same `warn` injects normally. One harness-level flag cannot express that.

The stdin envelope itself is exactly as claimed. `createBaseInput` in [hookEventHandler.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/hooks/hookEventHandler.ts) emits `session_id`, `transcript_path`, `cwd`, `hook_event_name` and `timestamp`, and `transcript_path` falls back to `''` when the chat recording service has no file yet:

```ts
const transcriptPath =
  this.context.geminiClient?.getChatRecordingService()?.getConversationFilePath() ?? '';
```

MCP tool events additionally carry `mcp_context` (`server_name`, `tool_name`, and optionally `command`, `args`, `cwd`, `url`, `tcp`), which is a cleaner source for handrail's `server` and `tool` payload fields than parsing the tool name (section 7).

## 5. Fail-open behaviour splits by failure mode

Section 4 of the spec also carries fail-open-on-hook-error and fail-open-on-timeout as global flags. On Gemini CLI the two split, and neither is a single value:

- Spawn failure: `child.on('error')` in [hookRunner.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/hooks/hookRunner.ts) resolves with `success: false` and no output, which folds to allow. Fails open.
- Timeout (default 60000 ms per hook): likewise yields no output, so it folds to allow. Fails open.
- Non-zero exit with output: blocks, as shown in section 3. Fails **closed**, and for any code except 1.

A misconfigured handrail hook command therefore fails open on Gemini, but a handrail binary that panics after writing to stderr fails closed. The capability matrix needs both facts, not one flag.

## 6. Configuration: settings.json, tiers and merge

Claims 3 and 4 both hold.

`hooksConfig.enabled` is declared with default `true` in [packages/cli/src/config/settingsSchema.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/cli/src/config/settingsSchema.ts), described as the "Canonical toggle for the hooks system. When disabled, no hooks will be executed." Hooks are on by default.

The top-level `hooks` object in the same schema holds one array per event name, each with `mergeStrategy: MergeStrategy.CONCAT`. Settings merge in [packages/cli/src/config/settings.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/cli/src/config/settings.ts) runs `schemaDefaults, systemDefaults, user, safeWorkspace, system`. Because per-event arrays concatenate rather than replace, one handrail entry per event written into the user-level file coexists with whatever a project defines. That is exactly sync's one-user-level-entry-per-event model, so claim 4 holds.

The user-level path is `homedir()/.gemini/settings.json` via `getGlobalSettingsPath()` in [packages/core/src/config/storage.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/config/storage.ts), with `GEMINI_DIR = '.gemini'`. Note for testing: there is **no relocation environment variable** for the user settings path, unlike `CODEX_HOME` on Codex. `GEMINI_CLI_SYSTEM_SETTINGS_PATH` relocates only the system file, and `GEMINI_CLI_TRUSTED_FOLDERS_PATH` only the trust file. Any testscript-level exercise of a Gemini writer has to redirect `$HOME`.

A hook entry is `{ matcher?, sequential?, hooks: [...] }`, and each hook is `{ type: 'command' | 'runtime' | 'plugin', command, name?, description?, timeout?, env? }` ([types.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/hooks/types.ts)). Matcher semantics, from [packages/core/src/hooks/hookPlanner.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/hooks/hookPlanner.ts): absent matcher, `''`, or `'*'` matches everything; for tool events the matcher is compiled with `new RegExp(matcher)` and tested **unanchored** against the tool name, falling back to literal equality only if the regex is invalid; for lifecycle events the matcher is compared for exact equality against the trigger string. Identical hooks are deduplicated on the key `` `${name}:${command}` ``.

## 7. Tool names and tool-kind classification

Wire names and argument keys come from [packages/core/src/tools/definitions/base-declarations.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/tools/definitions/base-declarations.ts). Names are shared constants across model families; only the JSON schemas differ between `model-family-sets/gemini-3.ts` and `model-family-sets/default-legacy.ts`, so an adapter can key on the name safely.

| Gemini tool | handrail `tool_kind` | Payload source |
| :--- | :--- | :--- |
| `run_shell_command` | `shell` | `command` |
| `write_file` | `file_edit` | `file_path`, `content` |
| `replace` | `file_edit` | `file_path`; no whole-file content, only `old_string`/`new_string` |
| `read_file` | `file_read` | `file_path` |
| `read_many_files` | `file_read` | no single path: `include`/`exclude` glob arrays |
| `list_directory` | `file_read` | `dir_path` |
| `glob` | `file_read` | `pattern`, `dir_path` |
| `grep_search` | `file_read` | `pattern`, `include_pattern`, `exclude_pattern` |
| `mcp_<server>_<tool>` | `mcp` | `mcp_context.server_name`, `mcp_context.tool_name` |
| `google_web_search`, `web_fetch`, `write_todos`, `ask_user`, `activate_skill`, `get_internal_docs`, `enter_plan_mode`, `read_mcp_resource`, `list_mcp_resources` | `other` | - |

Two wrinkles for the classifier:

- `replace` is Gemini's edit tool, and its payload has no `content` field. A handrail rule matching on `content` for `file_edit` sees nothing on `replace` unless the adapter maps `new_string` onto it, which changes the meaning of the field (a fragment, not the file).
- `read_many_files` is a `file_read` with no path. A rule keyed on `path` cannot match it.

MCP naming differs from Claude Code. [packages/core/src/tools/mcp-tool.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/tools/mcp-tool.ts) sets `MCP_TOOL_PREFIX = 'mcp_'` and `MCP_QUALIFIED_NAME_SEPARATOR = '_'`, giving `mcp_<server>_<tool>` against Claude Code's `mcp__server__tool`. Worse for parsing, `parseMcpToolName` splits on `/^([^_]+)_(.+)$/`, so a server name containing an underscore is misparsed, and `generateValidName` replaces every character outside `[a-zA-Z0-9_\-.:]` with `_` and truncates names over 63 characters to `slice(0,30) + '...' + slice(-30)`. An adapter should read `mcp_context` from the payload and treat the tool name as unreliable for server extraction.

## 8. Promotion target: the policy engine

Gemini has no equivalent of handrail's `if` field inside the hook configuration. The hook definition offers exactly one conditional, `matcher`, and it matches only the tool name.

It does, however, have a native permission mechanism the Advisor can target, so **Gemini earns a Promotion row**. Per [docs/reference/policy-engine.md](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/docs/reference/policy-engine.md), TOML rule files declare `[[rule]]` entries with `toolName`, `commandPrefix`, `commandRegex`, `argsPattern` and `decision` of `allow`, `deny` or `ask_user`, plus a `denyMessage`. User-level rules live in `~/.gemini/policies/*.toml` (`getUserPoliciesDir()` in [storage.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/config/storage.ts)).

The clean promotion is a `shell` rule whose condition is a command prefix: it restates as `toolName = "run_shell_command"`, `commandPrefix = "..."`, `decision = "deny"`, `priority = 999`, with the rule's message as `denyMessage`.

Priority is not optional and it decides whether a promotion actually binds. `transformPriority` in [packages/core/src/policy/toml-loader.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/policy/toml-loader.ts) computes `tier + priority / 1000`, with tiers 1 default, 2 extension, 3 workspace, 4 user, 5 admin, and rejects any priority above 999 to prevent tier overflow. Higher wins. Two consequences:

- `yolo` mode does **not** defeat a user-level promoted rule. Its allow-all lives in the default tier at raw priority 998 ([packages/core/src/policy/policies/yolo.toml](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/policy/policies/yolo.toml)), which transforms to 1.998 and loses to any user-tier rule at 4.x. Promotion on Gemini is genuinely load-bearing, unlike the Codex execpolicy case where `--ignore-rules` voids it.
- The user tier is shared with dynamic rules, notably 4.95 for tools the user marked "Always Allow" in the interactive UI (documented in the priority band comment in `yolo.toml`). A promoted deny needs `priority = 999`, giving 4.999, to outrank an earlier interactive always-allow.

One caveat belongs in the Promotion row: the workspace tier does not work, per [#18186 "Workspace/.gemini/policies/*.toml are not taking effect"](https://github.com/google-gemini/gemini-cli/issues/18186) (open, 2026-02-03). Only the user tier is a viable promotion target for handrail today, which happens to match the Global tier and leaves Project-shared rules with no promotion path.

## 9. Known bypasses

For the capability matrix's "Known bypasses" row. The first four disable handrail's hooks; the last two touch the trust gate and the policy layer.

1. `hooksConfig.enabled: false` in any settings tier turns off the whole hook system. `Config` only constructs `HookSystem` when `getEnableHooks()` is true ([packages/core/src/config/config.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/config/config.ts)).
2. `hooksConfig.disabled` is an array of hook **names** with `mergeStrategy: MergeStrategy.UNION` ([settingsSchema.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/cli/src/config/settingsSchema.ts)), and `HookRegistry.processHookDefinition` sets `enabled: !isDisabled` from it ([hookRegistry.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/hooks/hookRegistry.ts)). Because the strategy is UNION, a project-level file can add to the list and silence a named user-level hook. `/hooks disable-all` writes exactly that, to workspace scope when a workspace settings file exists ([packages/cli/src/ui/commands/hooksCommand.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/cli/src/ui/commands/hooksCommand.ts)). A handrail hook entry needs a stable, distinctive `name`, and that name is also the handle by which a project can kill it.
3. **An untrusted folder drops every settings-file hook, user-level included.** `processHooksFromConfig` in [hookRegistry.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/hooks/hookRegistry.ts) reads the *merged* settings hooks and then discards them all when the folder is untrusted, logging "Project hooks disabled because the folder is not trusted." This is the most consequential bypass for handrail: the user's own global guardrails vanish in precisely the repositories a user is least likely to trust. It bites by default, because folder trust defaults to enabled (`settings.security?.folderTrust?.enabled ?? true` in [packages/cli/src/config/trustedFolders.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/cli/src/config/trustedFolders.ts), and `security.folderTrust.enabled` default `true` in [settingsSchema.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/cli/src/config/settingsSchema.ts)) and `isTrustedFolder()` falls back to untrusted when the feature is on and no trust rule matches ([config.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/config/config.ts): `return this.folderTrust ? false : true;`). Note that [docs/cli/trusted-folders.md](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/docs/cli/trusted-folders.md) still says the feature is "**disabled by default**", which the schema contradicts.
4. `GEMINI_CLI_TRUST_WORKSPACE=false` forces untrusted from the environment, short-circuiting everything else in `checkPathTrust` ([packages/core/src/utils/trust.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/utils/trust.ts)), and therefore triggers bypass 3. The same variable set to `true` forces trusted, which is a bypass of the trust prompt rather than of hooks.
5. `yolo` mode installs an allow-all policy rule, but it defeats neither hooks (section 3: the `BeforeTool` hook runs before the policy check) nor user-tier promoted rules (section 8: 1.998 loses to 4.x). It is a bypass only for default-tier and extension-tier policy.
6. Trust file and IDE signal: `~/.gemini/trustedFolders.json` with `DO_NOT_TRUST`, or an IDE reporting an untrusted workspace, reach bypass 3 by the same route as 4 ([packages/core/src/utils/trust.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/utils/trust.ts); the file path itself is redirectable with `GEMINI_CLI_TRUSTED_FOLDERS_PATH`).

Gemini's own threat model, in [docs/hooks/best-practices.md](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/docs/hooks/best-practices.md), ranks hook sources: system safest, then user ("You are responsible for ensuring they are safe"), then extensions, then project, marked "**Untrusted by default.** Safest in trusted internal repos; higher risk in third-party/public repos." Project hooks are fingerprinted by `name` plus `command`, warned about once, then trusted; changing the `command` string re-triggers the warning.

## 10. Transcript access and Analyzer parity

Claim 6 is the one that needs correcting in section 12.

The transcript exists and is machine-readable, but three details are wrong or missing in the spec's phrasing:

- Format is **JSONL**, appended line by line ([packages/core/src/services/chatRecordingService.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/services/chatRecordingService.ts)), with legacy `.json` paths normalised by appending an `l`.
- The path is `~/.gemini/tmp/<projectShortId>/chats/session-<YYYY-MM-DDTHH-mm>-<first 8 chars of sessionId>.jsonl`. The project component is a slug allocated by a registry at `~/.gemini/projects.json` ([storage.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/config/storage.ts), `Storage.initialize`), migrated from an older hash-based scheme. Subagent transcripts nest one level deeper, under the parent session id, and are named `<sessionId>.jsonl`.
- **The transcript is not addressable by session id.** The filename truncates the session id to its first eight characters and prefixes a minute-resolution timestamp, so an Analyzer holding a full `session_id` must glob the chats directory rather than construct a path. handrail's Analyzer can rely on the `transcript_path` field it is handed, but any path it reconstructs itself needs a search.

`getConversationFilePath()` returns `string | null`, which is why `transcript_path` in the envelope can be `''`. An Analyzer on Gemini must handle the empty case rather than assume a path.

With those corrections, Analyzer parity is achievable: the data is there, in a line-delimited format, reachable from the hook payload.

## 11. Verdict on "no interface changes"

Section 12's conclusion does not survive, though the damage is narrow and lands on the capability matrix rather than on the Adapter's method set.

What survives: the event map, the blocking mechanism, hooks-on-by-default, the settings.json sync model, the stdin envelope, and adapter-internal work being confined to an event name map, tool-kind classification, a settings.json writer and a matrix row. Five of the six claims hold.

What does not:

1. **Context injection cannot be a global capability flag.** It is present on `SessionStart`, `BeforeAgent` and `AfterTool`, and absent on `BeforeTool` (section 4). Since `warn` is defined as injection with a documented degradation, the matrix must record injection per event or handrail will silently promise agent-visible warnings on `PreToolUse` that Gemini cannot deliver.
2. **Fail-open cannot be a single flag either.** Spawn failure and timeout fail open, non-zero exit with output fails closed, and the closed case covers every code except 1 (sections 3 and 5).

Both are Adapter-contract facts, which puts them squarely in T14's scope, and both are changes to `docs/spec.md` section 4's matrix shape, not just to section 12's prose. Section 12 should be rewritten to say that Gemini CLI needs no new Adapter *methods*, while the capability matrix gains per-event granularity for context injection and a split fail-open row.

Two smaller corrections for section 12: the transcript is JSONL and not session-id-addressable (section 10), and the "known bypasses" story is materially worse than on the v1 harnesses because an untrusted folder silently drops user-level hooks (section 9, bypass 3).

## What could not be verified

- Whether `AfterAgent` fires reliably enough to carry handrail's `Stop` outcomes. The code path exists with documented early returns, and [#27712](https://github.com/google-gemini/gemini-cli/issues/27712) reports it never firing from `settings.json`, but the issue is open and unconfirmed by a maintainer. Establishing this needs a live run against v0.56.0, which is out of scope for a documentation-and-source review.
- The same for `SessionStart` under startup, `/clear` and resume ([#28160](https://github.com/google-gemini/gemini-cli/issues/28160), open).
- Whether `hooksConfig.disabled` matching is on the hook's `name` only or can be defeated by omitting `name` (in which case `getHookName` falls back to the `command` string, per [hookRegistry.ts](https://github.com/google-gemini/gemini-cli/blob/30573d2e4d85bdc2c0ae8218c377cd410336da77/packages/core/src/hooks/hookRegistry.ts)). The fallback is visible in source; whether the CLI's `/hooks` surface exposes the same key for an unnamed hook was not traced.
- Windows behaviour of the hook subprocess (shell selection, quoting) was not examined; it belongs with T13.
