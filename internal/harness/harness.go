// Package harness adapts each supported agent harness to handrail: its hook
// payloads in, its exit codes and JSON protocol out.
package harness

import (
	"encoding/json"
	"errors"
	"io"
	"net/url"
	"os"
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
	// quirks are the behaviours a user should know about but handrail cannot
	// change. Reported next to the degradations, for the same reason.
	quirks  []string
	title   string // how the harness names itself in a report
	dir     string // user-level directory, under the home directory
	homeEnv string // the variable that relocates that directory, if the harness has one
	file    string // the one config file sync writes inside it
	// sessionEnv is the variable the harness sets in the shell of a session
	// it runs.
	sessionEnv string
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
	// patchInShell is true where the harness applies an apply_patch heredoc a
	// shell call sends, rather than running the line.
	patchInShell bool
	// bypass reads the harness's settings for what keeps its hooks from
	// running, since each harness keeps that switch in its own format.
	bypass func(a Adapter, root string) (Bypass, error)
}

// eventCaps is one row of an Adapter's Capability matrix.
type eventCaps struct {
	name   string
	deny   denial // how a block is delivered, or noDenial where it cannot be
	inject bool   // whether a hook can put a message in front of the agent without a block
	ask    bool   // whether a hook can hand the call to the human for approval
	silent bool   // whether the user sees nothing a hook says, on any channel
}

// denial is how a harness hears a block on one event. The zero value is the
// event with no denial to give, where a block degrades to a warning.
type denial int

const (
	noDenial       denial = iota
	permissionDeny        // exit 0 with permissionDecision: "deny" and the reason
	decisionBlock         // exit 0 with decision: "block" and the reason, the human's text
	continueOnce          // exit 0 with decision: "block" and the reason, the agent's next instruction
)

// The event, tool and kind names the code branches on, beyond the tables.
const (
	eventUserPromptSubmit = "UserPromptSubmit"
	eventSessionEnd       = "SessionEnd"
	eventSubagentStart    = "SubagentStart"
	eventSubagentStop     = "SubagentStop"
	toolAgent             = "Agent"
	kindShell             = "shell"
	kindFileEdit          = "file_edit"
)

// Errors a payload handrail cannot read at all fails with.
var (
	errNullPayload      = errors.New("the payload is null")
	errCwdNotString     = errors.New("cwd is not a string")
	errInputNotAnObject = errors.New("tool_input is not an object")
)

