package harness

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/svyatov/handrail/internal/rule"
)

const (
	ompKindOther        = "other"
	ompKindNetwork      = "network"
	ompKindMCP          = "mcp"
	ompKindAgent        = "agent"
	ompPathField        = "path"
	ompContentField     = "content"
	ompRemovedField     = "removed_content"
	ompUnreadableField  = "unreadable"
	ompModelField       = "model"
	ompAgentTypeField   = "agent_type"
	ompAgentPromptField = "agent_prompt"
	ompEditTool         = "edit"
	ompReadTool         = "read"
	ompWriteTool        = "write"
	ompTaskTool         = "task"
	ompUpdateOp         = "update"
	ompCreateOp         = "create"
	ompDeleteOp         = "delete"
	ompMoveHeader       = "Move to:"
)

var (
	ompSnapshot = regexp.MustCompile(`^(?:\[(.+)#[0-9A-Fa-f]{4}\]|¶(.+)#[0-9A-Fa-f]{4})$`)
	ompSelector = regexp.MustCompile(`:(?:raw|img|conflicts|` +
		`(?:[0-9]+(?:-[0-9]*|\+[0-9]+)?|-[0-9]+)(?:,(?:[0-9]+(?:-[0-9]*|\+[0-9]+)?|-[0-9]+))*)$`)
)

// ompPayloads reads native tool inputs without borrowing Claude tool aliases.
// Each batch member or textual file section stays a separate policy subject.
func ompPayloads(event string, decoded envelope) []rule.Payload {
	payload := rule.Payload{Event: event, Kind: "", StopHookActive: false}
	name := toolName(&payload, decoded.fields)

	input := decoded.input
	if name == ompEditTool {
		return ompEdits(event, input)
	}

	if name == ompTaskTool {
		return ompTasks(event, input)
	}

	setTool(&payload, []string{name})

	payload.Kind = ompKind(name)
	for _, field := range []string{commandKey, ompContentField, "url", ompModelField} {
		set(&payload, field, input, field)
	}

	set(&payload, ompRemovedField, input, "old_string")
	set(&payload, "prompt", decoded.fields, "prompt")
	setStop(&payload, event, decoded.fields)
	// An unnamed native child is still a child, unlike Claude's internal agents.
	if event == eventSubagentStart || event == eventSubagentStop {
		set(&payload, ompAgentTypeField, decoded.fields, ompAgentTypeField)
	}

	ompToolFields(&payload, name, input)

	return []rule.Payload{payload}
}

func ompKind(name string) string {
	switch name {
	case "":
		return ""
	case "bash":
		return kindShell
	case ompReadTool:
		return "file_read"
	case ompWriteTool:
		return kindFileEdit
	case "web_search":
		return ompKindNetwork
	default:
		if strings.HasPrefix(name, "mcp__") {
			return ompKindMCP
		}

		return ompKindOther
	}
}

func ompToolFields(payload *rule.Payload, name string, input map[string]any) {
	switch name {
	case ompReadTool:
		ompTarget(payload, input, true)
	case ompWriteTool:
		ompTarget(payload, input, false)

		if payload.Kind == kindFileEdit {
			_, text := input[ompContentField].(string)
			writesEmpty(payload, text)
		}
	default:
		if payload.Kind == ompKindMCP {
			payload.SetField(ompUnreadableField, "server")
		}
	}
}

// ompTarget never consults the filesystem: native selector resolution depends
// on whether a literal target exists, which is not an adapter's fact to guess.
func ompTarget(payload *rule.Payload, input map[string]any, read bool) {
	raw, present := input[ompPathField]
	if !present {
		return
	}

	target, ok := raw.(string)
	if !ok {
		payload.SetField(ompUnreadableField, ompPathField)

		return
	}

	target = ompUnwrap(target)
	if strings.Contains(target, "://") {
		ompRemoteTarget(payload, target, read)

		return
	}

	paths := []string{target}
	if read {
		if stripped := ompStripSelectors(target); stripped != target {
			paths = append(paths, ompUnwrap(stripped))
		}
	}

	payload.SetPathSpellings(paths)

	if len(paths) > 1 || strings.ContainsAny(target, ":;") {
		payload.SetField(ompUnreadableField, ompPathField)
	}
}

