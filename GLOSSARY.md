# handrail

A cross-harness guardrail manager: users declare rules once, and handrail enforces them through each harness's native hooks, with a performance-critical Go CLI as the engine.

## Language

**Harness**:
An agentic coding tool that exposes lifecycle hooks. handrail supports two: Claude Code and Codex CLI.
_Avoid_: agent, IDE, editor, platform

**Hook**:
A harness-native mechanism that runs an external command on a lifecycle event. handrail's one enforcement path, never the rule itself.
_Avoid_: using "hook" to mean a handrail rule

**Rule**:
handrail's unit of user intent: a declarative guardrail evaluated against events and enforced through hooks.
_Avoid_: hook, policy, check

**Example**:
A call written into a rule file, stating whether that rule must match it. It proves what the rule means, never what happens to the call, and it never changes what the rule enforces.
_Avoid_: fixture, test

**Rule tier**:
Where a rule lives and whom it applies to. Three tiers: Global (user-level, all projects), Project-shared (committed to the repo), Project-personal (local, gitignored). Precedence is most specific wins: Global < Project-shared < Project-personal. A tier's identity is its supply rather than its path: a `.handrail/local/` that is tracked in git or reached through a symlink is read as Project-shared. Tiers are convenience layering and not a security boundary, with one guaranteed exception: a Project-shared rule never replaces a Global one. The general claim stands because Project-personal outranks both other tiers and anything that can write the working tree can write it.
_Avoid_: scope, level

**Project root**:
The directory a working directory's project tiers are discovered at and its Trust grant is keyed by: the repo root, or the working directory itself outside a repo, with symlinks resolved so that one project has one identity.

**Shadowing**:
A rule file in a higher-precedence tier replacing a same-named rule in a lower tier wholesale. There is no field-level merging; the shadowing file is the effective rule. Shadowing with a disabled rule is how an inherited rule is switched off, so the act has an inverse: removing the shadowing file restores the rule it replaced. One tier pair is excluded: a Project-shared rule may not shadow a Global one, and a collision drops the shared file rather than failing validation. An enabled shadow is also tested against the `match:` Examples of the rule it replaces, so a copy that has drifted from its original is reported.

**Effective ruleset**:
The rules that can fire in one working directory: every tier that applies, merged, with each shadowed rule replaced by its higher-tier namesake and each disabled rule dropped. `check` prints it annotated (spec section 6), and the Analyzer reads it.
_Avoid_: conflating with the rules that match one event, which is one event's match set rather than a ruleset

**Matcher**:
The selecting half of a rule: an event name, an optional tool kind, and a list of conditions.

**Condition**:
One entry of a matcher's condition list: one or more Terms, which OR together. Conditions AND together, so a matcher selects only when every one of them is satisfied. A condition written as a bare field test is a one-Term condition; `any:` is how a rule file writes more than one.

**Term**:
One field test: a single operator applied to one canonical payload field.
_Avoid_: condition, when the single field test is meant

**Candidate**:
One thing a call does, as a canonical field sees it: a command the program runs at one level of wrapping, a redirect to a file, a host, one path of a rename. A field holds one or more. A rule matches when some choice of one Candidate per field satisfies its conditions.
_Avoid_: value, when one of several readings of the same thing is meant

**Spelling**:
One way of writing a Candidate: a command's source text or its unquoted form, or one of the names a tool answers to. A Candidate meets a Term when any of its Spellings does.

**Wrapper**:
A program that runs, on this machine, a command named in its own arguments, such as `sudo`, `setsid` or `find -exec`. A rule reads the command through it. A nested shell is a Wrapper whose command is a script.
_Avoid_: runner

**Event model**:
The set of lifecycle events rules are written against, named as Claude Code names them. An event joins when a rule on it has a job no other event does; one a harness lacks degrades to skip there.

**Canonical payload**:
What an Adapter's normalization produces from one harness event: one or more payloads, one per edit a patch makes and, for a shell call, one per file its syntax names beside the shell payload, each carrying the event name, its Tool kind, and the canonical fields a Condition can address. An in-process value rather than an input format: no command reads one from stdin, and `raw.*` and `transcript_path` are shape the spec reserves rather than data the engine carries.
_Avoid_: envelope, wire format, conflating with the harness payload the Adapter reads

**Adapter**:
The per-harness layer that maps the event model and rule enforcement onto one specific harness.
_Avoid_: driver, backend, integration

