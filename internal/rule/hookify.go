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

// Why an upstream file was skipped, or the import could not start.
var (
	errNoHookifyRules      = errors.New("no hookify.*.local.md rules")
	errTierHas             = errors.New("the Project-personal tier already has")
	errNoName              = errors.New("rule has no name")
	errUnusableName        = errors.New("is not a usable filename")
	errNoConditions        = errors.New("rule has no conditions, which upstream never matches")
	errNoCanonicalField    = errors.New("has no canonical equivalent")
	errUnexpressedOperator = errors.New("is not one handrail expresses")
	errEventSpan           = errors.New("conditions span more than one event")
	errMatcherEvent        = errors.New("cannot apply to event")
	errToolNoKind          = errors.New("names a tool with no canonical kind")
	errKindSpan            = errors.New("spans more than one tool kind")
	errKindConflict        = errors.New("and the rest of the rule names")
)

// The modes of a file handrail writes into a project, an imported rule or the
// exclude file, and of a directory it creates for one: git's own.
const (
	dirMode  = 0o755
	fileMode = 0o644
)

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
		target, err := importOne(s, dst)

		res := Imported{Source: s, Target: target, Reason: ""}
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
		return nil, fmt.Errorf("%w in %s", errNoHookifyRules, src)
	}

	return files, nil
}

// writeNew writes content to path unless something is already there. O_EXCL is
// what makes a re-run safe: the check and the write are one operation, so a
// second pass cannot overwrite a rule the user has since edited by hand.
func writeNew(path, content string) error {
	err := os.MkdirAll(filepath.Dir(path), dirMode)
	if err != nil {
		return err
	}

	out, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, fileMode)
	if errors.Is(err, fs.ErrExist) {
		return fmt.Errorf("%w %s", errTierHas, filepath.Base(path))
	}

	if err != nil {
		return err
	}

	_, err = out.WriteString(content)
	if err != nil {
		_ = out.Close()

		return err
	}

	return out.Close()
}

// eventKind is where a converted rule fires: its canonical event, and the tool
// kind on it, if any.
type eventKind struct{ event, kind string }

// String is the event and kind as a skip reason names them.
func (ek eventKind) String() string {
	if ek.kind == "" {
		return ek.event
	}

	return ek.event + " " + ek.kind
}

// term is one converted condition: the canonical field, operator, and value,
// plus the upstream field it came from and the event that field belongs to,
// which is what an all rule's inference reads.
type term struct {
	upstream string
	field    string
	op       string
	value    string
	where    eventKind
}

// importOne converts one upstream file and writes the handrail rule file under
// dst, returning the path it wrote.
func importOne(path, dst string) (string, error) {
	upstream, err := readHookify(path)
	if err != nil {
		return "", err
	}

	name := upstream.name
	if name == "" {
		return "", errNoName
	}

	if !isRuleName(name) {
		return "", fmt.Errorf("name %q %w", name, errUnusableName)
	}

	terms, err := hookifyConditions(upstream.conditions, upstream.event, upstream.pattern)
	if err != nil {
		return "", err
	}

	where, err := hookifyEvent(upstream.event, terms)
	if err != nil {
		return "", err
	}
	// The canonical event, not the upstream one: an all rule has no upstream
	// event to weigh a tool matcher against, and the inferred one is what the
	// converted rule will actually carry.
	where.kind, err = hookifyToolMatcher(upstream.toolMatcher, where)
	if err != nil {
		return "", err
	}
	// Upstream treats anything that is not "block" as a warning.
	action := upstream.action
	if action != "block" {
		action = Warn.String()
	}

	file := renderRule(where, action, upstream.enabled, terms, strings.TrimSpace(upstream.body))
	// The converted file is parsed back before it is written: an import that
	// needs hand-fixing before handrail check passes is not an import.
	_, err = Parse(name, []byte(file))
	if err != nil {
		return "", fmt.Errorf("converted rule is invalid: %w", err)
	}

	target := filepath.Join(dst, name+".md")

	err = writeNew(target, file)
	if err != nil {
		return "", err
	}

	return target, nil
}

// hookifyFile is one upstream file as far as the Importer reads it.
type hookifyFile struct {
	name, event, pattern, action, toolMatcher string
	conditions                                *node
	body                                      string
	enabled                                   bool
}

// readHookify reads one upstream file's frontmatter and body.
func readHookify(path string) (*hookifyFile, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	doc, body, err := parseFrontmatter(data)
	if err != nil {
		return nil, err
	}

	upstream := &hookifyFile{
		name: "", event: "", pattern: "", action: "", toolMatcher: "",
		conditions: nil, body: body, enabled: true,
	}
	for _, entry := range doc.mapping {
		err := upstream.set(entry)
		if err != nil {
			return nil, err
		}
	}

	return upstream, nil
}

