// Package harness adapts each supported agent harness to handrail: its hook
// payloads in, its exit codes and JSON protocol out.
package harness

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/svyatov/handrail/internal/rule"
)

// Adapter is one harness's translation knowledge: where its user-level config
// lives, what it calls itself in a report, and what it cannot be made to do.
// The identifier is the harness's binary name, which is what users type.
type Adapter struct {
	Name string
	// Quirks are the behaviours a user should know about but handrail cannot
	// change. Reported next to the degradations, for the same reason.
	Quirks       []string
	title        string // how the harness names itself in a report
	dir          string // user-level directory, under the home directory
	homeEnv      string // the variable that relocates that directory, if the harness has one
	file         string // the one config file sync writes inside it
	blocksPrompt bool   // whether a denial on UserPromptSubmit is honoured
}

// Both harnesses read the same Claude-shaped hook config and speak the same
// payload and decision protocol: Codex's hooks engine is Claude-compatible by
// design, down to the tool names on the wire (developers.openai.com/codex/hooks).
// The differences are the file it lives in and where blocking stops.
var adapters = []Adapter{
	{
		Name: "claude", title: "Claude Code", dir: ".claude", homeEnv: "CLAUDE_CONFIG_DIR", file: "settings.json",
		blocksPrompt: true,
		Quirks: []string{
			"hook errors and timeouts fail open, so a broken guardrail never stops the session",
			"disableAllHooks and cloud sessions bypass handrail entirely",
		},
	},
	{
		Name: "codex", title: "Codex CLI", dir: ".codex", homeEnv: "CODEX_HOME", file: "hooks.json",
		Quirks: []string{
			"hook errors and timeouts fail open, so a broken guardrail never stops the session",
			"non-managed hooks need a one-time trust review, and --dangerously-bypass-hook-trust skips that review rather than the hooks",
			"an enterprise allow_managed_hooks_only requirement ignores user-level hooks, handrail's included",
			"codex exec --ignore-rules bypasses execpolicy, so a rule promoted by advise loses its fail-closed backstop there",
		},
	},
}

// Adapters lists every harness handrail speaks, in the order sync reports them.
func Adapters() []Adapter { return adapters }

// Lookup finds the adapter a harness identifier names.
func Lookup(name string) (Adapter, bool) {
	for _, a := range adapters {
		if a.Name == name {
			return a, true
		}
	}
	return Adapter{}, false
}

// Names lists the harness identifiers, for the message a wrong one earns.
func Names() []string {
	names := make([]string, 0, len(adapters))
	for _, a := range adapters {
		names = append(names, a.Name)
	}
	return names
}

// hookInput is the part of a hook payload a matcher can address. Everything
// else is left where it came from: v1 conditions cannot reach the raw payload,
// so carrying it along would buy nothing on the hot path.
type hookInput struct {
	CWD       string         `json:"cwd"`
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
	Prompt    string         `json:"prompt"`
}

// Normalize turns a harness payload for event into the canonical payload the
// matcher evaluates, and reports the cwd tier discovery should start from.
func (a Adapter) Normalize(event string, data []byte) (rule.Payload, string, error) {
	var in hookInput
	if err := json.Unmarshal(data, &in); err != nil {
		return rule.Payload{}, "", err
	}
	p := rule.Payload{Event: event, Kind: classify(in.ToolName), Fields: map[string]string{}}
	switch p.Kind {
	case "shell":
		set(p.Fields, "command", in.ToolInput, "command")
	case "file_edit":
		set(p.Fields, "path", in.ToolInput, "file_path", "notebook_path")
		set(p.Fields, "content", in.ToolInput, "content", "new_string", "new_source")
		// Codex passes apply_patch as a shell-like tool, so the whole edit
		// arrives as one patch envelope under command, which its hooks reference
		// states outright: "Bash and apply_patch use tool_input.command". Left
		// unread, every path and content condition would silently never fire on
		// that harness's only editing tool.
		if patch, ok := in.ToolInput["command"].(string); ok && p.Fields["path"] == "" {
			unwrapPatch(p.Fields, patch)
		}
	case "file_read":
		set(p.Fields, "path", in.ToolInput, "file_path")
	case "mcp":
		if server, tool, ok := strings.Cut(strings.TrimPrefix(in.ToolName, "mcp__"), "__"); ok {
			p.Fields["server"], p.Fields["tool"] = server, tool
		}
	}
	if in.Prompt != "" {
		p.Fields["prompt"] = in.Prompt
	}
	return p, in.CWD, nil
}

