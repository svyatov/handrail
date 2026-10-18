# Approval requests have a distinct rule boundary

`PermissionRequest` joins the event model for Codex. Its native approval hook now exposes a decision point that `PreToolUse` cannot identify: a pending approval request, including a sandbox retry or a managed-network approval. A rule can refuse that request without refusing the same command when no approval is needed. This revises ADR 0023's earlier conclusion that the event had no distinct use.

The event carries ordinary tool fields and supports `block` and human-only `warn`. Handrail never emits an approval, and `ask` remains valid only on `PreToolUse`: an unmatched request follows Codex's normal approval flow. Request presence does not prove that a command is unsandboxed, and the hook does not supply the requested sandbox flags, so the Adapter does not invent `unsandboxed` or `network_grant` values.

The protocol baseline is Codex 0.163.0, checked against its release source and the [official hooks reference](https://learn.chatgpt.com/docs/hooks#permissionrequest). Other Adapters skip this event until they implement its native protocol.