func ompRemoteTarget(payload *rule.Payload, target string, read bool) {
	parsed, err := url.Parse(target)
	if read && err == nil && (parsed.Scheme == "http" || parsed.Scheme == "https") && parsed.Host != "" {
		payload.Kind = ompKindNetwork
		payload.SetField("url", target)

		return
	}

	payload.Kind = ompKindOther
	payload.SetField(ompUnreadableField, ompPathField)
}

func ompStripSelectors(target string) string {
	for {
		location := ompSelector.FindStringIndex(target)
		if location == nil {
			return target
		}

		target = target[:location[0]]
	}
}

func ompUnquote(path string) string {
	if len(path) >= 2 && (path[0] == '"' || path[0] == '\'') && path[len(path)-1] == path[0] {
		return path[1 : len(path)-1]
	}

	return path
}

func ompUnwrap(path string) string {
	if match := ompSnapshot.FindStringSubmatch(path); match != nil {
		if match[1] != "" {
			return ompUnquote(strings.TrimSpace(match[1]))
		}

		return ompUnquote(strings.TrimSpace(match[2]))
	}

	return path
}

func ompEdit(event string) rule.Payload {
	payload := rule.Payload{Event: event, Kind: kindFileEdit, StopHookActive: false}
	payload.SetField("tool", ompEditTool)

	return payload
}

func ompEdits(event string, input map[string]any) []rule.Payload {
	for _, key := range []string{"input", "_input"} {
		if raw, present := input[key]; present {
			if edits := ompTextEdits(event, raw); edits != nil {
				return edits
			}

			return ompUnreadEdits(event, input)
		}
	}

	if raw, present := input["edits"]; present {
		return ompEditBatch(event, input, raw)
	}

	if ompReplaceEntry(input) {
		return []rule.Payload{ompReplace(event, input, input)}
	}

	return ompUnreadEdits(event, input)
}

func ompTextEdits(event string, raw any) []rule.Payload {
	text, ok := raw.(string)
	if !ok {
		return nil
	}

	text = strings.ReplaceAll(strings.TrimPrefix(text, "\ufeff"), "\r\n", "\n")
	for line := range strings.SplitSeq(text, "\n") {
		if ompSnapshot.MatchString(strings.TrimSpace(line)) {
			return ompHashline(event, text)
		}

		if header, found := patchHeader(line); found && header.verb != ompMoveHeader {
			return patchPayloads(event, []string{ompEditTool}, "", text)
		}
	}

	return nil
}

func ompEditBatch(event string, input map[string]any, raw any) []rule.Payload {
	entries, ok := raw.([]any)
	if !ok || len(entries) == 0 {
		return ompUnreadEdits(event, input)
	}

	out := make([]rule.Payload, 0, len(entries))
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		switch {
		case ompReplaceEntry(entry):
			out = append(out, ompReplace(event, input, entry))
		case ompPatchShape(entry):
			out = append(out, ompPatchEntry(event, input, entry))
		default:
			out = append(out, ompUnreadEdits(event, input)...)
		}
	}

	return out
}

func ompReplaceEntry(entry map[string]any) bool {
	_, old := entry["old_string"]
	_, replacement := entry["new_string"]

	return old || replacement
}

func ompPatchShape(entry map[string]any) bool {
	for _, key := range []string{"op", "rename", "diff"} {
		if _, present := entry[key]; present {
			return true
		}
	}

	return false
}

func ompEditPath(payload *rule.Payload, input map[string]any) string {
	for _, key := range []string{ompPathField, "_path"} {
		raw, present := input[key]
		if !present {
			continue
		}

		path, ok := raw.(string)
		if !ok {
			payload.SetField(ompUnreadableField, ompPathField)

			return ""
		}

		if path != "" {
			path = ompUnwrap(path)
			payload.SetField(ompPathField, path)

			return path
		}
	}

	return ""
}

func ompReplace(event string, input, entry map[string]any) rule.Payload {
	payload := ompEdit(event)
	ompEditPath(&payload, input)
	set(&payload, ompContentField, entry, "new_string")
	set(&payload, ompRemovedField, entry, "old_string")
	_, text := entry["new_string"].(string)
	writesEmpty(&payload, text)

	return payload
}

func ompPatchEntry(event string, input, entry map[string]any) rule.Payload {
	payload := ompEdit(event)
	source := ompEditPath(&payload, input)
	operation := ompPatchOperation(&payload, entry)
	ompPatchRename(&payload, source, entry)

	if operation == ompDeleteOp {
		payload.SetField("deletes", "true")
	}

	ompPatchDiff(&payload, operation, entry)

	return payload
}

