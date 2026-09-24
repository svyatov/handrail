# The event ring grows by use, and a block on Stop means continue

With two harnesses, every Codex event is also a Claude Code event, so eleven events are shared. An event joins handrail's model only when a rule on it has a job that no event already in the model does. That admits `SubagentStart` and `SubagentStop` and nothing else, so the model goes from six events to eight. Completeness would have admitted `PreCompact` and `PermissionRequest`, where no rule has a job. `PostCompact` was the strongest candidate and fails the test. Both harnesses fire `SessionStart` again after a compaction and inject its context, so `SessionStart` rules already survive one.

An event one harness lacks may join, degrading to skip there. This reverses the part of ADR 0002 that rejected harness-only events. The model-switch events are the first to test it, and they fail on use: they guard the human's own switch, which Claude Code's `availableModels` already restricts on every path, subagents included. A `model` field exists since ADR 0013's later amendment, and it did not change this.

`SubagentStart` takes `warn` only: neither harness can refuse a spawn, so `block` on it is a validation error. It is the one documented way standing context reaches a subagent, since a `SessionStart` rule does not.

On `Stop` and `SubagentStop`, any message to the agent makes it continue, on both harnesses. So a `block` there means "not done: continue once", and handrail stops evaluating block rules once the harness reports `stop_hook_active`. Claude Code caps a loop at eight blocks, and Codex has no cap. A rule author therefore never writes a loop guard. A `warn` there tells only the human, because a message to the agent is a continuation. `Stop` and `SubagentStop` are where the agent does not hear a warn, the exception to ADR 0019. A `Stop` or `SubagentStop` rule narrows with `response`, the agent's last message; without it, the rule fires at every turn end.

## Considered options

Restricting `Stop` to warn was rejected: continuing once is the rule's use ("run the tests before you finish"), and the Importer already maps hookify's `stop` rules onto it. Exposing `stop_hook_active` as a field was rejected, because it makes every author write the guard the engine can apply once. Mapping `SubagentStart` onto `SessionStart` was rejected, because every spawn would repeat the human notices the author never asked a subagent to carry.
