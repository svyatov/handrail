# Who hears a rule fire: the agent always, the human by default

A guardrail that fires, tells the agent, and leaves the human unaware is thin, because the agent is the party the rule exists to constrain. An injected or merely careless agent reads the warning and proceeds, and nothing on the human's screen says a rule matched. So a rule that fires reaches the human too, and its author may cut that back where the rule is coaching rather than alarm.

The default is visible rather than hidden because rules arrive from an incident someone just watched, and someone in that position wants to know when it happens again. The opt-out, `agent_only: true`, exists because the opposite failure is equally real: a message repeated at coaching frequency trains people to stop reading the channel, which costs more than it ever bought.

The two audiences read different text, and that split is what keeps the default affordable. The cost that argued for hiding warns was the length of agent-facing prose, not the fact of a notice. The rule body is addressed to the agent, so the human gets it only on a block, where the interruption is theirs and the body answers why. On an `ask` (ADR 0021) the human reads the body in the approval prompt, because they are the one deciding. On a warn the human gets one line naming the rule.

A block cannot opt out, and neither can an `ask`. A denial whose reason the human cannot see is a support ticket, and an `ask` without the human has nobody to ask.

`Stop` and `SubagentStop` are the one exception to "the agent always". On both harnesses any message that reaches the agent there makes it continue, so a warn on those events reaches only the human (ADR 0023), and `agent_only` on such a warn would reach nobody.

The channel is the harness's own user-visible field, `systemMessage` on both Claude Code and Codex, and it is a per-event capability rather than a global one: Claude Code discards the field on eleven events and on every asynchronous hook, so handrail's hook stays synchronous, and Codex accepts it on every event but `SessionEnd`. Where no channel exists the rule still enforces and the agent still hears it, and sync says which events cannot tell the human. The audience narrowing is reported rather than degraded, because degradation substitutes a weaker action and here the action is unchanged.

This is the general form of a rule ADR 0012 stated for handrail's own failures: a notice only the agent sees is no notice at all when the agent is what the rule exists to constrain. 0012 reached it from the fail-open side and stopped at handrail's own trouble; this reaches it from the delivery side and covers every rule.

## Considered options

Showing every warn to the human in full was rejected: the body is prose addressed to the agent, and at warn frequency it trains the reader to ignore the channel. Hiding warns from the human by default was rejected because rules arrive after an incident the human watched. A global capability flag for the human channel was rejected because Claude Code discards `systemMessage` on eleven events, so a global flag would drop the message silently there. Treating a narrowed audience as a degradation was rejected: degradation substitutes an action, and the action here is unchanged.
