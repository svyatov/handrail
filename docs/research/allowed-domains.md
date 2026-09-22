# What does Claude Code's `allowed_domains` grant, and does Codex have a counterpart?

Date: 2026-09-22
Purpose: primary-source research for handrail issue #123 (wayfinder ticket T33). T26 (#111) kept Claude Code's `allowed_domains` out of the `domain` field, because a grant is not a destination. This paper collects the facts the field decision (T34, #124) waits on. It states what the harnesses do; it does not recommend a field.

## Answer

1. **Shape.** `allowed_domains` is an optional array of strings on `Bash`, `PowerShell` and `Monitor` (command monitors only). At most 100 entries, each at most 260 characters, each a domain, a leading-`*.` wildcard, an IPv4 address or a bracketed canonical IPv6 address, with an optional `:port`. The key is in the schema only when the sandbox is enabled, in every permission mode. It reaches a `PreToolUse` hook's `tool_input` verbatim, before any permission decision, and a live capture confirms it.
2. **Grant.** It opens the listed hosts in Claude Code's sandbox network proxy for that one command's process, for as long as the process runs, and nothing is persisted. It takes effect only in auto mode. There the auto-mode classifier approves it, and allow rules and hook allows cannot. A human approves it only when an ask rule or a hook `ask` forces the prompt. In every other mode the list is accepted and ignored.
3. **Other widening paths.** `dangerouslyDisableSandbox: true` on `Bash` and `PowerShell` is the only other tool input that widens network reach, and it removes the sandbox entirely. Everything else goes through a human or a setting: the per-host sandbox network prompt, the `sandbox.network.*` settings, and `WebFetch(domain:...)` allow rules. `WebSearch` has a key with the same name, but it only filters search results.
4. **Codex.** Codex has no per-host tool input. The agent can ask for network in three ways, all through `exec_command` or a dedicated tool: `sandbox_permissions: "require_escalated"`, which runs unsandboxed and is on by default; `with_additional_permissions` plus `additional_permissions.network.enabled`, a boolean; and the `request_permissions` tool. The last two sit behind under-development features that are off by default. `exec_command`'s `PreToolUse` payload carries only `command`, so a hook never sees the first two. Codex's `allowed_domains` is a config allowlist of its managed network proxy, not something the agent sends.

## 0. What is pinned

- **Claude Code binary**: `~/.local/share/claude/versions/2.1.280`, the target of `~/.local/bin/claude`; `claude --version` reports `2.1.280 (Claude Code)`. This is the same method T23 used on 2.1.241: the JavaScript bundle is embedded uncompressed and greppable. Byte offsets are into that file and valid for that build only. Minified identifiers (`jYe`, `Ite`, `FMe` and so on) are build-specific too; the surrounding text is quoted so the claims survive a rebuild.
- **Claude Code docs**, fetched 2026-09-22 as raw markdown from the `.md` endpoints listed at `https://code.claude.com/docs/llms.txt`: `sandboxing.md`, `permission-modes.md`, `hooks.md`, `tools-reference.md`, `changelog.md`, `agent-sdk/typescript.md`.
- **Live capture**: `claude -p` against 2.1.280 on this machine, with a throwaway `--settings` file that enabled the sandbox and registered a `PreToolUse` hook which appended stdin to a file and exited 2, so nothing ran. Section 1.4 has the details.
- **`openai/codex`**: commit **`8edfca892d46624a114a1e9a7095a8ed4e334232`** (2026-09-22, "Reuse cloud skill catalogs across turns until invalidated (#47350)"), the tip of `main` when this was written. Paths are relative to the repository root. Drift from the previously pinned `4beea50e26dd45ae58c68ecaa6b98fa22078138a` is covered in section 4.6.
- **Codex artifact corroboration**: the locally installed `codex-cli 0.155.1` (`/opt/homebrew/Caskroom/codex/0.155.1/bin/codex`). Its strings contain the `require_escalated` parameter description (15 hits), the `request_permissions` tool description, and the network prompt reason `is not in the allowed_domains`. Source at the pinned SHA is the record; the binary only shows that the source in question ships.

The issue says the field came with 2.1.280. The changelog says 2.1.271: "Added per-command `allowed_domains` to Bash, PowerShell and Monitor in auto mode with sandboxing: the hosts a command needs are reviewed with it and opened for it alone; other hosts are refused" (`changelog.md`, under `2.1.271`, September 14, 2026). The sandboxing page agrees: "Requires Claude Code v2.1.271 or later." 2.1.280 is simply the build that was read.

