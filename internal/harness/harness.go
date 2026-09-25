// Package harness adapts each supported agent harness to handrail: its hook
// payloads in, its exit codes and JSON protocol out.
package harness

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"path"
	"slices"
	"strings"

	"github.com/svyatov/handrail/internal/rule"
	"github.com/svyatov/handrail/internal/shell"
)

// Adapter is one harness's translation knowledge: where its user-level config
// lives, what it calls itself in a report, and what it cannot be made to do.
// The identifier is the harness's binary name, which is what users type.
type Adapter struct {
	Name string
	// Quirks are the behaviours a user should know about but handrail cannot
	// change. Reported next to the degradations, for the same reason.
	Quirks  []string
	title   string // how the harness names itself in a report
	dir     string // user-level directory, under the home directory
	homeEnv string // the variable that relocates that directory, if the harness has one
	file    string // the one config file sync writes inside it
	// patchInShell is true where the harness applies an apply_patch heredoc a
	// shell call sends, rather than running the line.
	patchInShell bool
	// aliases lists, per tool name on the wire, every name the harness
	// documents it also answers to, a former name included. The wire name leads.
	aliases [][]string
	// agentTypeKey and agentPromptKey are the tool input keys a spawn names
	// its subagent and hands over its task under.
	agentTypeKey, agentPromptKey string
	// events is the harness's Capability matrix: what a hook can do on each
	// event, in the order sync writes
	// hook entries for them. Delivery, sync and the degradation report all
	// read it, so a capability is written down once per harness.
	events []eventCaps
}

// eventCaps is one row of an Adapter's Capability matrix.
type eventCaps struct {
	name   string
	deny   denial // how a block is delivered, or noDenial where it cannot be
	inject bool   // whether the harness puts a hook's output in front of the agent
	ask    bool   // whether a hook can hand the call to the human for approval
}

// denial is how a harness hears a block on one event. The zero value is the
// event with no denial to give, where a block degrades to a warning.
type denial int

const (
	noDenial       denial = iota
	exitTwo               // exit 2, with the reason on stderr
	permissionDeny        // exit 0 with permissionDecision: "deny" and the reason
	decisionBlock         // exit 0 with decision: "block" and the reason
)