// classify assigns the canonical tool kind. An event without a tool has no kind,
// which is not the same as "other": a rule saying kind: other means a tool call.
//
// One table covers both harnesses because Codex reports the Claude Code names
// on the wire: its hooks reference documents Bash, apply_patch, and
// mcp__<server>__<tool>, and says a model calling Edit or Write still arrives
// as apply_patch. A name only one harness emits costs the other nothing.
func classify(tool string) string {
	if strings.HasPrefix(tool, "mcp__") {
		return "mcp"
	}
	switch tool {
	case "":
		return ""
	case "Bash":
		return "shell"
	case "Edit", "Write", "NotebookEdit", "apply_patch":
		return "file_edit"
	case "Read":
		return "file_read"
	}
	return "other"
}

// unwrapPatch fills the file_edit fields a patch envelope carries: the first
// file it names, and the lines that file adds. It stops at the second file,
// because path and content must describe the same edit: a content condition
// answering for one file while path answers for another is how a guardrail
// blocks the wrong thing.
//
// ponytail: first file only, because the canonical payload has one path field.
// A per-file payload is a spec change, not an implementation one.
func unwrapPatch(fields map[string]string, patch string) {
	var added []string
	var found bool
	for line := range strings.SplitSeq(patch, "\n") {
		if path, ok := patchPath(line); ok {
			if found {
				break
			}
			found, fields["path"] = true, path
			continue
		}
		if found && strings.HasPrefix(line, "+") {
			added = append(added, line[1:])
		}
	}
	if len(added) > 0 {
		fields["content"] = strings.Join(added, "\n")
	}
}

// patchPath reads the file a patch header names. The headers sit at column 0,
// so an indented line that looks like one is content, not a header.
func patchPath(line string) (string, bool) {
	rest, found := strings.CutPrefix(line, "*** ")
	if !found {
		return "", false
	}
	for _, verb := range []string{"Add File:", "Update File:", "Delete File:", "Move to:"} {
		if path, found := strings.CutPrefix(rest, verb); found {
			return strings.TrimSpace(path), true
		}
	}
	return "", false
}

// set copies the first key the tool input actually carries into the canonical
// field. A field the call does not carry stays absent, which is what a condition
// against it reads as: no match, in either polarity.
func set(fields map[string]string, name string, input map[string]any, keys ...string) {
	for _, k := range keys {
		if s, ok := input[k].(string); ok {
			fields[name] = s
			return
		}
	}
}

// canBlock reports whether the harness honours a denial on this event. Events
// that run after the fact or outside a decision point have no denial to give,
// and Codex cannot fail closed on a prompt before the model request.
func (a Adapter) canBlock(event string) bool {
	switch event {
	case "PreToolUse", "Stop":
		return true
	case "UserPromptSubmit":
		return a.blocksPrompt
	}
	return false
}

// blockReason says why a denial cannot be honoured, for the degradation report.
// Only sync and doctor ask; the hot path takes the predicate and no string.
func (a Adapter) blockReason(event string) string {
	if event == "UserPromptSubmit" {
		return a.title + " cannot fail closed on UserPromptSubmit before the model request (upstream #33630)"
	}
	return a.title + " has no denial to give on " + event
}

// canInject reports whether the harness puts a hook's output in front of the
// agent on this event. SessionEnd is the one that does not: the session is over
// and the JSON is discarded, so a warning there reaches the user or nobody.
func canInject(event string) bool { return event != "SessionEnd" }

type hookOutput struct {
	HookSpecificOutput hookSpecific `json:"hookSpecificOutput"`
}

type hookSpecific struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// Deliver writes message in the harness's protocol and returns the exit code:
// a block is exit 2 with the message as the denial reason on stderr, anything
// else proceeds and injects the message into the agent's context. Both
// harnesses document the same two channels, exit 2 with a stderr reason and a
// hookSpecificOutput.additionalContext object, so one implementation serves.
func (a Adapter) Deliver(event, message string, block bool, stdout, stderr io.Writer) int {
	if message == "" {
		return 0
	}
	// Exit 2 is the harness's own channel. On an event that can block it is the
	// denial; on SessionEnd, which has no decision control and whose JSON output
	// the harness discards, it is the only way left to reach the user, which is
	// what a warning degrades to where context injection does not exist.
	if (block && a.canBlock(event)) || !canInject(event) {
		// A failed write leaves nobody to tell, but the outcome still stands: a
		// block that cannot state its reason is still a block.
		_, _ = io.WriteString(stderr, message+"\n")
		return 2
	}

	out := hookOutput{hookSpecific{HookEventName: event, AdditionalContext: message}}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	// Rule messages are prose, so HTML escaping would only mangle them.
	enc.SetEscapeHTML(false)
	// A write that fails has nobody left to tell, and failing open is the
	// promise: never turn handrail's own trouble into the harness's.
	_ = enc.Encode(out)
	return 0
}