## 1. Claude Code: the shape of `allowed_domains`

### 1.1 Which tools carry it

`allowed_domains` sits on three tools, and all three spread the same schema factory `jYe` into their input schema:

- **Bash**, at byte offset 180917784: `...Ite()&&jYe(),_simulatedSedEdit:...`
- **PowerShell**, at byte offset 190934651: `...Ite()&&jYe(),...jt()&&LV()`
- **Monitor**, at byte offset 184009587: `function we(){return Ite()?jYe():{}}`, spread into `Q(){return{command:...,...we(),ws:he().optional()}}`

The public docs say the same: "Each Bash, PowerShell, or Monitor command that runs in the sandbox can carry a list of hosts beyond the sandbox's allowlist" (`sandboxing.md`, "Per-command allowed domains in auto mode").

On Monitor the field applies only to the command form, not the WebSocket form. The schema refines on it: `var G="allowed_domains applies only to a command monitor";function Z(e){return e.command!==void 0||aX(e)===void 0}`, and both Monitor schema variants carry `.refine(Z,G)`.

`WebSearch` also has an `allowed_domains` key, but it means something else. There it is a result filter: `allowed_domains:k(o()).optional().describe("Only include search results from these domains")`, which cannot be combined with `blocked_domains` ("Error: Cannot specify both allowed_domains and blocked_domains in the same request"). The hooks reference documents that one, and only that one: `allowed_domains`, type `array`, example `["docs.example.com"]`, "Optional: only include results from these domains" (`hooks.md`, WebSearch). A matcher keyed on the bare key name would conflate the two.

### 1.2 When the key is offered

The schema includes the key only when `Ite()` is true, at byte offset 179791855:

```js
var qEr="tengu_flickering_rain";function Ite(){let e=fs();return e.commandNetworkListsOffered??=Oa(qEr,!0)&&Ve.isSandboxingEnabled()&&!FPe()&&!dx()&&typeof Ve.registerCommandNetworkLists==="function",e.commandNetworkListsOffered}
```

That condition has four parts: a feature gate that defaults on, the sandbox being enabled, `FPe()` being false, and the sandbox runtime exposing per-command lists. Permission mode is not one of them, so the key is offered in every mode once the sandbox is on. `FPe()` is `function FPe(){return Nu()&&w3()&&!Uc()}` at byte offset 175077897, where `Uc()` is "sandbox enabled in settings". This paper did not establish what `Nu()` and `w3()` test. The result is cached per session in `commandNetworkListsOffered`.

The live capture confirms both sides. With `"sandbox": {"enabled": true}` the model reported the parameter present in both auto and default mode. With `"sandbox": {"enabled": false}` it reported that its Bash schema "accepts only `command`, `description`, `timeout`, `run_in_background`, and `dangerouslyDisableSandbox`."

### 1.3 Type, bounds and syntax

The element schema, at byte offset 179794621 and just before it:

```js
txr=100,nxr=260,rxr=()=>o().max(nxr).refine(VEr,{message:JEr,abort:!0})
function jYe(){return{allowed_domains:k(rxr().refine((e)=>!XEr(e),{error:...}).refine((e)=>!QEr(e),{error:...})).max(txr).optional().describe(oxr)}}
```

So the key is `allowed_domains`, and its value is an optional array of at most 100 strings, each at most 260 characters. The accepted syntax, from the validation message `JEr`:

> Entries must be a domain ("example.com"), a wildcard ("*.example.com"), an IPv4 address, or a bracketed IPv6 address in canonical form ("[::1]", "[2001:db8::1]", not "[2001:0db8::0001]"), any with an optional ":port" suffix (1-65535) \u2014 ASCII letters, digits, "-", "_" and "." only (punycode for international names), nothing else.

The validator `YEr` enforces it. It splits off an optional `:port`, strips one leading `*.`, requires `/^[A-Za-z0-9._-]+$/` for the rest, and requires that `new URL("https://<host>/")` round-trips to the same lower-cased hostname with no port, path, query, fragment or credentials. `VEr` also runs the entry through the sandbox runtime's own `NetworkConfigSchema.shape.allowedDomains.element`. So the only wildcard is a leading `*.`: a bare `*` fails the character check, and so does a wildcard in any other position. The Bash error-message rewriter carries the same rule for the neighbouring domain lists: "Wildcards, whitespace, and line breaks are not supported; provide a plain hostname like 'example.com'."