func ompPatchOperation(payload *rule.Payload, entry map[string]any) string {
	raw, present := entry["op"]
	if !present {
		return ompUpdateOp
	}

	operation, _ := raw.(string)
	switch operation {
	case ompCreateOp, ompDeleteOp, ompUpdateOp:
	default:
		payload.SetField(ompUnreadableField, ompContentField)
		payload.SetField(ompUnreadableField, ompRemovedField)
	}

	return operation
}

func ompPatchRename(payload *rule.Payload, source string, entry map[string]any) {
	raw, present := entry["rename"]
	if !present {
		return
	}

	if destination, ok := raw.(string); ok {
		payload.SetRename(source, ompUnquote(destination))
	} else {
		payload.SetField(ompUnreadableField, ompPathField)
	}
}

func ompPatchDiff(payload *rule.Payload, operation string, entry map[string]any) {
	raw, present := entry["diff"]
	if !present {
		return
	}

	diff, ok := raw.(string)
	if !ok {
		payload.SetField(ompUnreadableField, ompContentField)
		payload.SetField(ompUnreadableField, ompRemovedField)

		return
	}

	switch operation {
	case ompCreateOp:
		payload.SetField(ompContentField, ompCreateContent(diff))
		writesEmpty(payload, true)
	case ompUpdateOp:
		ompPatchUpdate(payload, diff)
	}
}

// Omp accepts either literal create content or uniformly plus-prefixed rows.
// Match normalize_create_content in its native diff_string.rs executor.
func ompCreateContent(content string) string {
	prefixed := false

	for line := range strings.SplitSeq(content, "\n") {
		if line == "" {
			continue
		}

		if !strings.HasPrefix(line, "+") {
			return content
		}

		prefixed = true
	}

	if !prefixed {
		return content
	}

	var text strings.Builder
	text.Grow(len(content))

	first := true
	for line := range strings.SplitSeq(content, "\n") {
		if !first {
			text.WriteByte('\n')
		}

		first = false

		if body, ok := strings.CutPrefix(line, "+ "); ok {
			text.WriteString(body)
		} else {
			text.WriteString(strings.TrimPrefix(line, "+"))
		}
	}

	return text.String()
}

func ompPatchUpdate(payload *rule.Payload, diff string) {
	section := patchSection{verb: "", paths: []string{""}, added: nil, removed: nil}

	for line := range strings.SplitSeq(strings.ReplaceAll(diff, "\r\n", "\n"), "\n") {
		if !strings.HasPrefix(line, "+++ ") && !strings.HasPrefix(line, "--- ") {
			section.read(line)
		}
	}

	payload.SetField(ompContentField, strings.Join(section.added, "\n"))
	payload.SetField(ompRemovedField, strings.Join(section.removed, "\n"))
	writesEmpty(payload, len(section.removed) > 0)
}

func ompUnreadEdits(event string, input map[string]any) []rule.Payload {
	var paths []string

	if raw, ok := input["paths"].([]any); ok {
		for _, value := range raw {
			if path, ok := value.(string); ok && path != "" {
				paths = append(paths, ompUnwrap(path))
			}
		}
	}

	if len(paths) == 0 {
		payload := ompEdit(event)
		ompEditPath(&payload, input)
		payload.SetField(ompUnreadableField, ompPathField)
		payload.SetField(ompUnreadableField, ompContentField)

		return []rule.Payload{payload}
	}

	out := make([]rule.Payload, 0, len(paths))
	for _, path := range paths {
		payload := ompEdit(event)
		payload.SetField(ompPathField, path)
		payload.SetField(ompUnreadableField, ompPathField)
		payload.SetField(ompUnreadableField, ompContentField)
		out = append(out, payload)
	}

	return out
}

// ompHashSection holds only one file section, so literal text cannot satisfy a
// path condition on a different file in the same patch.
type ompHashSection struct {
	payload      rule.Payload
	source       string
	added        []string
	body, unread bool
}

