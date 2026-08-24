# Codex multi-agent v2: does a spawn reach hooks under a different tool name?

Date: 2026-08-24
Purpose: primary-source research for handrail issue #110 (wayfinder ticket T25), the one residue
T22 (#107) left open when it landed `agent_type` and `agent_prompt` by key presence and accepted
that `kind: agent` under-covers. T22's Q11 recorded the suspicion in one sentence: Codex's
multi-agent v2 uses a configurable spawn namespace, default `collaboration`, that its own
hook-name normalization does not cover.

It is correct. A v2 spawn fires `PreToolUse`, carries `agent_type` and `message` under exactly
the keys T22 chose, and arrives under a `tool_name` that is not `spawn_agent`.

## 0. What is pinned

All `openai/codex` claims below cite `file:line` at commit
**`4beea50e26dd45ae58c68ecaa6b98fa22078138a`** (2026-08-24, "Advertise shell snapshot v2 support
on Unix (#40376)"), the tip of `main` when this was written. Paths are relative to the repository
root, so `codex-rs/core/src/tools/registry.rs:796` means that file in that repository at that
commit.

Prior handrail research pinned `536f86e5cc9ec1ff38457d099bf320b9d08eeeba` (2026-08-21). **Nothing
that matters here drifted between the two.** `git diff 536f86e5 4beea50e` over
`codex-rs/core/src/tools/handlers/multi_agents_v2/`,
`codex-rs/core/src/tools/handlers/multi_agents_spec.rs`, `codex-rs/core/src/tools/spec_plan.rs`,
`codex-rs/core/src/tools/hook_names.rs` and `codex-rs/models-manager/models.json` is empty.
`codex-rs/core/src/config/mod.rs` and `codex-rs/features/src/lib.rs` did change, but neither diff
touches a `multi_agent` line. In particular `function_hook_tool_name` is byte-identical at both
commits, so the issue's `registry.rs:797-802` citation still lands on the right lines.

Codex documentation claims cite `https://learn.chatgpt.com/docs/hooks`, which is where
`https://developers.openai.com/codex/hooks` 308-redirects.

Artifact corroboration where noted comes from the locally installed `codex-cli 0.149.0`
(`/opt/homebrew/bin/codex`). Source at the pinned SHA is the record; the binary only shows that
the source in question ships.

## 1. The namespace is real, configurable, and default-on for v2

### The config key

`features.multi_agent_v2.tool_namespace`, default `"collaboration"`.

- The constant: `codex-rs/core/src/config/mod.rs:230`,
  `const DEFAULT_MULTI_AGENT_V2_TOOL_NAMESPACE: &str = "collaboration";`
- The resolved field: `codex-rs/core/src/config/mod.rs:1261`, `pub tool_namespace: Option<String>`
  on `MultiAgentV2Config`.
- The default: `codex-rs/core/src/config/mod.rs:1280`,
  `tool_namespace: Some(DEFAULT_MULTI_AGENT_V2_TOOL_NAMESPACE.to_string())`. The default is
  `Some`, not `None`, so the namespace applies unless a user writes a different one.
- The TOML surface: `codex-rs/features/src/feature_configs.rs:265-267`, `pub tool_namespace:
  Option<String>` on `MultiAgentV2ConfigToml`, schema-annotated
  `#[schemars(length(min = 1, max = 64), regex(pattern = r"^[a-zA-Z0-9_-]+$"))]`.
- The merge: `codex-rs/core/src/config/mod.rs:2717-2720`, user value or the default.
- The validation: `codex-rs/core/src/config/mod.rs:3039-3099`. Non-empty, no surrounding
  whitespace, `^[a-zA-Z0-9_-]+$`, at most 64 characters, and not one of a reserved list
  (`api_tool`, `browser`, `computer`, `container`, `file_search`, `functions`, `image_gen`,
  `multi_tool_use`, `python`, `python_user_visible`, `submodel_delegator`, `terminal`,
  `tool_search`, `web`) nor `mcp` nor anything starting `mcp__`.

The regex is worth reading twice: a namespace may not contain a dot, a colon, or any other
separator. Whatever separator the reader expects between namespace and tool name, the config
layer forbids the user from supplying one.

The key string `features.multi_agent_v2.tool_namespace` is present verbatim in the shipped
`codex-cli 0.149.0` binary, so this is released behaviour and not unreleased `main`.

### The code path from config to wire name

1. `codex-rs/core/src/tools/spec_plan.rs:1118-1176`, `add_collaboration_tools`. When
   `multi_agent_v2_enabled(turn_context)` (`spec_plan.rs:593-595`, true iff
   `turn_context.multi_agent_version == MultiAgentVersion::V2`), the six v2 handlers are
   registered.
2. `codex-rs/core/src/tools/spec_plan.rs:1127-1129` reads the namespace:
   `namespace_tools_enabled(turn_context).then_some(turn_context.config.multi_agent_v2.tool_namespace.as_deref()).flatten()`.
   `namespace_tools_enabled` is `turn_context.provider.capabilities().namespace_tools`
   (`spec_plan.rs:589-591`), which defaults to `true`
   (`codex-rs/model-provider/src/provider.rs:60,70`).
3. `codex-rs/core/src/tools/spec_plan.rs:1287-1298`, `multi_agent_v2_handler` wraps each handler
   in `MultiAgentV2NamespaceOverride` when a namespace is present, and returns the handler bare
   when it is not.
4. `codex-rs/core/src/tools/spec_plan.rs:1305-1308`, the wrapper's `tool_name`:
   `ToolName::namespaced(self.namespace.clone(), self.handler.tool_name().name)`. The inner
   handler's own name is `ToolName::plain("spawn_agent")`
   (`codex-rs/core/src/tools/handlers/multi_agents_v2/spawn.rs:32-34`).
5. The spec the model sees becomes a `ToolSpec::Namespace` named after the namespace
   (`spec_plan.rs:1310-1319`).
6. The model calls it back as a `function_call` item carrying `name` and `namespace` as two
   separate fields. Verbatim from a protocol round-trip test,
   `codex-rs/protocol/src/models.rs:3174-3182`:

   ```json
   {
     "type": "function_call",
     "name": "spawn_agent",
     "namespace": "collaboration",
     "arguments": "{\"message\":\"hello\",\"task_name\":\"worker\"}",
     "encrypted_function_args": [],
     "call_id": "call-1"
   }
   ```

   The integration-test helper builds the same shape
   (`codex-rs/core/tests/common/responses.rs:944-960`), and every v2 test in the suite drives a
   spawn through it (for example `codex-rs/core/tests/suite/subagent_notifications.rs:1155-1160`).
7. `codex-rs/core/src/tools/router.rs:158` reconstructs the pair:
   `ToolName::new(namespace, name).with_default_namespace()`, giving
   `ToolName { name: "spawn_agent", namespace: Some("collaboration") }`.

### The hook name that results

`codex-rs/core/src/tools/registry.rs:796-805`, unchanged since `536f86e5`:

```rust
fn function_hook_tool_name(invocation: &ToolInvocation) -> HookToolName {
    if invocation.tool_name.name == "spawn_agent"
        && (invocation.tool_name.is_default_namespace()
            || invocation.tool_name.namespace.as_deref() == Some(MULTI_AGENT_V1_NAMESPACE))
    {
        return HookToolName::spawn_agent();
    }

    HookToolName::new(flat_tool_name(&invocation.tool_name).into_owned())
}
```

`MULTI_AGENT_V1_NAMESPACE` is `"multi_agent_v1"`
(`codex-rs/core/src/tools/handlers/multi_agents_spec.rs:14`). `is_default_namespace()` is true
only for `None`, `""` and `"functions"` (`codex-rs/protocol/src/tool_name.rs:46-51`). A namespace
of `"collaboration"` is none of those, so the special case does not fire and the fallthrough runs.

`flat_tool_name` (`codex-rs/core/src/tools/mod.rs:39-53`) concatenates with **no separator**:

```rust
Some(namespace) => {
    let mut name = String::with_capacity(namespace.len() + tool_name.name.len());
    name.push_str(namespace);
    name.push_str(&tool_name.name);
    Cow::Owned(name)
}
```

`ToolName`'s own `Display` does the same, `write!(f, "{namespace}{}", self.name)`
(`codex-rs/protocol/src/tool_name.rs:58`).

**So the exact `tool_name` string on hook stdin, under the default config, is
`collaborationspawn_agent`.** Under a custom namespace `agents` it is `agentsspawn_agent`, and in
general `<namespace><tool>` with nothing between them.

That reads like a defect rather than a design, and it is worth saying exactly how well it is
evidenced. The concatenation itself is unambiguous in source. The only test that pins a flat name
for a namespaced non-MCP tool is `codex-rs/core/src/tools/registry_tests.rs:434-477`, which at
line 437 constructs `ToolName::namespaced("functions.", "echo")`, with the dot baked into the
namespace string, and asserts `HookToolName::new("functions.echo")`. MCP tools never reach this
code path:
they have their own hook-name function that inserts `__`
(`codex-rs/core/src/tools/handlers/mcp.rs:96-103`). **No test anywhere in the repository asserts
the hook `tool_name` for a namespaced v2 spawn.** Treat `collaborationspawn_agent` as
source-derived, not test-pinned.

Two escape hatches exist and neither is the common case:

- If the provider does not advertise `namespace_tools`, step 2 yields `None`, the handler is
  registered bare as `ToolName::plain("spawn_agent")`, `is_default_namespace()` is true, and the
  hook sees `spawn_agent` with the `Agent` matcher alias. The default capability is `true`
  (`codex-rs/model-provider/src/provider.rs:70`).
- A user may set `features.multi_agent_v2.tool_namespace` to any allowed string, which changes the
  prefix but never removes it: there is no configured value that produces a bare `spawn_agent`,
  because the empty string is rejected (`config/mod.rs:3062-3067`).

### The matcher consequence, which is worse than the name

`codex-rs/core/src/tools/hook_names.rs:41-51` gives `spawn_agent` the matcher alias `Agent`. The
fallthrough at `registry.rs:804` uses `HookToolName::new`, which sets
`matcher_aliases: Vec::new()` (`hook_names.rs:21-26`). So a v2 spawn carries **no aliases at all**:
neither `spawn_agent` nor `Agent`.

Codex matcher semantics then close the last door. `codex-rs/hooks/src/events/common.rs:137-173`:

```rust
Some(matcher) if is_exact_matcher(matcher) => input
    .map(|input| matcher.split('|').any(|candidate| candidate == input))
    .unwrap_or(false),
Some(matcher) => input
    .and_then(|input| {
        regex::Regex::new(matcher)
            .ok()
            .map(|regex| regex.is_match(input))
    })
```

with

```rust
fn is_exact_matcher(matcher: &str) -> bool {
    matcher
        .chars()
        .all(|ch| ch.is_ascii_alphanumeric() || ch == '_' || ch == '|')
}
```

`spawn_agent` is alphanumerics and underscores, so it is an **exact** matcher, not a regex. Exact
equality against `collaborationspawn_agent` fails. The natural hook config, `matcher:
"spawn_agent"`, does not fire on a v2 spawn. A matcher containing any character outside
`[A-Za-z0-9_|]` falls to the regex branch and `Regex::is_match` is unanchored, so
`matcher: "spawn_agent$"` or `matcher: ".*spawn_agent"` would fire. Nobody writes that by accident.

## 2. Yes, a v2 spawn fires `PreToolUse`

It is not in T5's G4 class. The hosted network tools G4 found are spec-only variants with no
registered handler: `create_web_search_tool` returns `ToolSpec::WebSearch { .. }`
(`codex-rs/core/src/tools/hosted_spec.rs:14-32`) and no `WebSearchHandler` exists anywhere in
`codex-rs/core/src`. The vendor says the same: "Hosted tools, such as `WebSearch`" do not use the
local function-tool hook path (`https://learn.chatgpt.com/docs/hooks`).

The v2 spawn is the opposite in every respect:

- It is a real handler registered into the `ToolRegistry`,
  `registry.register_trusted_with_exposure(multi_agent_v2_handler(SpawnAgentHandlerV2::new(...)),
  exposure)` at `codex-rs/core/src/tools/spec_plan.rs:1134-1151`.
- Its payload kind is `Function`: `matches!(payload, ToolPayload::Function { .. })` at
  `codex-rs/core/src/tools/handlers/multi_agents_v2/spawn.rs:264-268`.
- The default `pre_tool_use_payload` returns `Some` for any function payload,
  `codex-rs/core/src/tools/registry.rs:129-138`.
- The namespace wrapper does not override it. `MultiAgentV2NamespaceOverride`
  (`spec_plan.rs:1300-1352`) implements `tool_name`, `spec`, `exposure`,
  `supports_parallel_tool_calls`, `search_info`, `handle`, `wait_until_ready`, `matches_kind`
  and `create_diff_consumer`, and nothing else. `pre_tool_use_payload` and
  `post_tool_use_payload` fall through to the trait defaults, which is precisely how the
  namespaced name reaches the hook.
- Registry dispatch runs the hooks: `codex-rs/core/src/tools/registry.rs:566-616`, `if let
  Some(pre_tool_use_payload) = tool.pre_tool_use_payload(&invocation) { match
  run_pre_tool_use_hooks(...)`, with `PreToolUseHookResult::Blocked` short-circuiting into
  `FunctionCallError::RespondToModel`.
- `run_pre_tool_use_hooks` serializes `tool_name: tool_name.name().to_string()` and
  `matcher_aliases: tool_name.matcher_aliases().to_vec()`
  (`codex-rs/core/src/hook_runtime.rs:174-192`).

The default exposure does not hide the tool either: `non_code_mode_only` defaults to `true`
(`config/mod.rs:1284`), which selects `ToolExposure::DirectModelOnly`
(`spec_plan.rs:1122-1126`), documented as "Include this tool in the initial model-visible tool
list only" (`codex-rs/tools/src/tool_executor.rs:68-72`). It is a directly offered tool.

So the hook fires, it is blockable, and it is blockable through the same code path as any other
function tool. The gap is entirely the name it fires under.

## 3. The v2 tool input carries `agent_type` and `message`, unchanged

`codex-rs/core/src/tools/handlers/multi_agents_v2/spawn.rs:270-281`, verbatim:

```rust
#[derive(Debug, Deserialize)]
#[serde(deny_unknown_fields)]
struct SpawnAgentArgs {
    message: String,
    task_name: String,
    agent_type: Option<String>,
    model: Option<String>,
    reasoning_effort: Option<ReasoningEffort>,
    service_tier: Option<String>,
    fork_turns: Option<String>,
    fork_context: Option<bool>,
}
```

Those are the exact JSON key names, and `deny_unknown_fields` means they are the only ones a v2
spawn will accept. `fork_context` is accepted by the deserializer only to be rejected with a
message telling the model to use `fork_turns`
(`multi_agents_v2/spawn.rs:284-289`).

The advertised schema is narrower than the struct. `create_spawn_agent_tool_v2`
(`codex-rs/core/src/tools/handlers/multi_agents_spec.rs:102-146`) declares
`required: ["task_name", "message"]` (line 139) and prunes optional properties:

- `agent_type` is removed unless `expose_agent_type` (lines 110-112), which is
  `!turn_context.config.agent_roles.is_empty()` (`spec_plan.rs:1139`). No configured agent roles,
  no `agent_type` in the schema, so the model does not send it. This is identical to v1
  (`codex-rs/core/tests/suite/spawn_agent_description.rs:280`, "v1 hides agent type without
  roles"), so it is not a v2 regression.
- `service_tier` is removed when `hide_spawn_agent_metadata` (lines 113-115), default `true`
  (`config/mod.rs:1281`).
- `model` and `reasoning_effort` are removed unless `expose_spawn_agent_model_overrides` (lines
  116-119), default `true` (`config/mod.rs:1282`).

Compare v1, `codex-rs/core/src/tools/handlers/multi_agents/spawn.rs:230-239`:

```rust
struct SpawnAgentArgs {
    message: Option<String>,
    items: Option<Vec<UserInput>>,
    agent_type: Option<String>,
    model: Option<String>,
    reasoning_effort: Option<ReasoningEffort>,
    service_tier: Option<String>,
    #[serde(default)]
    fork_context: bool,
}
```

The two differ in four ways, none of which touches the two keys handrail reads: v2 makes `message`
required rather than optional, adds a required `task_name`, drops `items`, and swaps
`fork_context` for `fork_turns`.

**This is the decisive answer.** T22's key-presence reading of `agent_type` and of `message` for
`agent_prompt` already covers a v2 spawn, on the same keys, with no Adapter change. The only thing
v2 breaks is name-based classification.

The v2 namespace covers six tools, all named plainly and all wrapped identically:
`spawn_agent`, `send_message`, `followup_task`, `wait_agent`, `interrupt_agent`, `list_agents`
(`codex-rs/core/src/tools/handlers/multi_agents_v2/{spawn,send_message,followup_task,wait,interrupt_agent,list_agents}.rs`,
`ToolName::plain(...)` at lines 33, 12, 12, 24, 10 and 10 respectively). Every one of them reaches
hooks with the namespace glued on.

## 4. v2 replaces v1 per turn, and the feature flag is not the only way in

### They do not coexist

`add_collaboration_tools` is a single `if`/`else`
(`codex-rs/core/src/tools/spec_plan.rs:1118-1201`). The v2 branch registers the six v2 handlers;
the `else` branch registers `SpawnAgentHandler` (v1), `SendInputHandler`, `ResumeAgentHandler`,
`WaitAgentHandler` and the rest. There is no configuration in which both a v1 and a v2
`spawn_agent` are offered in the same turn. v1's handler names itself
`ToolName::namespaced(MULTI_AGENT_V1_NAMESPACE, "spawn_agent")`
(`codex-rs/core/src/tools/handlers/multi_agents/spawn.rs:24-26`), which is exactly the case
`function_hook_tool_name` special-cases back to `spawn_agent`.

The whole block is gated on `collab_tools_enabled` (`spec_plan.rs:597-609`), which returns `false`
for `MultiAgentVersion::Disabled`, and for `V2` additionally requires that the session is the root
agent or that the model's own catalog entry is `v2`.

### The flag defaults to off

`codex-rs/features/src/lib.rs:1130-1135`:

```rust
FeatureSpec {
    id: Feature::MultiAgentV2,
    key: "multi_agent_v2",
    stage: Stage::Stable,
    default_enabled: false,
},
```

v1's gate, immediately above at `lib.rs:1124-1129`, is `Feature::Collab`, key `multi_agent`,
`Stage::Stable`, `default_enabled: true`. So the config-flag answer alone is: v1 by default, v2
behind `features.multi_agent_v2 = true`.

### But the model catalog can select v2 with the flag off

`codex-rs/core/src/config/mod.rs:1507-1533`:

```rust
pub(crate) fn multi_agent_version_override(&self) -> Option<MultiAgentVersion> {
    if self.features.enabled(Feature::MultiAgentV2) {
        Some(MultiAgentVersion::V2)
    } else if !self.agents_enabled {
        Some(MultiAgentVersion::Disabled)
    } else {
        None
    }
}

pub(crate) fn multi_agent_version_for_model(
    &self,
    model_multi_agent_version: Option<MultiAgentVersion>,
) -> MultiAgentVersion {
    self.multi_agent_version_override()
        .or(model_multi_agent_version)
        .unwrap_or_else(|| self.multi_agent_version_from_features())
}
```

The flag is an override, and when it is off the **model's own catalog entry** decides. Models carry
`multi_agent_version: Option<MultiAgentVersion>`
(`codex-rs/protocol/src/openai_models.rs:260,478`), and the bundled catalog already sets it:

| Model (`codex-rs/models-manager/models.json`) | `multi_agent_version` | `visibility` |
| :--- | :--- | :--- |
| `gpt-5.6-sol` (line 21) | `v2` | `list` |
| `gpt-5.6-terra` (line 152) | `v2` | `list` |
| `gpt-5.6-luna` (line 278) | `v1` | `list` |
| `gpt-daybreak-blue-latest` (line 400) | `v2` | `hide` |
| `gpt-daybreak-red-latest` (line 518) | `v2` | `hide` |
| `gpt-5.5` (line 630) | `null` | `list` |
| `gpt-5.4` (746), `gpt-5.4-mini` (863), `gpt-5.2` (975) | `null` | mixed |
| `codex-auto-review` (line 1083) | `v1` | `hide` |

Two of those v2 models are user-selectable (`visibility: list`). **Selecting `gpt-5.6-sol` or
`gpt-5.6-terra` puts a session on multi-agent v2 with `features.multi_agent_v2` untouched.** The
question "is v2 behind a flag defaulting to off" has a true answer of "yes" and a misleading one:
the flag is off by default and the feature still reaches a default-configuration user by way of
the model they pick.

`codex-cli 0.149.0` contains the v2 spawn schema string "Task name for the new agent. Use
lowercase letters, digits, and underscores." verbatim, so the v2 tool ships in the released
binary.

## 5. What this means for handrail

Stated as facts, with the judgement left to the ticket:

1. A `kind: agent` rule cannot reach a Codex v2 spawn through any name-based classifier that does
   not know the namespace, because the name is `collaborationspawn_agent` by default and
   `<user string>spawn_agent` in general. A classifier keyed on an exact string cannot enumerate
   the general case; the user's namespace is arbitrary within `^[a-zA-Z0-9_-]{1,64}$`. A suffix
   match on `spawn_agent` is the only shape that covers both, and it is the shape T5 and ADR 0013
   have so far avoided.
2. `agent_type` and `agent_prompt` are unaffected. The keys are `agent_type` and `message`, the
   same as v1, and T22's key-presence reading is ungated by kind precisely so this case works.
   The field is the durable path; this ticket is the first live proof of it rather than a
   hypothetical.
3. Neither `agent_type` nor `kind: agent` is implemented on handrail's `main` today.
   `internal/harness/harness.go:145` `classify` returns five kinds and none of them is `agent`;
   `internal/rule/rule.go:85` and `:95` close the kind and field vocabularies without them; and
   `docs/spec.md:95-112`'s capability matrix has no agent row. So everything above lands on
   unwritten code, which is the cheapest possible moment for it to land.
4. The gap is not observable to a rule author. Codex's own matcher is exact for an
   alphanumeric-and-underscore pattern (`codex-rs/hooks/src/events/common.rs:169-173`), so a
   handrail-recommended native hook entry of `matcher: "spawn_agent"` would also silently miss a
   v2 spawn. That is a second surface, distinct from `kind`, and it is on the relay side.

## 6. What could not be established from a primary source

- **Whether the unseparated concatenation is intentional.** `flat_tool_name`
  (`codex-rs/core/src/tools/mod.rs:44-50`) and `ToolName::Display`
  (`codex-rs/protocol/src/tool_name.rs:58`) agree, so it is deliberate at the level of "two
  functions were written the same way", but no comment, test, or document says what separator a
  namespaced flat name is supposed to have. The `functions.` test namespace at
  `registry_tests.rs:436` reads like a caller compensating for it. No upstream issue or commit
  message was consulted; this was established from source only.
- **The exact hook `tool_name` for a v2 spawn as observed behaviour.** No test in `openai/codex`
  asserts it, and no live Codex session was run against a hook to confirm it. The string
  `collaborationspawn_agent` is derived by reading `router.rs:158`, `registry.rs:796-805` and
  `tools/mod.rs:39-53` in sequence. An empirical check is one `codex` session with a logging
  `PreToolUse` hook and a v2-capable model, and it would settle this outright.
- **Whether Codex intends the v1 special case to be extended.** `function_hook_tool_name` names
  `MULTI_AGENT_V1_NAMESPACE` explicitly and `DEFAULT_MULTI_AGENT_V2_TOOL_NAMESPACE` not at all.
  Whether that is an oversight or a decision is not stated anywhere in the repository.
- **Which model is the live default.** The bundled `models.json` carries no `is_default` key, and
  `default_model_from_available` (`codex-rs/models-manager/src/manager.rs:597-604`) picks from a
  preset list assembled at runtime from a remote catalog. So "does a default-configuration user
  get v2" cannot be answered from source; only "two listed models select it" can.
- **Whether the published docs cover any of this.** `https://learn.chatgpt.com/docs/hooks`
  enumerates `Bash`, `apply_patch` (with `Edit` and `Write` aliases), `mcp__server__tool`, and
  local function tools "including `spawn_agent` and `update_plan`". It does not mention
  multi-agent v2, the `collaboration` namespace, namespaced tool names outside the `mcp__` prefix,
  or `features.multi_agent_v2` at all. `docs/config.md` in the repository does not document
  `features.multi_agent_v2` either. The feature is source- and artifact-established, undocumented.
- **What `agent_type` values a v2 spawn can carry.** Determined by `config.agent_roles`, which is
  per-repository agent definition files, so the value space is open by construction. This is the
  same answer T22's Q3 already gave and is recorded here only to confirm v2 did not narrow it.