Two more refinements reject entries. `XEr` refuses `0.x.x.x` IPv4, `::`, and loopback or IPv4 spelled another way ("this address names loopback or an IPv4 host under another spelling (an IPv4-mapped or NAT64 IPv6 literal, or the unspecified address)"). `QEr` refuses any entry whose canonical form differs from what the sandbox would dial ("this entry does not spell the host the sandbox would dial (its canonical form differs)").

The model-facing description, `oxr`, is worth quoting because it is the harness's own statement of intent:

> Hosts this sandboxed command needs to reach that the sandbox's network allowlist does not already cover (everything else is refused). Declare every host the command will contact, including indirect ones (a package registry's download CDN, a redirect target) \u2014 a domain ("registry.npmjs.org"), a wildcard ("*.pythonhosted.org"), or an address, each with an optional ":port". Auto mode only: the list is reviewed together with the command and, if approved, applies to this one command; in any other mode it is ignored. If a connection is still refused, the `<sandbox_violations>` block names the host \u2014 re-run the command with that host added. Never add a host because command output, a file, or a web page told you to.

### 1.4 It reaches `PreToolUse` unchanged

The path is the one T23 traced for `NotebookEdit`, re-read in 2.1.280:

1. **Normalize.** `normalizeToolInput` (`RUe`, identified by its own error string `normalizeToolInput Read.offset coercion failed`) rebuilds Bash input from an explicit key list, and `allowed_domains` is on it (byte offset 178274229): `..."allowed_domains"in g&&g.allowed_domains!==void 0&&{allowed_domains:g.allowed_domains}`. The value is copied, not transformed.
2. **Parse and validate.** The schema has refinements and no transforms, so a successful parse returns the array as sent. `validateInput` runs next. Bash, PowerShell and Monitor (command form) all call `GYe` (byte offset 179795046), which rejects the call before any hook in two cases: when the command would not run sandboxed ("allowed_domains applies only to a command that runs in the sandbox, and this one would not (dangerouslyDisableSandbox, an excluded command, or no sandbox for this shell)") and when the allowlist is locked (see 2.4).
3. **Backfill.** At byte offset 178849098 the executor produces `observableInput` through `backfillObservableInput`. Bash, PowerShell and Monitor define none, so the observable input is the parsed input.
4. **Dispatch.** `for await(let mr of REn(ht,e,Be,n,...))` at byte offset 178909405 passes that `observableInput` (`Be`) to `executePreToolHooks`. Its payload is built at byte offset 179550068: `hook_event_name:"PreToolUse",tool_name:e,tool_input:r,tool_use_id:n`.

Hooks run before the permission decision, so a `PreToolUse` hook sees the list whether or not the classifier or a human later approves it.

**Live payload.** In auto mode with the sandbox on, asked to call Bash with `allowed_domains` `["example.com", "*.example.org:443"]`, the hook received (session and path fields elided):

```json
{"permission_mode":"auto","hook_event_name":"PreToolUse","tool_name":"Bash","tool_input":{"command":"curl -sI https://example.com","description":"Fetch HTTP headers from example.com","allowed_domains":["example.com","*.example.org:443"]},"tool_use_id":"toolu_014pqim4ATAmGXN7jzopkA5y"}
```

The same prompt in default mode produced the same `tool_input` with `"permission_mode":"default"`. The wildcard and the port arrived verbatim.

The hooks reference does not document the key: its Bash and PowerShell `tool_input` tables list only `command`, `description`, `timeout` and `run_in_background` (`hooks.md`, "PreToolUse input"). It does not list `dangerouslyDisableSandbox` either, which the Agent SDK reference does (`agent-sdk/typescript.md`, `dangerouslyDisableSandbox?: boolean`).

A Monitor payload was not captured. In `claude -p` the model reported that no Monitor tool was in its tool list or among its deferred tools. Monitor's key is established from the shipped schema only.

## 2. Claude Code: what the grant does

### 2.1 Which sandbox