// Both harnesses read the same Claude-shaped hook config and speak the same
// payload and decision protocol: Codex's hooks engine is Claude-compatible by
// design, down to the tool names on the wire (developers.openai.com/codex/hooks).
// The differences are the file it lives in and where blocking stops.
//
// Events that run after the fact or outside a decision point have no denial to
// give, and Codex cannot fail closed on a prompt before the model request.
// SessionEnd injects nothing on either: the session is over and the JSON is
// discarded, so a warning there reaches the user or nobody.
var adapters = []Adapter{
	{
		Name: "claude", title: "Claude Code", dir: ".claude", homeEnv: "CLAUDE_CONFIG_DIR", file: "settings.json",
		aliases: [][]string{{"Agent", "Task"}}, agentTypeKey: "subagent_type", agentPromptKey: "prompt",
		events: []eventCaps{
			{name: "PreToolUse", deny: permissionDeny, inject: true, ask: true},
			{name: "PostToolUse", inject: true},
			{name: "UserPromptSubmit", deny: decisionBlock, inject: true},
			{name: "SessionStart", inject: true},
			{name: "SessionEnd"},
			{name: "Stop", deny: exitTwo, inject: true},
		},
		Quirks: []string{
			"hook errors and timeouts fail open, so a broken guardrail never stops the session",
			"disableAllHooks and cloud sessions bypass handrail entirely",
		},
	},
	{
		Name: "codex", title: "Codex CLI", dir: ".codex", homeEnv: "CODEX_HOME", file: "hooks.json", patchInShell: true,
		aliases:      [][]string{{"apply_patch", "Edit", "Write"}, {"spawn_agent", "Agent"}},
		agentTypeKey: "agent_type", agentPromptKey: "message",
		events: []eventCaps{
			{name: "PreToolUse", deny: permissionDeny, inject: true},
			{name: "PostToolUse", inject: true},
			{name: "UserPromptSubmit", inject: true},
			{name: "SessionStart", inject: true},
			{name: "SessionEnd"},
			{name: "Stop", deny: exitTwo, inject: true},
		},
		Quirks: []string{
			"hook errors and timeouts fail open, so a broken guardrail never stops the session",
			"non-managed hooks need a one-time trust review, and --dangerously-bypass-hook-trust skips that review rather than the hooks",
			"an enterprise allow_managed_hooks_only requirement ignores user-level hooks, handrail's included",
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

// Normalize turns a harness payload for event into the canonical payloads the
// matcher evaluates, one per edit a patch makes, and reports the cwd tier
// discovery should start from. The envelope is read untyped, so a canonical
// key holding a value of the wrong type costs its own field and not the whole
// payload; only an envelope that is not an object, or a tool input that is
// not one, fails it.
func (a Adapter) Normalize(event string, data []byte) ([]rule.Payload, string, error) {
	var env map[string]any
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, "", err
	}
	if env == nil {
		return nil, "", errors.New("the payload is null")
	}
	// cwd picks the project whose rules apply, so one handrail cannot read
	// fails the payload, which is declared, rather than passing for absent.
	var cwd, name string
	var input map[string]any
	if raw, ok := env["cwd"]; ok {
		if cwd, ok = raw.(string); !ok {
			return nil, "", errors.New("cwd is not a string")
		}
	}
	if raw, ok := env["tool_input"]; ok {
		if input, ok = raw.(map[string]any); !ok {
			return nil, "", errors.New("tool_input is not an object")
		}
	}
	p := rule.Payload{Event: event}
	if raw, ok := env["tool_name"]; ok {
		if name, ok = raw.(string); !ok {
			p.SetField("unreadable", "tool")
		}
	}
	p.Kind = classify(name)
	tools := a.toolNames(name)
	setTool(&p, tools)
	// Read by key presence, on any tool, and from the tool input alone: the
	// envelope's model is the session's, and its agent_type on a tool event
	// names the subagent calling rather than one the call asks for.
	set(&p, "agent_type", input, a.agentTypeKey)
	set(&p, "agent_prompt", input, a.agentPromptKey)
	set(&p, "model", input, "model")
	set(&p, "url", input, "url")
	switch name {
	case "Monitor":
		if ws, ok := input["ws"].(map[string]any); ok {
			set(&p, "url", ws, "url")
		}
	case "webrun":
		// Most refs name a search result rather than a page; only an absolute
		// http or https URL is a destination.
		open, _ := input["open"].([]any)
		for _, o := range open {
			ref, _ := o.(map[string]any)
			if s, ok := ref["ref_id"].(string); ok {
				if u, err := url.Parse(s); err == nil && (u.Scheme == "http" || u.Scheme == "https") {
					p.SetField("url", s)
				}
			}
		}
	}
	var edits []rule.Payload
	switch p.Kind {
	case "shell":
		set(&p, "command", input, "command")
		// Claude Code's shells ask their sandbox for more under these keys.
		// WebSearch's allowed_domains filters results instead, and it is not
		// a shell.
		if raw, ok := input["allowed_domains"]; ok {
			grants, readable := raw.([]any)
			for _, g := range grants {
				if s, ok := g.(string); ok {
					p.SetField("network_grant", s)
				} else {
					readable = false
				}
			}
			if !readable {
				p.SetField("unreadable", "network_grant")
			}
		}
		if raw, ok := input["dangerouslyDisableSandbox"]; ok {
			if off, ok := raw.(bool); !ok {
				p.SetField("unreadable", "unsandboxed")
			} else if off {
				p.SetField("unsandboxed", "true")
			}
		}
		// Codex applies a patch heredoc sent through the shell itself, after
		// this hook and with no second one, so this is the only place its
		// edits are seen. Claude Code runs the same line as a program.
		if line, ok := input["command"].(string); ok && a.patchInShell {
			if dir, patch, ok := shell.Patch(line); ok {
				edits = patchPayloads(event, tools, dir, patch)
			}
		}
	case "file_edit":
		set(&p, "path", input, "file_path", "notebook_path")
		set(&p, "content", input, textKeys[:]...)
		set(&p, "removed_content", input, "old_string")
		writesEmpty(&p, slices.ContainsFunc(textKeys[:], func(k string) bool {
			_, ok := input[k].(string)
			return ok
		}))
		// Codex passes apply_patch as a shell-like tool, so the whole edit
		// arrives as one patch envelope under command, which its hooks reference
		// states outright: "Bash and apply_patch use tool_input.command". Left
		// unread, every path and content condition would silently never fire on
		// that harness's only editing tool.
		// A non-string envelope reads as an empty one: an edit naming nothing.
		if raw, ok := input["command"]; ok && !p.Has("path") {
			patch, _ := raw.(string)
			return patchPayloads(event, tools, "", patch), cwd, nil
		}
	case "file_read":
		set(&p, "path", input, "file_path")
	case "mcp":
		if server, _, ok := strings.Cut(strings.TrimPrefix(name, "mcp__"), "__"); ok {
			p.SetField("server", server)
		}
	}
	set(&p, "prompt", env, "prompt")
	return append([]rule.Payload{p}, edits...), cwd, nil
}

// classify assigns the canonical tool kind. An event without a tool has no kind,
// which is not the same as "other": a rule saying kind: other means a tool call.
//
// One table covers both harnesses because Codex reports the Claude Code names
// on the wire: its hooks reference documents Bash, apply_patch, and
// mcp__<server>__<tool>, and says a model calling Edit or Write still arrives
// as apply_patch. A name only one harness emits costs the other nothing.
//
// PowerShell is a shell for the same reason Bash is, and its command rides in
// tool_input.command like Bash's: Claude Code's tools reference says so
// outright. It is opt-in on Linux and macOS rather than absent there, and a
// shell rule that stops firing the moment a user opts in is the failure this
// table exists to prevent. Monitor watches a command or a WebSocket, and takes
// the kind whose field a rule can read: shell.
func classify(tool string) string {
	if strings.HasPrefix(tool, "mcp__") {
		return "mcp"
	}
	switch tool {
	case "":
		return ""
	case "Bash", "PowerShell", "Monitor":
		return "shell"
	case "Edit", "Write", "NotebookEdit", "apply_patch":
		return "file_edit"
	case "Read":
		return "file_read"
	case "Agent":
		return "agent"
	case "WebFetch", "WebSearch", "webrun":
		return "network"
	}
	// Codex's multi-agent v2 puts its spawn tool in a configurable namespace,
	// collaborationspawn_agent by default, with no alias to match instead.
	if strings.HasSuffix(tool, "spawn_agent") {
		return "agent"
	}
	return "other"
}

// toolNames lists every name the harness answers to for a call to tool: the
// name on the wire, then the aliases this harness documents for it. An MCP
// call also answers to its bare tool name. No Adapter invents a name another
// harness uses, because tool is the harness's own vocabulary and kind the
// portable one.
func (a Adapter) toolNames(tool string) []string {
	if rest, ok := strings.CutPrefix(tool, "mcp__"); ok {
		if _, bare, ok := strings.Cut(rest, "__"); ok {
			return []string{tool, bare}
		}
	}
	for _, names := range a.aliases {
		if names[0] == tool {
			return names
		}
	}
	return []string{tool}
}

// setTool writes every name the call answers to as a Spelling of tool.
func setTool(p *rule.Payload, names []string) {
	for _, n := range names {
		p.SetField("tool", n)
	}
}

// textKeys are the tool input keys a file edit carries its written text under,
// in the order content reads them.
var textKeys = [...]string{"content", "new_string", "new_source"}

// writesEmpty sets writes_empty on a call that names the text it writes and
// whose text was read as empty, the one fact content's absence would otherwise
// hide. A call that never names its text, or names no path, says nothing.
func writesEmpty(p *rule.Payload, namesText bool) {
	if namesText && p.Has("path") && !p.Has("content") {
		p.SetField("writes_empty", "true")
	}
}

// patchPayloads reads a patch envelope as one file_edit payload per file
// section, each holding the file it names and the lines it adds. path and
// content must describe the same edit: a content condition answering for one
// file while path answers for another is how a guardrail blocks the wrong
// thing (ADR 0014).
//
// A rename is one edit: its Move to: header adds the destination to the
// section it sits in. A relative path is joined to dir, the directory Codex
// applies the patch in when a shell call cds there first. Every edit carries
// tools, the names of the call that made it. An envelope with no file header
// is still an edit, one that names nothing handrail can read.
func patchPayloads(event string, tools []string, dir, patch string) []rule.Payload {
	var edits []rule.Payload
	var paths, added, removed []string
	var section string // the verb of the header that opened it
	done := func() {
		if paths == nil {
			return
		}
		p := rule.Payload{Event: event, Kind: "file_edit"}
		setTool(&p, tools)
		p.SetRename(paths[0], paths[len(paths)-1])
		p.SetField("content", strings.Join(added, "\n"))
		p.SetField("removed_content", strings.Join(removed, "\n"))
		if section == "Delete File:" {
			p.SetField("deletes", "true")
		}
		// An added file names its whole text and an edit that removes lines
		// names what replaces them; a bare rename names no text at all.
		writesEmpty(&p, section == "Add File:" || len(removed) > 0)
		edits = append(edits, p)
		paths, added, removed = nil, nil, nil
	}
	for line := range strings.SplitSeq(patch, "\n") {
		verb, file, ok := patchHeader(line)
		if dir != "" && file != "" && !path.IsAbs(file) {
			file = path.Join(dir, file)
		}
		switch {
		case !ok:
			if paths == nil {
				continue
			}
			if strings.HasPrefix(line, "+") {
				added = append(added, line[1:])
			} else if strings.HasPrefix(line, "-") {
				removed = append(removed, line[1:])
			}
		case verb == "Move to:":
			paths = append(paths, file)
		default:
			done()
			paths, section = []string{file}, verb
		}
	}
	done()
	if edits == nil {
		p := rule.Payload{Event: event, Kind: "file_edit"}
		setTool(&p, tools)
		for _, f := range [...]string{"path", "content", "removed_content"} {
			p.SetField("unreadable", f)
		}
		edits = append(edits, p)
	}
	return edits
}

// patchHeader reads a patch header: its verb and the file it names. The
// headers sit at column 0, so an indented line that looks like one is content,
// not a header.
func patchHeader(line string) (verb, file string, ok bool) {
	rest, found := strings.CutPrefix(line, "*** ")
	if !found {
		return "", "", false
	}
	for _, verb := range []string{"Add File:", "Update File:", "Delete File:", "Move to:"} {
		if file, found := strings.CutPrefix(rest, verb); found {
			return verb, strings.TrimSpace(file), true
		}
	}
	return "", "", false
}

// set copies the first key the tool input actually carries into the canonical
// field. A key the call omits and a key it carries empty are the same answer,
// so both fall through to the next key and, failing every key, leave the field
// absent. Which of those two a key is, and what an empty one means, is
// rule.Payload.SetField's answer rather than this Adapter's. A key carrying
// anything but a string, null included, is the source the call chose, so it
// declares the field unreadable rather than falling through to the next key.
func set(p *rule.Payload, name string, input map[string]any, keys ...string) {
	for _, k := range keys {
		v, present := input[k]
		if !present {
			continue
		}
		s, ok := v.(string)
		if !ok {
			p.SetField("unreadable", name)
			return
		}
		if p.SetField(name, s) {
			return
		}
	}
}

// caps reads event's row of the Adapter's Capability matrix. An event the
// matrix lacks has no row, and so can neither block nor inject.
func (a Adapter) caps(event string) eventCaps {
	for _, c := range a.events {
		if c.name == event {
			return c
		}
	}
	return eventCaps{}
}

// degrade is the Outcome the harness delivers for o on event: o itself, or
// the nearest one keeping its promise where the harness cannot deliver it. An
// ask rises to block, since only a denial keeps a call from proceeding without
// a human yes; a block falls to warn.
func (a Adapter) degrade(event string, o rule.Outcome) rule.Outcome {
	c := a.caps(event)
	if o == rule.Ask && !c.ask {
		o = rule.Block
	}
	if o == rule.Block && c.deny == noDenial {
		o = rule.Warn
	}
	return o
}

// Note is what an ask this harness turns into a block adds to its message,
// and "" for any other rule. The one substitution that tightens says so at
// event time; the others are reported at sync alone.
func (a Adapter) Note(r *rule.Rule) string {
	if r.Action == rule.Ask && a.Action(r) == rule.Block {
		return "handrail: " + a.reason(r.Event, rule.Block) + "."
	}
	return ""
}

// reason says why the harness delivers to on event in place of the rule's own
// action, for the degradation report. Only a block is ever reached by rising,
// from an ask; every other substitution is a denial the harness cannot honour.
func (a Adapter) reason(event string, to rule.Outcome) string {
	switch {
	case to == rule.Block:
		return "the rule asks for approval, and " + a.title + " cannot ask for it, so the call is denied"
	case event == "UserPromptSubmit":
		return a.title + " cannot fail closed on UserPromptSubmit before the model request (upstream #33630)"
	}
	return a.title + " has no denial to give on " + event
}

type hookOutput struct {
	Decision           string        `json:"decision,omitempty"`
	Reason             string        `json:"reason,omitempty"`
	SystemMessage      string        `json:"systemMessage,omitempty"`
	HookSpecificOutput *hookSpecific `json:"hookSpecificOutput,omitempty"`
}

type hookSpecific struct {
	HookEventName string `json:"hookEventName"`
	// A deny's reason reaches the agent; an ask's is the approval prompt the
	// human reads, and the agent reads the rule as context.
	PermissionDecision       string `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string `json:"permissionDecisionReason,omitempty"`
	AdditionalContext        string `json:"additionalContext,omitempty"`
}

// toStderr reports whether Deliver sends an event's message on exit 2, the
// harness's own channel. On an event whose denial is exit 2 it is the denial;
// on SessionEnd, which has no decision control and whose JSON output the
// harness discards, it is the only way left to reach the user, which is what a
// warning degrades to where context injection does not exist.
func (a Adapter) toStderr(event string, outcome rule.Outcome) bool {
	c := a.caps(event)
	return (a.degrade(event, outcome) == rule.Block && c.deny == exitTwo) || !c.inject
}

// Human is the text the user is shown when Deliver sends message and human
// for event, and "" when nothing reaches them: the whole message on stderr,
// else human, in the approval prompt or beside the call.
func (a Adapter) Human(event, message, human string, outcome rule.Outcome) string {
	if a.toStderr(event, outcome) {
		return message
	}
	return human
}

// Deliver writes message in the harness's protocol and returns the exit code:
// a block is exit 2 with the message as the denial reason on stderr, an ask
// is a permissionDecision whose reason the approval prompt shows, and anything
// else proceeds. Every path but the stderr one injects the message into the
// agent's context. Both harnesses document the same channels, exit 2 with a
// stderr reason and a hookSpecificOutput object, so one implementation serves.
// human is what the user sees on systemMessage; on the stderr and ask paths it
// is already part of message, which reaches the user there.
func (a Adapter) Deliver(event, message, human string, outcome rule.Outcome, stdout, stderr io.Writer) int {
	if message == "" {
		return 0
	}
	if a.toStderr(event, outcome) {
		// A failed write leaves nobody to tell, but the outcome still stands: a
		// block that cannot state its reason is still a block.
		_, _ = io.WriteString(stderr, message+"\n")
		return 2
	}

	out := hookOutput{SystemMessage: human, HookSpecificOutput: &hookSpecific{HookEventName: event, AdditionalContext: message}}
	switch o := a.degrade(event, outcome); {
	case o == rule.Ask:
		// The human's message rides in the approval prompt, not beside it.
		out.SystemMessage = ""
		out.HookSpecificOutput.PermissionDecision = "ask"
		out.HookSpecificOutput.PermissionDecisionReason = human
	case o == rule.Block && a.caps(event).deny == decisionBlock:
		// The harness shows the reason to the human and erases the prompt, so
		// no agent is left to hear the rest.
		out.Decision, out.Reason, out.HookSpecificOutput = "block", human, nil
	case o == rule.Block:
		out.HookSpecificOutput = &hookSpecific{HookEventName: event, PermissionDecision: "deny", PermissionDecisionReason: message}
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	// Rule messages are prose, so HTML escaping would only mangle them.
	enc.SetEscapeHTML(false)
	// A write that fails has nobody left to tell, and failing open is the
	// promise: never turn handrail's own trouble into the harness's.
	_ = enc.Encode(out)
	return 0
}