func (section *ompHashSection) finish() rule.Payload {
	content := strings.Join(section.added, "\n")
	section.payload.SetField(ompContentField, content)

	if section.unread {
		section.payload.SetField(ompUnreadableField, ompContentField)
	} else if len(section.added) > 0 && strings.Trim(content, "\n") == "" {
		section.payload.SetField("writes_empty", "true")
	}

	return section.payload
}

func (section *ompHashSection) read(line, trimmed string) {
	if section.body && strings.HasPrefix(line, "+") {
		section.added = append(section.added, line[1:])

		return
	}

	section.body = false
	section.operation(trimmed)
}

func (section *ompHashSection) operation(line string) {
	switch {
	case line == "REM":
		section.payload.SetField("deletes", "true")
	case strings.HasPrefix(line, "MV "):
		destination := ompUnquote(strings.TrimSpace(strings.TrimPrefix(line, "MV ")))
		section.payload.SetRename(section.source, destination)
	case strings.HasPrefix(line, "PUT "):
		section.body = strings.HasSuffix(line, ":")
		if !section.body {
			section.unread = true
		}
	case strings.HasPrefix(line, "CUT "), line == "", line == "*** Begin Patch":
	default:
		section.unread = true
		section.payload.SetField(ompUnreadableField, ompPathField)
	}
}

func ompHashline(event, text string) []rule.Payload {
	var (
		out     []rule.Payload
		section ompHashSection
	)

	for line := range strings.SplitSeq(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "*** End Patch" || trimmed == "*** Abort" ||
			trimmed == "[*** End Patch]" || trimmed == "[*** Abort]" {
			break
		}

		if ompSnapshot.MatchString(trimmed) {
			if section.source != "" {
				out = append(out, section.finish())
			}

			section = ompHashSection{
				payload: ompEdit(event), source: ompUnwrap(trimmed), added: nil, body: false, unread: false,
			}
			section.payload.SetField(ompPathField, section.source)
		} else if section.source != "" {
			section.read(line, trimmed)
		}
	}

	if section.source != "" {
		out = append(out, section.finish())
	}

	return out
}

func ompTasks(event string, input map[string]any) []rule.Payload {
	if raw, present := input["tasks"]; present {
		entries, ok := raw.([]any)
		if !ok || len(entries) == 0 {
			return []rule.Payload{ompTask(event, nil, input, true)}
		}

		out := make([]rule.Payload, 0, len(entries))
		for _, raw := range entries {
			entry, _ := raw.(map[string]any)
			out = append(out, ompTask(event, entry, input, true))
		}

		return out
	}

	return []rule.Payload{ompTask(event, input, nil, false)}
}

func ompTask(event string, entry, batch map[string]any, batched bool) rule.Payload {
	payload := rule.Payload{Event: event, Kind: ompKindAgent, StopHookActive: false}
	payload.SetField("tool", ompTaskTool)

	if entry == nil {
		for _, name := range []string{ompAgentTypeField, ompAgentPromptField, ompModelField} {
			payload.SetField(ompUnreadableField, name)
		}

		return payload
	}

	set(&payload, ompAgentTypeField, entry, ompKindAgent)
	set(&payload, ompAgentPromptField, entry, ompTaskTool)

	if batched {
		ompTaskContext(&payload, entry, batch)
	}

	ompTaskModels(&payload, entry)

	return payload
}

func ompTaskContext(payload *rule.Payload, entry, batch map[string]any) {
	task, valid := entry[ompTaskTool].(string)
	if !valid || task == "" {
		payload.SetField(ompUnreadableField, ompAgentPromptField)
	}

	raw, present := batch["context"]
	if !present {
		return
	}

	context, ok := raw.(string)
	if !ok {
		payload.SetField(ompUnreadableField, ompAgentPromptField)

		return
	}

	if valid {
		payload.SetField(ompAgentPromptField, context+"\n\n"+task)
	} else if _, present := entry[ompTaskTool]; !present {
		payload.SetField(ompAgentPromptField, context)
	}
}

func ompTaskModels(payload *rule.Payload, entry map[string]any) {
	models, ok := entry[ompModelField].([]any)
	if !ok {
		set(payload, ompModelField, entry, ompModelField)

		return
	}

	var values []string

	invalid := len(models) == 0
	for _, raw := range models {
		if model, ok := raw.(string); ok {
			values = append(values, model)
		} else {
			invalid = true
		}
	}

	payload.SetModels(values)

	if invalid {
		payload.SetField(ompUnreadableField, ompModelField)
	}
}