// set reads one upstream frontmatter field.
func (h *hookifyFile) set(entry pair) error {
	switch entry.key {
	case "name":
		return scalarInto(entry, &h.name)
	case "event":
		return scalarInto(entry, &h.event)
	case "pattern":
		return scalarInto(entry, &h.pattern)
	case keyAction:
		return scalarInto(entry, &h.action)
	case "tool_matcher":
		return scalarInto(entry, &h.toolMatcher)
	case "enabled":
		var value string

		err := scalarInto(entry, &value)
		if err != nil {
			return err
		}
		// Upstream lowercases before comparing, so False and FALSE
		// disable a rule there. Reading them as enabled would arm a
		// guardrail its author had switched off.
		h.enabled = !strings.EqualFold(value, "false")
	case "conditions":
		h.conditions = entry.val
		if entry.val.seq == nil {
			return fmt.Errorf("line %d: conditions %w", entry.line, errNotList)
		}
	}
	// Upstream ignores a key it does not know, so converting cannot lose
	// meaning by ignoring it too.
	return nil
}

// isRuleName reports whether name can be a rule file's basename. Identity is the
// filename, so a name carrying a separator or a leading dot would name a file
// nobody asked for.
func isRuleName(name string) bool {
	if strings.HasPrefix(name, ".") {
		return false
	}

	for i := range len(name) {
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
		return hookifyPattern(event, pattern)
	}

	terms := make([]term, 0, len(list.seq))
	for _, item := range list.seq {
		t, err := hookifyCondition(item)
		if err != nil {
			return nil, err
		}

		terms = append(terms, t)
	}

	return terms, nil
}

// hookifyPattern converts the pattern shorthand into its one condition.
func hookifyPattern(event, pattern string) ([]term, error) {
	if pattern == "" {
		return nil, errNoConditions
	}
	// Upstream's own inference, verbatim, which docs/spec.md section 8 asks
	// for. On a prompt or stop rule it names content, a field neither event
	// carries: the shorthand is inert upstream too, and inventing a field
	// the author never wrote would import a guardrail they never had.
	field := fieldContent

	switch event {
	case "bash":
		field = fieldCommand
	case "file":
		field = "new_text"
	}

	t, err := convertCondition(field, "regex_match", pattern)
	if err != nil {
		return nil, err
	}

	return []term{t}, nil
}

// hookifyCondition converts one entry of the condition list.
func hookifyCondition(item *node) (term, error) {
	if !item.isMapping() {
		return term{}, fmt.Errorf("line %d: condition %w", item.line, errNotMapping)
	}

	field, operator, value := "", "regex_match", ""

	for _, entry := range item.mapping {
		var err error

		switch entry.key {
		case "field":
			err = scalarInto(entry, &field)
		case "operator":
			err = scalarInto(entry, &operator)
		case "pattern":
			err = scalarInto(entry, &value)
		}

		if err != nil {
			return term{}, err
		}
	}

	return convertCondition(field, operator, value)
}

// convertCondition maps one upstream field and operator onto handrail's.
func convertCondition(field, operator, value string) (term, error) {
	canonical, where, known := hookifyField(field)
	if !known {
		return term{}, fmt.Errorf("condition field %q %w", field, errNoCanonicalField)
	}

	converted, known := hookifyOperator(operator)
	if !known {
		return term{}, fmt.Errorf("condition operator %q %w", operator, errUnexpressedOperator)
	}

	if converted == opMatches {
		// Upstream compiles every pattern with IGNORECASE, so the case-sensitive
		// default here would quietly narrow what the rule catches. The reported
		// pattern stays the one the author wrote.
		_, err := regexp.Compile("(?i)" + value)
		if err != nil {
			return term{}, fmt.Errorf("pattern %q is not an RE2 regexp: %w", value, err)
		}

		value = "(?i)" + value
	}

	return term{upstream: field, field: canonical, op: converted, value: value, where: where}, nil
}

// hookifyField maps an upstream condition field onto the canonical field, and
// onto the event and tool kind whose payload carries it. One table for both,
// because the second one is only ever asked about a field the first accepted.
// new_string is upstream's own alias for new_text, extracted identically there.
func hookifyField(field string) (string, eventKind, bool) {
	switch field {
	case "command":
		return fieldCommand, eventKind{eventPreToolUse, kindShell}, true
	case "file_path":
		return fieldPath, eventKind{eventPreToolUse, kindFileEdit}, true
	case "new_text", "new_string", "content":
		return fieldContent, eventKind{eventPreToolUse, kindFileEdit}, true
	case "user_prompt":
		return fieldPrompt, eventKind{eventUserPromptSubmit, ""}, true
	}

	return "", eventKind{"", ""}, false
}

