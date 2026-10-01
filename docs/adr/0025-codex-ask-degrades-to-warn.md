# Codex ask degrades to warn

Codex has no hook-supported `ask`, so converting it to `block` makes every matching call a denial with no approval path. An `ask` rule instead degrades to `warn` on Codex: the call proceeds and the warning is delivered, with the substitution reported by `sync` and `doctor` and preserved in the Decision log. This trades hook-enforced approval for usable cross-harness rules; a user who needs a denial on Codex must choose `block`.

The degraded rule uses normal warning output: the agent reads the full body labeled `handrail warn`, and the human sees one line naming the rule. The Decision log records the delivered `warn` with `degraded_from: ask`, preserving the rule's declared action without presenting an approval prompt that cannot be answered.

This supersedes the Codex degradation choice in [ADR 0021](0021-ask-is-an-action.md) and qualifies the rule-directory approval guarantee in [ADR 0020](0020-project-shared-is-add-only.md). Codex's rule-editing skills write approved changes after per-rule approval in chat, replacing the manual-copy fallback; that approval is a skill instruction, not a hook-enforced gate.