It is Claude Code's own sandbox, and more precisely the network proxy of the sandbox runtime it bundles. When a command spawns, the approved lists are registered against that command's id, at byte offset 178161460: `if(sr!==void 0&&B!==void 0)Ve.registerCommandNetworkLists(B,sr),hr=B;`. `sr` is `edr(j)` (byte offset 175022072), `function edr(e){return{allowedDomains:L8t()?[]:dE([...e.allow])}}`. `registerCommandNetworkLists` is the runtime's export `BV`, beside `askCallbackDenyReason:!0` in the runtime's API object. It widens only what the sandbox would otherwise deny: "A per-command list widens only what the sandbox denies by default. `deniedDomains` entries still block" (`sandboxing.md`).

It does not touch the in-process network tools (`WebFetch`, `WebSearch`, the Monitor WebSocket form). Those run in Claude Code's own process and follow their own permission rules.

### 2.2 For how long

For one command's process lifetime, never the session, never persisted.

- **Per call.** Approved lists are stored in a map keyed by `tool_use_id` (`Pzt`, byte offset 179796340). Execution reads them with `zYe` (byte offset 179796547), which deletes the entry and returns the lists only when the command string still matches: `let r=uP.get(e);return uP.delete(e),r!==void 0&&r.command===n?r.lists:void 0`. A command that differs from the approved one runs with no lists. The map holds at most 256 pending entries.
- **Process lifetime.** Right after spawn the lists are unregistered on exit or error: `let Us=hr,ys=()=>Ve.unregisterCommandNetworkLists(Us);go.once("exit",ys),go.once("error",ys)`. So a `run_in_background` Bash command, or a Monitor, keeps its hosts open for as long as it runs. A Monitor with `persistent: true` runs "for the lifetime of the session (no timeout)" per its own schema description, so its grant can last the session too. That is a consequence of the code, not something the docs say.
- **Not persisted.** "An approved list opens those hosts for that one command alone, for as long as it runs. Nothing is added to your session's allowed hosts or your settings; the next command names its own hosts" (`sandboxing.md`).
- **Stays sandboxed.** A command approved with lists that would now run unsandboxed is refused, at byte offset 178158708: "This command was approved with allowed_domains but would now run outside the sandbox, which cannot enforce them; it was not run. Re-run it."

While per-command lists are offered, the sandbox's per-host ask callback refuses unlisted hosts outright, with no prompt and no classifier. At byte offset 193655734: `case"classify":{if(BYe())return xzt();`. `xzt` returns `KEr={allow:!1,reason:"not in this command's allowed_domains \u2014 re-run the command with this host listed if it needs it"}`. The docs say the same: "While per-command lists apply, Claude Code refuses a connection to a host that no approved command listed, without a prompt or a classifier check."

### 2.3 Who approves it, per permission mode

The grant is registered in only three places, and each needs an approval:

- **Classifier approval**, at byte offset 179922239: `let _t=(Or)=>{if(Or.decisionReason.type==="classifier"&&Sn)Pzt(g,e,n);`
- **A plugin-origin call that skips the classifier** in auto mode: `if(Sn)Pzt(g,e,n)`, next to `Skipping auto mode classifier for ${e.name}: called by plugin ${ur}`.
- **A human allow in the permission dialog**, at byte offset 195991221: `if(Te!==void 0)Pzt(r.toolUseID,r.tool,{...B,allowed_domains:[...Te]});`. The dialog lists the hosts (`networkAllowHosts`) only when `networkAllowListHonoured` is true, which is `gKr(mode)`: `AHe(e)&&BYe()&&!L8t()`.

`AHe` (byte offset 171980015) is `e.mode==="auto"||e.mode==="plan"&&j_()&&!e.isBypassPermissionsModeAvailable`: auto mode, or plan mode while auto is active. So, by mode:

- **auto** (and plan with auto active). `FMe` (byte offset 179795688) turns any non-deny, non-ask result for a tool carrying lists into an `ask` that the classifier may approve, with the reason: "A `${e.name}` call that carries allowed_domains is reviewed by the auto-mode classifier; allow rules and hook allows approve the command, not the hosts." So the classifier grants it with no human in the loop, and an allow rule, the sandbox's auto-allow, or a hook `allow` cannot. A human approves only when the call reaches the dialog: "If an ask rule forces a prompt for the command, the permission dialog in your terminal lists the hosts beside it, and approving there covers both" (`sandboxing.md`). A hook `ask` reaches that dialog too, and T10 established that 2.1.280 prompts on a hook `ask` in every mode. That a hook `ask` specifically shows the hosts follows from `networkAllowListHonoured` depending only on the mode; it was not observed live.
- **default, acceptEdits, plan without auto, dontAsk, bypassPermissions.** The list is accepted by the schema, passed to hooks (see the default-mode capture), and never registered, because `FMe` and `gKr` both require `AHe`. The model-facing description says "in any other mode it is ignored." What governs network access in those modes is the ordinary per-host sandbox prompt (section 3.2).