// Both harnesses read the same Claude-shaped hook config and speak the same
// payload and decision protocol: Codex's hooks engine is Claude-compatible by
// design, down to the tool names on the wire (developers.openai.com/codex/hooks).
// The differences are the file it lives in and where blocking stops.
//
// Events that run after the fact or outside a decision point have no denial to
// give, and Codex cannot fail closed on a prompt before the model request.
// SessionEnd injects nothing on either: the session is over and the JSON is
// discarded, so a warning there reaches the user or nobody. On Stop and
// SubagentStop any message to the agent makes it continue, so there only a
// block reaches it.
var adapters = []Adapter{
	{
		Name: "claude", title: "Claude Code", dir: ".claude", homeEnv: "CLAUDE_CONFIG_DIR", file: "settings.json",
		aliases: [][]string{{toolAgent, "Task"}}, agentTypeKey: "subagent_type", agentPromptKey: "prompt",
		patchInShell: false, sessionEnv: "CLAUDE_CODE_SESSION_ID", bypass: claudeBypass,
		events: []eventCaps{
			{name: "PreToolUse", deny: permissionDeny, inject: true, ask: true},
			{name: "PostToolUse", inject: true},
			{name: "UserPromptSubmit", deny: decisionBlock, inject: true},
			{name: "SessionStart", inject: true},
			{name: "SessionEnd"},
			{name: "Stop", deny: continueOnce},
			{name: "SubagentStart", inject: true},
			{name: "SubagentStop", deny: continueOnce},
		},
		quirks: []string{
			"hook errors and timeouts fail open, so a broken guardrail never stops the session",
			"disableAllHooks and cloud sessions bypass handrail entirely",
		},
	},
	{
		Name: "codex", title: "Codex CLI", dir: ".codex", homeEnv: "CODEX_HOME", file: "hooks.json", patchInShell: true,
		bypass:       codexBypass,
		aliases:      [][]string{{"apply_patch", "Edit", "Write"}, {"spawn_agent", toolAgent}},
		agentTypeKey: "agent_type", agentPromptKey: "message", sessionEnv: "CODEX_THREAD_ID",
		events: []eventCaps{
			{name: "PreToolUse", deny: permissionDeny, inject: true},
			{name: "PostToolUse", inject: true},
			{name: "UserPromptSubmit", inject: true},
			{name: "SessionStart", inject: true},
			{name: "SessionEnd", silent: true},
			{name: "Stop", deny: continueOnce},
			{name: "SubagentStart", inject: true},
			{name: "SubagentStop", deny: continueOnce},
		},
		quirks: []string{
			"hook errors and timeouts fail open, so a broken guardrail never stops the session",
			"non-managed hooks need a one-time trust review, " +
				"and --dangerously-bypass-hook-trust skips that review rather than the hooks",
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

	var none Adapter

	return none, false
}

// Session names the variable that says this process runs inside a harness
// session, and "" outside one.
func Session() string {
	for _, a := range adapters {
		if os.Getenv(a.sessionEnv) != "" {
			return a.sessionEnv
		}
	}

	return ""
}

// Names lists the harness identifiers, for the message a wrong one earns.
func Names() []string {
	names := make([]string, 0, len(adapters))
	for _, a := range adapters {
		names = append(names, a.Name)
	}

	return names
}

// Call is one harness payload as handrail reads it: the canonical payloads,
// the cwd tier discovery should start from, and the session it belongs to.
type Call struct {
	Cwd      string
	Session  string
	Payloads []rule.Payload
}

// Normalize turns a harness payload for event into the canonical payloads the
// matcher evaluates, one per edit a patch makes, with the cwd and the session.
// The envelope is read untyped, so a canonical key holding a value of the
// wrong type costs its own field and not the whole payload; only an envelope
// that is not an object, or a tool input that is not one, fails it. The
// session is read even then, since the Decision log names it.
func (a Adapter) Normalize(event string, data []byte) (Call, error) {
	decoded, err := decodeEnvelope(data)
	session, _ := decoded.fields["session_id"].(string)

	if err != nil {
		return Call{Cwd: "", Session: session, Payloads: nil}, err
	}

	return Call{Cwd: decoded.cwd, Session: session, Payloads: payloads(a, event, decoded)}, nil
}

// payloads reads the canonical payloads a harness payload yields from its
// decoded envelope, none for an internal agent.
func payloads(adapter Adapter, event string, decoded envelope) []rule.Payload {
	env, input := decoded.fields, decoded.input
	payload := rule.Payload{Event: event, Kind: "", StopHookActive: false}
	name := toolName(&payload, env)
	payload.Kind = classify(name)
	tools := adapter.toolNames(name)
	setTool(&payload, tools)
	// Read by key presence, on any tool, and from the tool input alone: the
	// envelope's model is the session's, and its agent_type on a tool event
	// names the subagent calling rather than one the call asks for.
	set(&payload, "agent_type", input, adapter.agentTypeKey)
	set(&payload, "agent_prompt", input, adapter.agentPromptKey)
	set(&payload, "model", input, "model")
	set(&payload, "url", input, "url")
	setToolURL(&payload, name, input)

	var edits []rule.Payload

	switch payload.Kind {
	case kindShell:
		set(&payload, "command", input, "command")
		setSandbox(&payload, input)
		edits = adapter.shellEdits(event, tools, input)
	case kindFileEdit:
		set(&payload, "path", input, "file_path", "notebook_path")

		// The tool input keys a file edit carries its written text under, in
		// the order content reads them.
		textKeys := [...]string{"content", "new_string", "new_source"}
		set(&payload, "content", input, textKeys[:]...)
		set(&payload, "removed_content", input, "old_string")
		writesEmpty(&payload, slices.ContainsFunc(textKeys[:], func(k string) bool {
			_, ok := input[k].(string)

			return ok
		}))
		// Codex passes apply_patch as a shell-like tool, so the whole edit
		// arrives as one patch envelope under command, which its hooks reference
		// states outright: "Bash and apply_patch use tool_input.command". Left
		// unread, every path and content condition would silently never fire on
		// that harness's only editing tool.
		// A non-string envelope reads as an empty one: an edit naming nothing.
		if raw, ok := input["command"]; ok && !payload.Has("path") {
			patch, _ := raw.(string)

			return patchPayloads(event, tools, "", patch)
		}
	case "file_read":
		set(&payload, "path", input, "file_path")
	case "mcp":
		setServer(&payload, name)
	}

	set(&payload, "prompt", env, "prompt")
	setStop(&payload, event, env)

	if !setSubagent(&payload, event, env) {
		return nil
	}

	return append([]rule.Payload{payload}, edits...)
}

// envelope is a harness payload read untyped, along with the two keys whose
// wrong type fails it: cwd and tool_input.
type envelope struct {
	fields map[string]any
	input  map[string]any
	cwd    string
}

// decodeEnvelope reads a harness payload into its envelope. On an error the
// envelope is whatever was read so far, and the caller discards it.
func decodeEnvelope(data []byte) (envelope, error) {
	var env envelope

	err := json.Unmarshal(data, &env.fields)
	if err != nil {
		return env, err
	}

	if env.fields == nil {
		return env, errNullPayload
	}
	// cwd picks the project whose rules apply, so one handrail cannot read
	// fails the payload, which is declared, rather than passing for absent.
	if raw, ok := env.fields["cwd"]; ok {
		if env.cwd, ok = raw.(string); !ok {
			return env, errCwdNotString
		}
	}

	if raw, ok := env.fields["tool_input"]; ok {
		if env.input, ok = raw.(map[string]any); !ok {
			return env, errInputNotAnObject
		}
	}

	return env, nil
}

// toolName reads the name of the tool called, declaring tool unreadable where
// it is not a string.
func toolName(p *rule.Payload, env map[string]any) string {
	var name string
	if raw, ok := env["tool_name"]; ok {
		if name, ok = raw.(string); !ok {
			p.SetField("unreadable", "tool")
		}
	}

	return name
}

// setToolURL reads the url a tool carries somewhere other than its url key.
func setToolURL(payload *rule.Payload, tool string, input map[string]any) {
	switch tool {
	case "Monitor":
		if ws, ok := input["ws"].(map[string]any); ok {
			set(payload, "url", ws, "url")
		}
	case "webrun":
		// Most refs name a search result rather than a page; only an absolute
		// http or https URL is a destination.
		open, _ := input["open"].([]any)
		for _, o := range open {
			ref, _ := o.(map[string]any)
			if s, ok := ref["ref_id"].(string); ok {
				u, err := url.Parse(s)
				if err == nil && (u.Scheme == "http" || u.Scheme == "https") {
					payload.SetField("url", s)
				}
			}
		}
	}
}

// setSandbox reads what a shell call asks of its sandbox.
func setSandbox(payload *rule.Payload, input map[string]any) {
	// Claude Code's shells ask their sandbox for more under these keys.
	// WebSearch's allowed_domains filters results instead, and it is not
	// a shell.
	if raw, ok := input["allowed_domains"]; ok {
		grants, readable := raw.([]any)
		for _, g := range grants {
			if s, ok := g.(string); ok {
				payload.SetField("network_grant", s)
			} else {
				readable = false
			}
		}

		if !readable {
			payload.SetField("unreadable", "network_grant")
		}
	}

	if raw, ok := input["dangerouslyDisableSandbox"]; ok {
		if off, ok := raw.(bool); !ok {
			payload.SetField("unreadable", "unsandboxed")
		} else if off {
			payload.SetField("unsandboxed", "true")
		}
	}
}

// setServer reads the MCP server an mcp__<server>__<tool> call names.
func setServer(p *rule.Payload, tool string) {
	if server, _, ok := strings.Cut(strings.TrimPrefix(tool, "mcp__"), "__"); ok {
		p.SetField("server", server)
	}
}

// setStop reads what a stop event carries.
func setStop(payload *rule.Payload, event string, env map[string]any) {
	// Codex sends null for a stop with no text, which on this key alone means
	// absent rather than unreadable.
	if rule.StopEvent(event) {
		payload.StopHookActive, _ = env["stop_hook_active"].(bool)
		if env["last_assistant_message"] != nil {
			set(payload, "response", env, "last_assistant_message")
		}
	}
}

// setSubagent reads the subagent a subagent event is about, and reports false
// for an event no rule is about.
func setSubagent(payload *rule.Payload, event string, env map[string]any) bool {
	// On a subagent event the envelope's agent_type names the subagent the
	// event is about, the meaning it has on a spawn call. One that is empty,
	// null or absent is Claude Code's own internal agent, which no rule is
	// about; any other non-string is still declared unreadable.
	if event == eventSubagentStart || event == eventSubagentStop {
		if v := env["agent_type"]; v == nil || v == "" {
			return false
		}

		set(payload, "agent_type", env, "agent_type")
	}

	return true
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
		return kindShell
	case "Edit", "Write", "NotebookEdit", "apply_patch":
		return kindFileEdit
	case "Read":
		return "file_read"
	case toolAgent:
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

// setTool writes every name the call answers to as a Spelling of tool.
func setTool(p *rule.Payload, names []string) {
	for _, n := range names {
		p.SetField("tool", n)
	}
}

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
	var (
		edits   []rule.Payload
		section patchSection
	)

	done := func() {
		if section.paths != nil {
			edits = append(edits, section.edit(event, tools))
		}
	}

	for line := range strings.SplitSeq(patch, "\n") {
		header, ok := patchHeader(line)
		switch {
		case !ok:
			section.read(line)
		case header.verb == "Move to:":
			section.paths = append(section.paths, inDir(dir, header.file))
		default:
			done()

			section = patchSection{verb: header.verb, paths: []string{inDir(dir, header.file)}, added: nil, removed: nil}
		}
	}

	done()

	if edits == nil {
		unread := rule.Payload{Event: event, Kind: kindFileEdit, StopHookActive: false}
		setTool(&unread, tools)

		for _, f := range [...]string{"path", "content", "removed_content"} {
			unread.SetField("unreadable", f)
		}

		edits = append(edits, unread)
	}

	return edits
}

// patchSection is one file section of a patch envelope, as read so far.
type patchSection struct {
	verb                  string // the verb of the header that opened it
	paths, added, removed []string
}

// read takes a line of the section's body: one it adds, one it removes, or
// context. A line before the first header belongs to no section.
func (s *patchSection) read(line string) {
	if s.paths == nil {
		return
	}

	if strings.HasPrefix(line, "+") {
		s.added = append(s.added, line[1:])
	} else if strings.HasPrefix(line, "-") {
		s.removed = append(s.removed, line[1:])
	}
}

// edit is the file_edit payload the section makes.
func (s *patchSection) edit(event string, tools []string) rule.Payload {
	payload := rule.Payload{Event: event, Kind: kindFileEdit, StopHookActive: false}
	setTool(&payload, tools)
	payload.SetRename(s.paths[0], s.paths[len(s.paths)-1])
	payload.SetField("content", strings.Join(s.added, "\n"))
	payload.SetField("removed_content", strings.Join(s.removed, "\n"))

	if s.verb == "Delete File:" {
		payload.SetField("deletes", "true")
	}
	// An added file names its whole text and an edit that removes lines
	// names what replaces them; a bare rename names no text at all.
	writesEmpty(&payload, s.verb == "Add File:" || len(s.removed) > 0)

	return payload
}

// inDir joins a relative file to dir, the directory the patch is applied in.
func inDir(dir, file string) string {
	if dir != "" && file != "" && !path.IsAbs(file) {
		return path.Join(dir, file)
	}

	return file
}

// header is a patch header: its verb and the file it names.
type header struct {
	verb, file string
}

// patchHeader reads a patch header. The headers sit at column 0, so an
// indented line that looks like one is content, not a header.
func patchHeader(line string) (header, bool) {
	var none header

	rest, found := strings.CutPrefix(line, "*** ")
	if !found {
		return none, false
	}

	for _, verb := range []string{"Add File:", "Update File:", "Delete File:", "Move to:"} {
		if file, found := strings.CutPrefix(rest, verb); found {
			return header{verb: verb, file: strings.TrimSpace(file)}, true
		}
	}

	return none, false
}

// set copies the first key the tool input actually carries into the canonical
// field. A key the call omits and a key it carries empty are the same answer,
// so both fall through to the next key and, failing every key, leave the field
// absent. Which of those two a key is, and what an empty one means, is
// rule.Payload.SetField's answer rather than this Adapter's. A key carrying
// anything but a string, null included, is the source the call chose, so it
// declares the field unreadable rather than falling through to the next key.
func set(payload *rule.Payload, name string, input map[string]any, keys ...string) {
	for _, k := range keys {
		v, present := input[k]
		if !present {
			continue
		}

		text, ok := v.(string)
		if !ok {
			payload.SetField("unreadable", name)

			return
		}

		if payload.SetField(name, text) {
			return
		}
	}
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

// Injects reports whether the agent hears a message on event that is not a
// block. On Stop and SubagentStop any message to it makes it continue, and on
// SessionEnd it is gone.
func (a Adapter) Injects(event string) bool { return a.caps(event).inject }

// stderrExit is the exit code both harnesses read as a block with its reason
// on stderr, and the one Claude Code shows the user on SessionEnd.
const stderrExit = 2

// Deliver writes message and human in the harness's protocol and returns the
// exit code. A PreToolUse block is a permissionDecision deny whose reason the
// agent reads, an ask is one whose reason the approval prompt shows, a
// UserPromptSubmit block is a decision the human reads, a block on Stop or
// SubagentStop is a decision whose reason is the agent's next instruction, and
// anything else proceeds. Where the harness injects, the agent reads message
// as context. Both harnesses document the same channels, so one
// implementation serves. human is what the user sees on systemMessage.
func (a Adapter) Deliver(event, message, human string, outcome rule.Outcome, stdout, stderr io.Writer) int {
	if message == "" && human == "" {
		return 0
	}
	// SessionEnd has no decision control and both harnesses discard its JSON,
	// so stderr on exit 2, which Claude Code shows the user, is the one
	// channel left.
	if event == eventSessionEnd {
		_, _ = io.WriteString(stderr, human+"\n")

		return stderrExit
	}

	out := a.output(event, message, human, outcome)
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	// Rule messages are prose, so HTML escaping would only mangle them.
	enc.SetEscapeHTML(false)
	// A write that fails has nobody left to tell, and failing open is the
	// promise: never turn handrail's own trouble into the harness's. A block
	// is the exception, since it is the rule's outcome rather than handrail's
	// trouble: exit 2 still denies on both harnesses.
	err := enc.Encode(out)
	if err != nil && a.degrade(event, outcome) == rule.Block {
		_, _ = io.WriteString(stderr, message+"\n")

		return stderrExit
	}

	return 0
}

// output is the hook output Deliver writes on stdout.
func (a Adapter) output(event, message, human string, outcome rule.Outcome) hookOutput {
	out := hookOutput{Decision: "", Reason: "", SystemMessage: human, HookSpecificOutput: nil}
	if a.Injects(event) {
		out.HookSpecificOutput = &hookSpecific{
			HookEventName: event, PermissionDecision: "", PermissionDecisionReason: "", AdditionalContext: message,
		}
	}

	switch delivered := a.degrade(event, outcome); {
	case delivered == rule.Ask:
		// The human's message rides in the approval prompt, not beside it.
		out.SystemMessage = ""
		out.HookSpecificOutput.PermissionDecision = "ask"
		out.HookSpecificOutput.PermissionDecisionReason = human
	case delivered == rule.Block && a.caps(event).deny == decisionBlock:
		// The harness shows the reason to the human and erases the prompt, so
		// no agent is left to hear the rest.
		out.Decision, out.Reason, out.HookSpecificOutput = "block", human, nil
	case delivered == rule.Block && a.caps(event).deny == continueOnce:
		out.Decision, out.Reason = "block", message
	case delivered == rule.Block:
		out.HookSpecificOutput = &hookSpecific{
			HookEventName: event, PermissionDecision: "deny", PermissionDecisionReason: message, AdditionalContext: "",
		}
	}

	return out
}

// shellEdits reads the edits a shell call's patch heredoc makes, where this
// harness applies one.
func (a Adapter) shellEdits(event string, tools []string, input map[string]any) []rule.Payload {
	// Codex applies a patch heredoc sent through the shell itself, after
	// this hook and with no second one, so this is the only place its
	// edits are seen. Claude Code runs the same line as a program.
	if line, ok := input["command"].(string); ok && a.patchInShell {
		if patch := shell.Patch(line); patch != nil {
			return patchPayloads(event, tools, patch.Dir, patch.Body)
		}
	}

	return nil
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

// caps reads event's row of the Adapter's Capability matrix. An event the
// matrix lacks has no row, and so can neither block nor inject.
func (a Adapter) caps(event string) eventCaps {
	for _, c := range a.events {
		if c.name == event {
			return c
		}
	}

	var none eventCaps

	return none
}

// degrade is the Outcome the harness delivers for outcome on event: outcome
// itself, or the nearest one keeping its promise where the harness cannot
// deliver it. An ask rises to block, since only a denial keeps a call from
// proceeding without a human yes; a block falls to warn. On an event the
// harness lacks, every rule is skipped.
func (a Adapter) degrade(event string, outcome rule.Outcome) rule.Outcome {
	row := a.caps(event)
	if row.name == "" {
		return rule.Allow
	}

	if outcome == rule.Ask && !row.ask {
		outcome = rule.Block
	}

	if outcome == rule.Block && row.deny == noDenial {
		outcome = rule.Warn
	}

	return outcome
}

// reason says why the harness delivers to on event in place of the rule's own
// action, for the degradation report. Only a block is ever reached by rising,
// from an ask; every other substitution is a denial the harness cannot honour.
func (a Adapter) reason(event string, to rule.Outcome) string {
	switch {
	case to == rule.Allow:
		return a.title + " has no " + event + " event"
	case to == rule.Block:
		return "the rule asks for approval, and " + a.title + " cannot ask for it, so the call is denied"
	case event == eventUserPromptSubmit:
		return a.title + " cannot fail closed on UserPromptSubmit before the model request (upstream #33630)"
	}

	return a.title + " has no denial to give on " + event
}
