// Package claude adapts Claude Code to handrail: its hook payloads in, its exit
// codes and JSON protocol out.
package claude

import (
	"encoding/json"
	"io"
	"strings"

	"github.com/svyatov/handrail/internal/rule"
)

// Name is the harness identifier, which is the harness's binary name.
const Name = "claude"

// hookInput is the part of a Claude Code hook payload a matcher can address.
// Everything else is left where it came from: v1 conditions cannot reach the
// raw payload, so carrying it along would buy nothing on the hot path.
type hookInput struct {
	CWD       string         `json:"cwd"`
	ToolName  string         `json:"tool_name"`
	ToolInput map[string]any `json:"tool_input"`
	Prompt    string         `json:"prompt"`
}

// Normalize turns a Claude Code payload for event into the canonical payload the
// matcher evaluates, and reports the cwd tier discovery should start from.
func Normalize(event string, data []byte) (rule.Payload, string, error) {
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
func classify(tool string) string {
	if strings.HasPrefix(tool, "mcp__") {
		return "mcp"
	}
	switch tool {
	case "":
		return ""
	case "Bash":
		return "shell"
	case "Edit", "Write", "NotebookEdit":
		return "file_edit"
	case "Read":
		return "file_read"
	}
	return "other"
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

// canBlock reports whether the harness honours a denial on this event. Claude
// Code's other events run either after the fact or outside a decision point, so
// a block there degrades to a warning rather than pretending to have stopped it.
func canBlock(event string) bool {
	switch event {
	case "PreToolUse", "UserPromptSubmit", "Stop":
		return true
	}
	return false
}

type hookOutput struct {
	HookSpecificOutput hookSpecific `json:"hookSpecificOutput"`
}

type hookSpecific struct {
	HookEventName     string `json:"hookEventName"`
	AdditionalContext string `json:"additionalContext"`
}

// Deliver writes message in Claude Code's protocol and returns the exit code:
// a block is exit 2 with the message as the denial reason on stderr, anything
// else proceeds and injects the message into the agent's context.
func Deliver(event, message string, block bool, stdout, stderr io.Writer) int {
	if message == "" {
		return 0
	}
	// Exit 2 is the harness's own channel. On an event that can block it is the
	// denial; on SessionEnd, which has no decision control and whose JSON output
	// the harness discards, it is the only way left to reach the user, which is
	// what a warning degrades to where context injection does not exist.
	if (block && canBlock(event)) || event == "SessionEnd" {
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
