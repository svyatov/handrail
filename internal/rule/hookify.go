package rule

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// This is the one-shot converter from upstream hookify's
// .claude/hookify.*.local.md files into Project-personal rules (ADR 0008). The
// mapping is mechanical, and everything the rule format cannot express is
// skipped with a reason rather than written as a stub that cannot work.
//
// Upstream's frontmatter is the same block-style subset this package already
// parses, so the parser is shared: a file that does not parse is one more
// reported skip, which is the strictness handrail promises anyway.

// Imported is one upstream file's outcome: converted and written to Target, or
// skipped for Reason. A skip leaves nothing behind, so the original path and
// the reason are everything a hand-port needs.
type Imported struct {
	Source string
	Target string // empty when skipped
	Reason string // empty when converted
}

// ImportHookify converts every upstream rule file at src into a Project-personal
// rule under dst. src is a directory holding hookify.*.local.md files, or one
// such file. Originals are never touched and an existing target is never
// overwritten, so re-runs are safe.
func ImportHookify(src, dst string) ([]Imported, error) {
	sources, err := hookifySources(src)
	if err != nil {
		return nil, err
	}
	out := make([]Imported, 0, len(sources))
	for _, s := range sources {
		res := Imported{Source: s}
		name, file, err := convertHookify(s)
		if err == nil {
			target := filepath.Join(dst, name+".md")
			if err = writeNew(target, file); err == nil {
				res.Target = target
			}
		}
		if err != nil {
			res.Reason = err.Error()
		}
		out = append(out, res)
	}
	return out, nil
}

// hookifySources lists the upstream files to convert, in a stable order.
func hookifySources(src string) ([]string, error) {
	fi, err := os.Stat(src)
	if err != nil {
		return nil, err
	}
	if !fi.IsDir() {
		return []string{src}, nil
	}
	// Upstream's own glob, so pointing the importer at any directory finds what
	// upstream would have loaded from it and nothing else.
	files, err := filepath.Glob(filepath.Join(src, "hookify.*.local.md"))
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no hookify.*.local.md rules in %s", src)
	}
	return files, nil
}

// writeNew writes content to path unless something is already there. O_EXCL is
// what makes a re-run safe: the check and the write are one operation, so a
// second pass cannot overwrite a rule the user has since edited by hand.
func writeNew(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("the Project-personal tier already has %s", filepath.Base(path))
	}
	if err != nil {
		return err
	}
	if _, err := f.WriteString(content); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// term is one converted condition: the canonical field, operator, and value,
// plus the upstream field it came from and the event that field belongs to,
// which is what an all rule's inference reads.
type term struct {
	upstream string
	field    string
	op       string
	value    string
	event    string
	kind     string
}

// convertHookify reads one upstream file and returns the rule's name and the
// handrail rule file to write under it.
func convertHookify(path string) (name, file string, err error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", "", err
	}
	doc, body, err := parseFrontmatter(data)
	if err != nil {
		return "", "", err
	}

	var event, pattern, action, toolMatcher string
	var conditions *node
	enabled := true
	for _, kv := range doc.mapping {
		switch kv.key {
		case "name":
			err = scalarInto(kv, &name)
		case "event":
			err = scalarInto(kv, &event)
		case "pattern":
			err = scalarInto(kv, &pattern)
		case "action":
			err = scalarInto(kv, &action)
		case "tool_matcher":
			err = scalarInto(kv, &toolMatcher)
		case "enabled":
			var v string
			if err = scalarInto(kv, &v); err == nil {
				// Upstream lowercases before comparing, so False and FALSE
				// disable a rule there. Reading them as enabled would arm a
				// guardrail its author had switched off.
				enabled = !strings.EqualFold(v, "false")
			}
		case "conditions":
			if kv.val.seq == nil {
				err = fmt.Errorf("line %d: conditions must be a list", kv.line)
			}
			conditions = kv.val
		}
		// Upstream ignores a key it does not know, so converting cannot lose
		// meaning by ignoring it too.
		if err != nil {
			return "", "", err
		}
	}

	if name == "" {
		return "", "", fmt.Errorf("rule has no name")
	}
	if !isRuleName(name) {
		return "", "", fmt.Errorf("name %q is not a usable filename", name)
	}
	terms, err := hookifyConditions(conditions, event, pattern)
	if err != nil {
		return "", "", err
	}
	ev, kind, err := hookifyEvent(event, terms)
	if err != nil {
		return "", "", err
	}
	// The canonical event, not the upstream one: an all rule has no upstream
	// event to weigh a tool matcher against, and the inferred one is what the
	// converted rule will actually carry.
	if kind, err = hookifyToolMatcher(toolMatcher, ev, kind); err != nil {
		return "", "", err
	}
	// Upstream treats anything that is not "block" as a warning.
	if action != "block" {
		action = "warn"
	}

	file = renderRule(ev, kind, action, enabled, terms, strings.TrimSpace(body))
	// The converted file is parsed back before it is written: an import that
	// needs hand-fixing before handrail check passes is not an import.
	if _, err := Parse(name, []byte(file)); err != nil {
		return "", "", fmt.Errorf("converted rule is invalid: %v", err)
	}
	return name, file, nil
}