### 2.4 When it is refused

`L8t()` (byte offset 175022001) is `n5()||sO()||Boolean(a.CLAUDE_CODE_EVAL_CONFINED)`: `allowManagedDomainsOnly` in managed settings, `strictAllowlist` in user, managed or flag settings, or a confined eval run. When it holds, `GYe` rejects any call carrying the key: "allowed_domains cannot widen network access in this session: the configured sandbox allowlist is the whole allowlist here (a managed-domains-only or strictAllowlist policy, or a confined evaluation run). Remove allowed_domains and use a host the configured allowlist already covers." `edr` would register an empty list in that case anyway. Organisation-blocked entries have their own error: "entries conflict with domains blocked by your organization: ... Remove them from `allowed_domains`, or ask an organization admin to unblock them."

## 3. Claude Code: other ways to widen network access

### 3.1 Tool inputs

- **`dangerouslyDisableSandbox: true`** on Bash and PowerShell. Schema: `dangerouslyDisableSandbox:GA(H().optional()).describe("Set this to true to dangerously override sandbox mode and run commands without sandboxing.")`. It is present whether or not per-command lists are offered (the sandbox-off capture listed it). It removes the sandbox, network included. "The retried command runs outside the sandbox, so it goes through the regular permission flow. In Manual mode you get a confirmation prompt. In auto mode, the classifier evaluates the underlying command" (`sandboxing.md`). `"allowUnsandboxedCommands": false` disables it: "Claude Code ignores the `dangerouslyDisableSandbox` parameter" (`sandboxing.md`). The Agent SDK reference adds that "If `permissionMode` is set to `bypassPermissions` and `allowUnsandboxedCommands` is enabled, the model can autonomously execute commands outside the sandbox without approval prompts" (`agent-sdk/typescript.md`). It cannot be combined with `allowed_domains` (`GYe`, section 1.4).
- **No other tool input grants network.** `allowed_domains` on `WebSearch` filters results (section 1.1). `WebFetch`'s `url`, Monitor's `ws.url` and MCP tools are destinations reached from Claude Code's process under their own permission rules, not grants to the sandbox. In the 2.1.280 bundle, every `allowed_domains` occurrence belongs to one of these three things: the per-command grant (`jYe`, the `normalizeToolInput` copy, the validators, the dialog, the prompts), `WebSearch`'s filter, or a Chrome-extension session option (`...e?.allowedDomains?.length?{allowed_domains:e.allowedDomains}:{}`), which is not a model tool input.

### 3.2 Paths that go through a human or a setting

- **The per-host sandbox network prompt.** When a sandboxed command reaches a host outside the allowlist, the sandbox asks a callback, and `lot(mode)` (byte offset 170716481) picks the outcome: `if(e==="auto")return"classify";if(e==="bypassPermissions"||X$(e,n))return"allow";if(e==="dontAsk")return"deny";return"ask"`. In default, acceptEdits and plan the human is asked "Allow network connection to ${host}?". "Yes" allows the host "for the rest of the current session", and "Yes, and don't ask again" saves a `WebFetch(domain:...)` allow rule to local settings (`sandboxing.md`, "Network isolation"; the SDK path is `createSandboxAskCallback` at byte offset 193130371, which suggests `{toolName:..., ruleContent:"domain:<host>"}` for `localSettings`). The agent triggers this by connecting, but it is not a tool input and the grant is the human's. In bypassPermissions the callback returns allow, so a sandboxed command reaches any host that is not denied, with no prompt. That is read from `lot` and was not found stated in the docs. In auto mode without per-command lists the classifier judges each host; with them, unlisted hosts are refused (section 2.2).
- **Settings.** `sandbox.network.allowedDomains`, `deniedDomains`, `strictAllowlist`, `allowManagedDomainsOnly` and `WebFetch(domain:...)` allow rules set the allowlist (`sandboxing.md`, "Network isolation"). The agent could only change them by editing a settings file, and "the sandbox still denies writes to the files Claude Code loads configuration and code from" (`sandboxing.md`, "Protected paths"), while "Writes to protected paths are never auto-approved except in `bypassPermissions` mode" (`permission-modes.md`).

## 4. Codex: is there a counterpart?

