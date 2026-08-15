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
Where a rule lives and whom it applies to. Three tiers: Global (user-level, all projects), Project-shared (committed to the repo), Project-personal (local, gitignored).
_Avoid_: scope, level

**Event model**:
The harness-neutral set of lifecycle events rules are written against.

**Adapter**:
The per-harness layer that maps the event model and rule enforcement onto one specific harness.
_Avoid_: driver, backend, integration

**Advisor**:
The intelligence that recommends the cheapest sufficient enforcement mechanism for a rule, for example a native permission entry instead of a hook.
_Avoid_: suggestion engine, linter

**Action**:
What a matched rule does: warn or block (final set is an open decision).