**Analyzer**:
The LLM-side agent, shipped in each harness plugin, that on demand reads the current session's transcript and, where granted, the Decision log, and proposes new rules, amendments and retirements of existing ones, and conclusions to trials. Proposal-only: every rule needs per-rule user approval, a proposal that weakens a rule is approved alone, and every change is verified via check and test before it lands.

**Surveyor**:
The LLM-side agent, shipped in each harness plugin, that on demand reads the current repository's Repo signals and, when asked, the user's own instruction files, and proposes rules from them. Proposal-only: every rule needs per-rule user approval and is verified via check and test before it lands.

**Repo signal**:
A fact read from the repository that fixes every literal in a rule's matcher: a lockfile's name, a generated directory, a prohibition stated in the repo's prose. It never fixes the rule's action, its tier, or whether the rule should exist.
_Avoid_: heuristic, detection

**Action**:
What a matched rule does: warn (proceed, inject the rule's message into the agent's context, and tell the human the rule fired), ask (hand the call to the human, who sees the rule's message and approves or refuses it), or block (deny, with the rule's message as the reason to the agent and to the human). On Stop and SubagentStop a block keeps the agent working once, and a warn tells only the human. Allow is the absence of a match, not an action.
_Avoid_: defer, which is a harness's session-control value rather than an Action

**Trial**:
A Rule that is evaluated and recorded but delivers nothing: it contributes no Outcome, no message and no ordering slot, so a trial block never blocks and a trial warn never injects. Written `trial: true` on the Rule, or imposed on every Rule by the Enforcement state, it is how a Rule is watched before it is trusted, and it is deliberately not an Action, because the traffic it measures has to stay unaltered by the measuring.
_Avoid_: shadow (Shadowing is the tier mechanism), audit mode, dry run, calling it an Action or an Outcome

**Outcome**:
What evaluating one event against the Effective ruleset produces: allow, warn, ask, or block. Exactly four, ordered block > ask > warn > allow. An Outcome takes the strongest Action among the matched rules, so warn, ask and block are deliberately the Action's own values; allow is the absence of a match rather than an Action, and belongs to no rule. The order ranks whether the call proceeds, not how loudly: a warn beside an ask still delivers its message.
_Avoid_: result, verdict, calling one matched rule's Action the Outcome

**Tool kind**:
The canonical cross-harness classification of what one payload does, assigned by the Adapter: shell, file_edit, file_read, mcp, agent, network, other. A tool call usually has one kind; a shell call also yields a file_edit or file_read payload for each file its syntax names.
_Avoid_: tool type, tool category

**Capability matrix**:
What an Adapter declares its harness supports: which events exist, which can block, which can carry a human-visible message, context injection, transcript access, and fail-open behavior. Drives degradation.

**Degradation**:
Substituting the nearest action that keeps a rule's promise when one harness lacks a capability the other has: a block falls to warn, then skip; an ask rises to block, because its promise is that the call does not proceed without a human. Reported at sync time, never silently.

**Trust**:
handrail's own per-repo, path-once grant that lets a repo's Project-shared rules take effect. Distinct from any harness's workspace trust. Global and genuinely local Project-personal tiers never need it.
_Avoid_: conflating with harness workspace trust

**Enforcement state**:
Whether handrail enforces at all in one project: `enforce`, `trial`, or `off`, set by `handrail mode` and stored per Project root beside Trust, with a machine-wide fallback. The weaker of it and a Rule's own state applies, so it only ever loosens. Every SessionStart in a project that is not enforcing says so, because the state has no expiry and nothing else would.
_Avoid_: mode on its own, which reads as a harness permission mode; conflating with Trust, which gates one tier rather than the whole engine; calling it a kill switch, which promises a boundary it is not

**Decision log**:
handrail's own record of what it decided: one line per evaluation in which a Rule matched or handrail set `unreadable`, written where the user has granted the Project root, and wherever a Rule on trial matched. It holds handrail's reasoning rather than the agent's traffic, because the harness's own transcript already holds the calls, and it carries the payload unredacted, because that transcript already does too. The Analyzer reads it (spec section 7).
_Avoid_: audit log, transcript, conflating with the harness's own session file

**Sync**:
Installing handrail's one hook entry per event into each harness's user-level configuration, after validating the rules. Its output depends on no rule. Performed by the `sync` command.

**Importer**:
The one-shot `import hookify` command that converts upstream hookify rule files into Project-personal rules, skipping and reporting anything the rule format cannot express.
_Avoid_: migrator, conflating with Sync