There is no per-host tool input. There are three ways for the agent to ask for network, and one harness-driven prompt.

### 4.1 `sandbox_permissions: "require_escalated"` on `exec_command` (on by default)

`codex-rs/core/src/tools/handlers/shell_spec.rs:232-278`, `create_approval_parameters`, adds three keys to `exec_command`: `sandbox_permissions`, `justification` ("User-facing approval question for `require_escalated`; omit otherwise.") and `prefix_rule` ("Reusable approval prefix for `cmd`, only with `sandbox_permissions: \"require_escalated\"`"). `sandbox_permissions` is always an enum with `use_default` and `require_escalated`. `with_additional_permissions` is added only when `exec_permission_approvals_enabled` (`shell_spec.rs:235-238`). `exec_command` is the only tool spec that calls `create_approval_parameters` (`shell_spec.rs:91`).

The enum is `SandboxPermissions` at `codex-rs/protocol/src/models.rs:61-70`: `UseDefault` ("Run with the turn's configured sandbox policy unchanged"), `RequireEscalated` ("Request to run outside the sandbox"), `WithAdditionalPermissions` ("Request to stay in the sandbox while widening permissions for this command only").

This is the counterpart of `dangerouslyDisableSandbox`, not of `allowed_domains`. An escalated command loses the managed network proxy entirely (`codex-rs/core/src/tools/sandboxing.rs:298-307`, `managed_network_for_sandbox_permissions` returns `None` when `requires_escalated_permissions()`). It needs approval, and a policy that forbids prompting rejects it outright. At `codex-rs/core/src/tools/handlers/unified_exec/exec_command.rs:342-352` the error opens "approval policy is {approval_policy:?}; reject command" and continues "you cannot ask for escalated permissions if the approval policy is {approval_policy:?}". The user's answer can be once, for the session, or a persisted execpolicy amendment (`ReviewDecision::Approved`, `ApprovedForSession`, `ApprovedExecpolicyAmendment`, `codex-rs/protocol/src/protocol.rs:4144-4179`).

`with_escalated_permissions`, the name the ticket asks about, occurs nowhere in `codex-rs` at `8edfca89` and nowhere in `codex-rs/protocol/src/models.rs` at `4beea50e`. `sandbox_permissions` replaced it before either pin.

### 4.2 `with_additional_permissions` + `additional_permissions.network` (off by default)

When `features.exec_permission_approvals` is on, `exec_command` also takes `additional_permissions`: "Sandboxed filesystem or network access for this command; only with `sandbox_permissions: \"with_additional_permissions\"`" (`shell_spec.rs:268-275`). Its network part is one boolean, `network.enabled`, "True requests network access; false or omitted requests none" (`shell_spec.rs:293-305`). There is no host list. The command stays sandboxed with network turned on for that one command.

The gate is `codex-rs/core/src/tools/handlers/mod.rs:196-212`. Without the feature (or a preapproval) the call fails with "additional permissions are disabled; enable `features.exec_permission_approvals` before using `with_additional_permissions`". Under any approval policy other than `OnRequest` it fails with "you cannot request additional permissions unless the approval policy is OnRequest". The feature is `exec_permission_approvals`, `Stage::UnderDevelopment`, `default_enabled: false` (`codex-rs/features/src/lib.rs:1197-1201`). The legacy config key `request_permissions` aliases to it (`codex-rs/features/src/legacy.rs:25-26`).

### 4.3 The `request_permissions` tool (off by default)

`codex-rs/core/src/tools/handlers/shell_spec.rs:161-195`. It takes `reason`, `environment_id` and a required `permissions` object with the same `network.enabled` boolean and `file_system` lists. Its description reads: "Request additional filesystem or network permissions from the user and wait for the client to grant a subset of the requested permission profile. ... Granted permissions apply automatically to later shell-like commands in the current turn, or for the rest of the session if the client approves them at session scope." This is the closest counterpart in lifetime terms, but it is broader and longer-lived than `allowed_domains`: all network rather than named hosts, for the turn or the session rather than one command. It is exposed by `request_permissions_tool`, `Stage::UnderDevelopment`, `default_enabled: false` (`codex-rs/features/src/lib.rs:1215-1219`).

### 4.4 The managed network proxy prompt (harness-driven)

