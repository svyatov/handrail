# Compaction and interruption have distinct rule uses

`PreCompact` and `Interrupt` join the event model for Codex. A `PreCompact` rule can stop history compaction before it discards context, selecting manual or automatic compaction with the envelope's `trigger`. An `Interrupt` warning can remind the human to inspect unfinished work after cancellation. Both events support human-only warnings; an interruption cannot be blocked. Other Adapters skip these events.

This revises ADR 0023's earlier scope decision. `PostCompact` remains outside the model because `SessionStart` already reinjects standing context after compaction. Codex now also permits a `SessionStart` block to stop the next model request, including a post-compaction continuation, so rejecting every such rule at validation would discard a supported control. Other Adapters degrade it to a warning.

The baseline is Codex 0.163.0 and its [hooks reference](https://learn.chatgpt.com/docs/hooks). Stop controls use `continue: false` and `stopReason`; they do not use a tool-denial decision. `SessionEnd` is advisory and discards output, so Handrail records matched rules but returns success without output on Codex.
