// The installer prepends the ownership marker and JSON-encoded handrailBinary.
// OMP rebinds this factory for each session; never cache identity at module scope.
export default function handrail(pi) {
	process.env.HANDRAIL_OMP_SESSION = "1";

	async function notice(ctx, text) {
		if (!text) return;
		try {
			if (ctx.mode === "tui") {
				await ctx.ui.notify(text, "warning");
				return;
			}
		} catch {
			// A broken UI must neither erase a denial nor escape a tool handler.
		}
		try {
			await Bun.write(Bun.stderr, text + "\n");
		} catch {
			// There is no remaining human channel when stderr also fails.
		}
	}

	function passive(event, text) {
		if (!text) return;
		if (event === "PreToolUse" || event === "PostToolUse") {
			return { additionalContext: text };
		}
		if (event === "SessionStart" || event === "SubagentStart") {
			pi.sendMessage(
				{ customType: "handrail", content: text, display: false, attribution: "agent" },
				{ deliverAs: "nextTurn", triggerTurn: false },
			);
		}
	}

	async function failedOpen(event, ctx, error) {
		const text = "handrail: omp bridge failed open: " + (error instanceof Error ? error.message : String(error));
		await notice(ctx, text);
		try {
			return passive(event, text);
		} catch (deliveryError) {
			await notice(ctx, "handrail: omp bridge failed open: passive delivery failed: " + String(deliveryError));
		}
	}

	function assistantText(message) {
		if (message?.role !== "assistant" || !Array.isArray(message.content)) return;
		const parts = message.content
			.filter(block => block?.type === "text" && typeof block.text === "string")
			.map(block => block.text);
		return parts.length ? parts.join("\n") : undefined;
	}

	function envelope(event, native, ctx) {
		const payload = { cwd: ctx.cwd, session_id: ctx.sessionManager.getSessionId() };
		if (event === "PreToolUse" || event === "PostToolUse") {
			payload.tool_name = native.toolName;
			payload.tool_input = native.input;
		}
		if (event === "SubagentStart" || event === "SubagentStop") {
			payload.agent_type = ctx.agent.name;
		}
		if (event === "Stop") {
			payload.stop_hook_active = native.stop_hook_active;
			payload.last_assistant_message = assistantText(native.last_assistant_message);
		}
		if (event === "SubagentStop") {
			payload.stop_hook_active = false;
			const messages = native.messages;
			for (let i = messages.length - 1; i >= 0; i--) {
				if (messages[i]?.role === "assistant") {
					payload.last_assistant_message = assistantText(messages[i]);
					break;
				}
			}
		}
		return payload;
	}

	async function invoke(event, payload, ctx, signal) {
		let child;
		try {
			child = Bun.spawn([handrailBinary, "hook", "omp", event], {
				cwd: ctx.cwd,
				stdin: new Blob([JSON.stringify(payload)]),
				stdout: "pipe",
				stderr: "pipe",
				timeout: 1000,
				killSignal: "SIGKILL",
				signal,
			});
			const [stdout, stderr, code] = await Promise.all([
				new Response(child.stdout).text(),
				new Response(child.stderr).text(),
				child.exited,
			]);
			if (signal?.aborted) throw new Error("hook invocation aborted");
			if (child.signalCode) throw new Error("hook terminated by " + child.signalCode + " (timeout or signal)");
			if (code !== 0 && code !== 2) throw new Error("hook exited with code " + code);
			return { stdout, stderr, code };
		} catch (error) {
			if (child) {
				try {
					child.kill("SIGKILL");
				} catch {
					// It may already have exited.
				}
			}
			throw error;
		} finally {
			if (child) {
				try {
					await child.exited;
				} catch {
					// Preserve the original failure, but always wait for the child.
				}
			}
		}
	}

	function response(stdout, event) {
		if (!stdout.trim()) return {};
		const out = JSON.parse(stdout);
		const object = value => value !== null && typeof value === "object" && !Array.isArray(value);
		const strings = (value, fields) => {
			for (const field of fields) {
				if (Object.hasOwn(value, field) && typeof value[field] !== "string") {
					throw new Error("invalid response field " + field);
				}
			}
		};
		if (!object(out)) throw new Error("hook response is not an object");
		strings(out, ["decision", "reason", "systemMessage"]);
		if (Object.hasOwn(out, "decision") && (out.decision !== "block" || event !== "Stop")) {
			throw new Error("invalid decision for " + event);
		}
		if (out.decision === "block" && typeof out.reason !== "string") {
			throw new Error("block response has no reason");
		}
		if (Object.hasOwn(out, "hookSpecificOutput")) {
			const specific = out.hookSpecificOutput;
			if (!object(specific)) throw new Error("hookSpecificOutput is not an object");
			strings(specific, ["hookEventName", "permissionDecision", "permissionDecisionReason", "additionalContext"]);
			if (Object.hasOwn(specific, "hookEventName") && specific.hookEventName !== event) {
				throw new Error("hook response event does not match " + event);
			}
			if (Object.hasOwn(specific, "permissionDecision")) {
				if (event !== "PreToolUse" || !["allow", "deny", "ask"].includes(specific.permissionDecision)) {
					throw new Error("invalid permission decision for " + event);
				}
				if (specific.permissionDecision !== "allow" && typeof specific.permissionDecisionReason !== "string") {
					throw new Error("permission response has no reason");
				}
			}
		}
		return out;
	}

	async function bridge(event, native, ctx) {
		try {
			const result = await invoke(event, envelope(event, native, ctx), ctx, native.signal);
			// Exit 2 is also the Go delivery fallback after a partial stdout write.
			// Do not parse that partial JSON and accidentally turn a denial into allow.
			if (result.code === 2) {
				if (!["PreToolUse", "Stop", "SessionEnd"].includes(event)) {
					throw new Error("unexpected hook exit 2 for " + event);
				}
				await notice(ctx, result.stderr);
				if (event === "PreToolUse") return { block: true, reason: result.stderr };
				if (event === "Stop") return { decision: "block", reason: result.stderr };
				return;
			}
			const out = response(result.stdout, event);
			const specific = out.hookSpecificOutput;
			if (specific?.permissionDecision === "ask") {
				const reason = specific.permissionDecisionReason;
				let approved = false;
				try {
					if (ctx.hasUI) approved = (await ctx.ui.confirm("handrail approval", reason)) === true;
				} catch {
					// Cancellation and failed/unsupported dialogs are never approval.
				}
				if (!approved) return { block: true, reason: reason + "\nApproval was not granted." };
				// The dialog already delivered the human reason; no second notice.
				return passive(event, specific.additionalContext);
			}
			await notice(ctx, out.systemMessage);
			if (specific?.permissionDecision === "deny") {
				return { block: true, reason: specific.permissionDecisionReason };
			}
			if (out.decision === "block") return { decision: "block", reason: out.reason };
			return passive(event, specific?.additionalContext);
		} catch (error) {
			return failedOpen(event, ctx, error);
		}
	}

	pi.on("tool_call", (event, ctx) => bridge("PreToolUse", event, ctx));
	pi.on("tool_result", (event, ctx) => {
		if (event.isError !== true) return bridge("PostToolUse", event, ctx);
	});
	pi.on("session_start", (event, ctx) => {
		if (ctx.agent.kind === "main") return bridge("SessionStart", event, ctx);
		if (ctx.agent.kind === "sub") return bridge("SubagentStart", event, ctx);
	});
	for (const name of ["session_switch", "session_branch", "session_tree", "session_compact"]) {
		pi.on(name, (event, ctx) => {
			if (ctx.agent.kind === "main") return bridge("SessionStart", event, ctx);
		});
	}
	pi.on("session_shutdown", (event, ctx) => {
		if (ctx.agent.kind === "main") return bridge("SessionEnd", event, ctx);
	});
	pi.on("session_stop", (event, ctx) => {
		if (ctx.agent.kind === "main") return bridge("Stop", event, ctx);
	});
	pi.on("agent_end", (event, ctx) => {
		if (ctx.agent.kind === "sub" && event.willContinue !== true) return bridge("SubagentStop", event, ctx);
	});
}