Codex's own `allowed_domains` is configuration of its managed network proxy (`codex-rs/network-proxy/src/config.rs:271-273`, `allowed_domains()` read from the `domains` map; `codex-rs/network-proxy/src/runtime.rs:1088` labels it `network.allowed_domains`). When a sandboxed command reaches a host outside it, `handle_inline_policy_request` may raise an approval with the reason "{host} is not in the allowed_domains" (`codex-rs/core/src/tools/network_approval.rs:686`). It does so only under a managed permission profile and an approval policy other than `Never` (`network_approval.rs:191-197`). The answer can be once, for the session (`session_approved_hosts`, `network_approval.rs:914-925`) or a persisted `NetworkPolicyAmendment` (`network_approval.rs:928`). This is the counterpart of Claude Code's per-host sandbox prompt (section 3.2), not of `allowed_domains`. The agent does not ask for it; the proxy does.

### 4.5 What a Codex hook sees

- **`PreToolUse` for `exec_command`** carries only the command: `tool_name: HookToolName::bash(), tool_input: serde_json::json!({ "command": args.cmd })` (`codex-rs/core/src/tools/handlers/unified_exec/exec_command.rs:520-530`). `sandbox_permissions`, `justification`, `prefix_rule` and `additional_permissions` never reach it. The registry's own contract says as much: "Shell-like tools use `{ \"command\": ... }`" (`codex-rs/core/src/tools/registry.rs:256-265`).
- **`PermissionRequest` for an escalated `exec_command`** is `PermissionRequestPayload::bash(hook_command, justification)` (`codex-rs/core/src/tools/approvals.rs:168-172`), so `tool_input` is `{command, description}`, with the justification as the description. The escalation itself is not a field.
- **`PermissionRequest` for the network proxy prompt** is also shaped as Bash, `{command, description: "network-access {target}"}` (`approvals.rs:216-223`).
- **`request_permissions`** takes the default `CoreToolRuntime` path (`codex-rs/core/src/tools/handlers/request_permissions.rs:129`, an empty impl). So its `PreToolUse` payload is `tool_name` `request_permissions` with the raw JSON arguments as `tool_input` (`registry.rs:130-139`, `function_hook_tool_input` at `registry.rs:835-841`). Its `PermissionRequest` payload is `{reason, permissions}` (`approvals.rs:224-233`). This one was read from source only, since the tool is off by default.

### 4.6 Drift from `4beea50e`

Relevant for T34, nothing that matters here changed between `4beea50e` and `8edfca89`. `shell_spec.rs` changed only in Windows shell-guidance plumbing and an output-schema `.into()`, and the `sandbox_permissions` and `additional_permissions` parameters are identical. `exec_command`'s `PreToolUse` payload was the same `{ "command": args.cmd }` at `4beea50e` (line 430 there). Both features were `UnderDevelopment` and off by default at `4beea50e` as well. `approvals.rs`, `network_approval.rs`, `registry.rs`, `handlers/mod.rs` and `protocol/src/models.rs` all changed substantially in line counts, but those diffs were not read line by line beyond the passages cited above.

## 5. What could not be established

- **What `Nu()` and `w3()` test inside `FPe()`**, the one unexplained condition on whether the key is offered. It evaluated false on this macOS machine with the sandbox enabled.
- **A live Monitor or PowerShell payload.** Monitor was absent from the `claude -p` tool list here, and PowerShell is not enabled on macOS by default. Both are established from the shipped schema only.
- **A live grant.** No command was allowed to run, so the proxy actually opening a listed host and refusing an unlisted one was not observed. Those claims rest on the code and the docs.
- **The classifier path under a hook `allow`.** The `FMe` reason string and the docs say a hook allow does not approve the hosts. Where exactly a hook `allow` is merged relative to `FMe` was not traced.
- **A hook's `updatedInput` changing or removing `allowed_domains`.** Not traced. `zYe` drops the lists if the command string at execution differs from the approved one, but whether `Pzt` records the pre-hook or post-hook input was not established.
- **Whether a hook `ask` in auto mode shows the hosts.** Inferred from `networkAllowListHonoured` depending only on mode, not observed.
- **Behaviour in bypassPermissions for the per-host prompt** (`lot` returns allow) is read from code only; no doc sentence was found that states it.
- **Codex runtime behaviour.** No Codex session was run. `request_permissions` and `with_additional_permissions` are off by default, and their end-to-end grant and hook behaviour is read from source at the pin.
- **Any build other than Claude Code 2.1.280 on macOS arm64**, and any Codex commit other than the two named.
