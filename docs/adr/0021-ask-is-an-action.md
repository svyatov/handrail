# ask is an Action, defer is not, and ask degrades up

A rule may hand a tool call to the human: `ask` joins `warn` and `block`, and the Outcome order is `block > ask > warn > allow`. It is the only Action whose decision is not the agent's. `warn` is advice an injected agent reads and ignores; `block` is total, so users either block what is only sometimes wrong and watch the agent route around it, or let it through with a note. `ask` is the gate between them, and the only one besides `block` that a hostile agent cannot argue past. Claude Code prompts on a hook `ask` in every permission mode, `bypassPermissions` and `dontAsk` included.

The order ranks the gate, not the volume. An `ask` a human approves can look weaker than a `warn` that always injects, but messages already travel separately from the Outcome, so a `warn` matched beside an `ask` still delivers, and on the question the Outcome answers, whether the call proceeds, `ask` is the stricter.

`ask` is valid only on `PreToolUse`, the one event whose subject a human can approve. The human reads the rule's body in the approval prompt, and the agent gets it as context, because a rejected `ask` otherwise tells the model nothing about the rule. `agent_only` on an `ask` rule is a validation error, since an `ask` without the human has nobody to ask.

ADR 0003 refused `ask` because degradation would make it unpredictable exactly where a user wanted a human gate. That holds for degrading down: an `ask` that becomes a `warn` on a harness without it runs the call with nobody's approval. So `ask` degrades up, to `block`. Codex cannot ask, and worse, fails open when told to; there an `ask` rule denies, and says why. The promise the user wrote, not without me, holds on both harnesses. Claude Code makes the same substitution itself when no human is present.

It is not a default. handrail decides nothing the user did not write (ADR 0012), so input it cannot read gets `ask` only from a rule that says so.

An `ask` is as strong as the harness's approval path. Any other PermissionRequest hook can answer it, a committed project settings file among them, which puts it in the same class as `disableAllHooks`: named, reported by `doctor`, and not a boundary.

## Considered options

`defer` was considered and refused. It suspends a headless session so an orchestrator can resume it, works only for a lone tool call, and is ignored interactively: a rule could not know whether it applied, and what it does is session control rather than a guardrail decision. Making `ask` the default for input handrail cannot read was rejected as handrail inventing a decision the user never wrote, only a softer one. Degrading `ask` down to `warn` on Codex was rejected because it runs the call with no human yes, the exact thing the rule exists to stop. Allowing `ask` on events other than `PreToolUse` was rejected: only a tool call can be approved, so it is invalid rather than degraded elsewhere. Recording the human's answer in the Decision log was rejected: it needs joining a later event to the call by `tool_use_id`, which is cross-event state on the hot path, and the answer is in the harness transcript.