// hookifyOperator maps the operators upstream evaluates. String operators copy
// verbatim: upstream's are case-sensitive already. Anything else is a no-match
// upstream, so converting it would invent a guardrail the user never had.
func hookifyOperator(operator string) (string, bool) {
	switch operator {
	case "regex_match":
		return opMatches, true
	case opContains, "not_contains", opEquals, opStartsWith, opEndsWith:
		return operator, true
	}

	return "", false
}

// hookifyEvent maps the upstream event, or infers it from the condition fields
// when the rule is an all rule, which is upstream's default.
func hookifyEvent(event string, terms []term) (eventKind, error) {
	switch event {
	case "bash":
		return eventKind{eventPreToolUse, kindShell}, nil
	case "file":
		return eventKind{eventPreToolUse, kindFileEdit}, nil
	case "prompt":
		return eventKind{eventUserPromptSubmit, ""}, nil
	case "stop":
		return eventKind{eventStop, ""}, nil
	case "", "all":
		return inferEvent(terms)
	}

	return eventKind{}, fmt.Errorf("%w event %q", errUnknown, event)
}

// inferEvent reads an all rule's event from its condition fields.
func inferEvent(terms []term) (eventKind, error) {
	// Upstream fires an all rule only where its fields exist, so the fields are
	// the rule's real event. Fields spanning two events name no single one.
	var (
		where eventKind
		from  string
	)

	for _, cond := range terms {
		if where.event == "" {
			where, from = cond.where, cond.upstream

			continue
		}

		if cond.where != where {
			return eventKind{}, fmt.Errorf("%w: %q names %s and %q names %s",
				errEventSpan, from, where, cond.upstream, cond.where)
		}
	}

	return where, nil
}

// hookifyToolMatcher narrows the rule's kind when an upstream tool_matcher
// names tools that share one. A matcher naming a tool with no canonical kind,
// or tools spanning two, is the rule's whole selection, so it cannot be
// dropped.
func hookifyToolMatcher(matcher string, where eventKind) (string, error) {
	if matcher == "" || matcher == "*" {
		return where.kind, nil
	}
	// Only a tool call carries a tool name to match against, and PreToolUse is
	// the one event this conversion produces from a tool rule.
	if where.event != eventPreToolUse {
		return "", fmt.Errorf("tool_matcher %q %w %q", matcher, errMatcherEvent, where.event)
	}

	matched := ""

	for tool := range strings.SplitSeq(matcher, "|") {
		named := toolKind(tool)
		if named == "" {
			return "", fmt.Errorf("tool_matcher %q %w: %s", matcher, errToolNoKind, tool)
		}

		if matched != "" && matched != named {
			return "", fmt.Errorf("tool_matcher %q %w", matcher, errKindSpan)
		}

		matched = named
	}

	if where.kind != "" && where.kind != matched {
		return "", fmt.Errorf("tool_matcher %q names %s, %w %s", matcher, matched, errKindConflict, where.kind)
	}

	return matched, nil
}

// toolKind classifies the tool names upstream matches by name. It is upstream's
// own list, not a harness capability table: the Adapter's classification lives
// in the harness package, which imports this one.
func toolKind(tool string) string {
	switch tool {
	case "Bash":
		return kindShell
	case "Edit", "Write", "MultiEdit", "NotebookEdit":
		return kindFileEdit
	case "Read":
		return kindFileRead
	}

	return ""
}

// renderRule writes the converted rule file. Condition values are quoted so a
// pattern carrying a comment marker, a colon, or a leading quote survives the
// round trip.
func renderRule(where eventKind, action string, enabled bool, terms []term, message string) string {
	var out strings.Builder
	out.WriteString("---\nevent: " + where.event + "\n")

	if where.kind != "" {
		out.WriteString("kind: " + where.kind + "\n")
	}

	out.WriteString("action: " + action + "\n")

	if !enabled {
		out.WriteString("enabled: false\n")
	}

	if len(terms) > 0 {
		out.WriteString("conditions:\n")

		for _, t := range terms {
			out.WriteString("  - field: " + t.field + "\n    " + t.op + ": " + quote(t.value) + "\n")
		}
	}

	out.WriteString("---\n" + message + "\n")

	return out.String()
}

// quote renders a value as a single-quoted YAML scalar, where the only escape
// is a doubled quote and a backslash means itself, which is what a regexp needs.
func quote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}