// isRuleName reports whether name can be a rule file's basename. Identity is the
// filename, so a name carrying a separator or a leading dot would name a file
// nobody asked for.
func isRuleName(name string) bool {
	if strings.HasPrefix(name, ".") {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !isAlphanumeric(c) && c != '-' && c != '_' && c != '.' {
			return false
		}
	}
	return true
}

// hookifyConditions converts the condition list, or the pattern shorthand when
// there is no list. Upstream expands the shorthand by inferring the field from
// the event, and a rule left with no condition at all never matches there.
func hookifyConditions(list *node, event, pattern string) ([]term, error) {
	if list == nil || len(list.seq) == 0 {
		if pattern == "" {
			return nil, fmt.Errorf("rule has no conditions, which upstream never matches")
		}
		// Upstream's own inference, verbatim, which docs/spec.md section 8 asks
		// for. On a prompt or stop rule it names content, a field neither event
		// carries: the shorthand is inert upstream too, and inventing a field
		// the author never wrote would import a guardrail they never had.
		field := "content"
		switch event {
		case "bash":
			field = "command"
		case "file":
			field = "new_text"
		}
		t, err := convertCondition(field, "regex_match", pattern)
		if err != nil {
			return nil, err
		}
		return []term{t}, nil
	}

	terms := make([]term, 0, len(list.seq))
	for _, item := range list.seq {
		if item.isScalar || item.seq != nil {
			return nil, fmt.Errorf("line %d: condition must be a mapping", item.line)
		}
		field, op, value := "", "regex_match", ""
		for _, kv := range item.mapping {
			var err error
			switch kv.key {
			case "field":
				err = scalarInto(kv, &field)
			case "operator":
				err = scalarInto(kv, &op)
			case "pattern":
				err = scalarInto(kv, &value)
			}
			if err != nil {
				return nil, err
			}
		}
		t, err := convertCondition(field, op, value)
		if err != nil {
			return nil, err
		}
		terms = append(terms, t)
	}
	return terms, nil
}

// convertCondition maps one upstream field and operator onto handrail's.
func convertCondition(field, op, value string) (term, error) {
	canonical, event, kind, ok := hookifyField(field)
	if !ok {
		return term{}, fmt.Errorf("condition field %q has no canonical equivalent", field)
	}
	converted, ok := hookifyOperator(op)
	if !ok {
		return term{}, fmt.Errorf("condition operator %q is not one handrail expresses", op)
	}
	if converted == "matches" {
		// Upstream compiles every pattern with IGNORECASE, so the case-sensitive
		// default here would quietly narrow what the rule catches. The reported
		// pattern stays the one the author wrote.
		if _, err := regexp.Compile("(?i)" + value); err != nil {
			return term{}, fmt.Errorf("pattern %q is not an RE2 regexp: %v", value, err)
		}
		value = "(?i)" + value
	}
	return term{upstream: field, field: canonical, op: converted, value: value, event: event, kind: kind}, nil
}

// hookifyField maps an upstream condition field onto the canonical field, and
// onto the event and tool kind whose payload carries it. One table for both,
// because the second one is only ever asked about a field the first accepted.
// new_string is upstream's own alias for new_text, extracted identically there.
func hookifyField(field string) (canonical, event, kind string, ok bool) {
	switch field {
	case "command":
		return "command", "PreToolUse", "shell", true
	case "file_path":
		return "path", "PreToolUse", "file_edit", true
	case "new_text", "new_string", "content":
		return "content", "PreToolUse", "file_edit", true
	case "user_prompt":
		return "prompt", "UserPromptSubmit", "", true
	}
	return "", "", "", false
}

