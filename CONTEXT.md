# handrail

A cross-harness guardrail manager: users declare rules once, and handrail enforces them through each harness's native hook or permission mechanisms, with a performance-critical Go CLI as the engine.

## Language

**Harness**:
An agentic coding tool that exposes lifecycle hooks or native guardrail mechanisms (Claude Code, Codex CLI, ...). Primary target: Claude Code.
_Avoid_: agent, IDE, editor, platform

**Hook**:
A harness-native mechanism that runs an external command on a lifecycle event. One possible enforcement backend for a rule, never the rule itself.
_Avoid_: using "hook" to mean a handrail rule

**Rule**:
handrail's unit of user intent: a declarative guardrail evaluated against events, enforced via hooks or native mechanisms.
_Avoid_: hook, policy, check

**Rule tier**:
Where a rule lives and whom it applies to. Three tiers: Global (user-level, all projects), Project-shared (committed to the repo), Project-personal (local, gitignored). Precedence is most specific wins: Global < Project-shared < Project-personal. Tiers are convenience layering, not a security boundary.
_Avoid_: scope, level

**Shadowing**:
A rule file in a higher-precedence tier replacing a same-named rule in a lower tier wholesale. There is no field-level merging; the shadowing file is the effective rule.

**Effective ruleset**:
The rules that can fire in one working directory: every tier that applies, merged, with each shadowed rule replaced by its higher-tier namesake and each disabled rule dropped. `check` prints it annotated with what was replaced or disabled, and the Analyzer reads it.
_Avoid_: conflating with the rules that match one event, which is one event's match set rather than a ruleset

**Matcher**:
The selecting half of a rule: an event name, an optional tool kind, and a list of conditions.

**Condition**:
One field test inside a matcher: a single operator applied to one canonical payload field. Conditions AND together; a one-level any group expresses OR.

**Event model**:
The harness-neutral set of lifecycle events rules are written against.

**Adapter**:
The per-harness layer that maps the event model and rule enforcement onto one specific harness.
_Avoid_: driver, backend, integration

**Advisor**:
The CLI-resident intelligence that recommends promoting a rule to a harness-native mechanism (for example a Claude Code permission deny) when the matcher translates exactly. Recommend-only: accepted entries become the user's own harness config, and the rule stays active as the message-bearing layer.
_Avoid_: suggestion engine, linter

**Analyzer**:
The LLM-side agent, shipped in each harness plugin, that reads the current session's transcript on demand and proposes new rules for behaviors worth preventing. Proposal-only: every rule needs per-rule user approval and is verified via check and test before it lands.
_Avoid_: conflating with the Advisor

**Action**:
What a matched rule does: warn (proceed, but inject the rule's message into the agent's context) or block (deny, with the rule's message as the reason). Allow is the absence of a match, not an action.

**Outcome**:
What evaluating one event against the Effective ruleset produces: allow, warn, or block. Exactly three, and allow is the absence of a match rather than an Action. Among several matched rules, one block makes the Outcome block.
_Avoid_: result, verdict, conflating with Action

**Tool kind**:
The canonical cross-harness classification of a tool, assigned by the Adapter: shell, file_edit, file_read, mcp, other.
_Avoid_: tool type, tool category

**Capability matrix**:
What an Adapter declares its harness supports: which events exist, which can block, context injection, transcript access, and fail-open behavior. Drives degradation.

**Degradation**:
Substituting the strongest available action when a harness lacks a capability a rule needs (block, then warn, then skip), reported at sync time, never silently.

**Trust**:
handrail's own per-repo, path-once grant that lets a repo's Project-shared rules take effect. Distinct from any harness's workspace trust. Global and Project-personal tiers never need it.
_Avoid_: conflating with harness workspace trust

**Sync**:
Compiling rules into each harness's native configuration. Performed by the `sync` command.

**Importer**:
The one-shot `import hookify` command that converts upstream hookify rule files into Project-personal rules, skipping and reporting anything the rule format cannot express.
_Avoid_: migrator, conflating with Sync