// hookifyOperator maps the operators upstream evaluates. String operators copy
// verbatim: upstream's are case-sensitive already. Anything else is a no-match
// upstream, so converting it would invent a guardrail the user never had.
func hookifyOperator(op string) (string, bool) {
	switch op {
	case "regex_match":
		return "matches", true
	case "contains", "not_contains", "equals", "starts_with", "ends_with":
		return op, true
	}
	return "", false
}

// hookifyEvent maps the upstream event, or infers it from the condition fields
// when the rule is an all rule, which is upstream's default.
func hookifyEvent(event string, terms []term) (canonical, kind string, err error) {
	switch event {
	case "bash":
		return "PreToolUse", "shell", nil
	case "file":
		return "PreToolUse", "file_edit", nil
	case "prompt":
		return "UserPromptSubmit", "", nil
	case "stop":
		return "Stop", "", nil
	case "", "all":
	default:
		return "", "", fmt.Errorf("unknown event %q", event)
	}

	// Upstream fires an all rule only where its fields exist, so the fields are
	// the rule's real event. Fields spanning two events name no single one.
	var from string
	for _, t := range terms {
		if canonical == "" {
			canonical, kind, from = t.event, t.kind, t.upstream
			continue
		}
		if t.event != canonical || t.kind != kind {
			return "", "", fmt.Errorf("conditions span more than one event: %q names %s and %q names %s",
				from, eventKind(canonical, kind), t.upstream, eventKind(t.event, t.kind))
		}
	}
	return canonical, kind, nil
}

func eventKind(event, kind string) string {
	if kind == "" {
		return event
	}
	return event + " " + kind
}

// hookifyToolMatcher narrows the kind when an upstream tool_matcher names tools
// that share one. A matcher naming a tool with no canonical kind, or tools
// spanning two, is the rule's whole selection, so it cannot be dropped.
func hookifyToolMatcher(matcher, event, kind string) (string, error) {
	if matcher == "" || matcher == "*" {
		return kind, nil
	}
	// Only a tool call carries a tool name to match against, and PreToolUse is
	// the one event this conversion produces from a tool rule.
	if event != "PreToolUse" {
		return "", fmt.Errorf("tool_matcher %q cannot apply to event %q", matcher, event)
	}
	matched := ""
	for _, tool := range strings.Split(matcher, "|") {
		k := toolKind(tool)
		if k == "" {
			return "", fmt.Errorf("tool_matcher %q names a tool with no canonical kind: %s", matcher, tool)
		}
		if matched != "" && matched != k {
			return "", fmt.Errorf("tool_matcher %q spans more than one tool kind", matcher)
		}
		matched = k
	}
	if kind != "" && kind != matched {
		return "", fmt.Errorf("tool_matcher %q names %s, and the rest of the rule names %s", matcher, matched, kind)
	}
	return matched, nil
}

// toolKind classifies the tool names upstream matches by name. It is upstream's
// own list, not a harness capability table: the Adapter's classification lives
// in the harness package, which imports this one.
func toolKind(tool string) string {
	switch tool {
	case "Bash":
		return "shell"
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return "file_edit"
	case "Read":
		return "file_read"
	}
	return ""
}

// renderRule writes the converted rule file. Condition values are quoted so a
// pattern carrying a comment marker, a colon, or a leading quote survives the
// round trip.
func renderRule(event, kind, action string, enabled bool, terms []term, message string) string {
	var b strings.Builder
	b.WriteString("---\nevent: " + event + "\n")
	if kind != "" {
		b.WriteString("kind: " + kind + "\n")
	}
	b.WriteString("action: " + action + "\n")
	if !enabled {
		b.WriteString("enabled: false\n")
	}
	if len(terms) > 0 {
		b.WriteString("conditions:\n")
		for _, t := range terms {
			b.WriteString("  - field: " + t.field + "\n    " + t.op + ": " + quote(t.value) + "\n")
		}
	}
	b.WriteString("---\n" + message + "\n")
	return b.String()
}

// quote renders a value as a single-quoted YAML scalar, where the only escape
// is a doubled quote and a backslash means itself, which is what a regexp needs.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
