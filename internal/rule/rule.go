// Package rule parses handrail rule files and discovers the tiers they live in.
package rule

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"slices"
	"strings"
)

// The fixed part of each parse error, which the message wraps with the line
// and the value it is about. The ones shared across the package's files are
// here with the rest.
var (
	errDuplicateField          = errors.New("duplicate field")
	errUnknown                 = errors.New("unknown")
	errNotList                 = errors.New("must be a list")
	errNotMapping              = errors.New("must be a mapping")
	errNotSingle               = errors.New("must be a single value")
	errNotBool                 = errors.New("must be true or false")
	errNeverCarries            = errors.New("never carries")
	errKindNotTool             = errors.New("kind applies only to PreToolUse and PostToolUse")
	errMissingEvent            = errors.New(`missing required field "event"`)
	errNoMessage               = errors.New("rule has no message")
	errAgentOnlyNotWarn        = errors.New("agent_only applies only to warn")
	errAgentOnlyRefused        = errors.New("agent_only is refused")
	errAskNotPreToolUse        = errors.New("ask applies only to PreToolUse")
	errTrialDisabled           = errors.New("trial applies only to an enabled rule")
	errBlockRefused            = errors.New("block is refused")
	errNoFrontmatter           = errors.New("missing YAML frontmatter")
	errUnterminatedFrontmatter = errors.New("unterminated YAML frontmatter")
	errAnyNotAlone             = errors.New("any must be a condition's only key")
	errAnyNested               = errors.New("any groups cannot nest")
	errNoField                 = errors.New("condition has no field")
	errNoOperator              = errors.New("condition has no operator")
	errOperators               = errors.New("operators")
	errNotClean                = errors.New("is not clean")
	errTrailingBackslash       = errors.New("trailing backslash")
	errEmptyClass              = errors.New("empty character class")
	errUnterminatedClass       = errors.New("unterminated character class")
)

// The vocabulary the package spells more than once: the events, kinds, fields
// and operators a rule names, and the frontmatter keys read in several places.
const (
	eventPreToolUse       = "PreToolUse"
	eventPostToolUse      = "PostToolUse"
	eventUserPromptSubmit = "UserPromptSubmit"
	eventSessionStart     = "SessionStart"
	eventSessionEnd       = "SessionEnd"
	eventStop             = "Stop"
	eventSubagentStart    = "SubagentStart"
	eventSubagentStop     = "SubagentStop"

	kindShell    = "shell"
	kindFileEdit = "file_edit"
	kindFileRead = "file_read"

	fieldCommand      = "command"
	fieldPath         = "path"
	fieldContent      = "content"
	fieldTool         = "tool"
	fieldPrompt       = "prompt"
	fieldResponse     = "response"
	fieldAgentType    = "agent_type"
	fieldURL          = "url"
	fieldNetworkGrant = "network_grant"
	fieldUnreadable   = "unreadable"

	opMatches    = "matches"
	opContains   = "contains"
	opEquals     = "equals"
	opStartsWith = "starts_with"
	opEndsWith   = "ends_with"
	opGlob       = "glob"

	keyKind   = "kind"
	keyAction = "action"
)

// Outcome is the Action and Outcome vocabulary, one type for both: an Outcome
// takes the strongest Action among the matched rules, so warn, ask and block
// are the same values under either name. The constants run in the order of the
// gate, allow < warn < ask < block, so the strongest of several is their max.
// Allow is the absence of a match, and no Rule.Action holds it.
type Outcome int

// The four Outcomes, weakest first.
const (
	Allow Outcome = iota
	Warn
	Ask
	Block
)

// String is the Outcome as a rule file and every report spell it.
func (o Outcome) String() string { return [...]string{"allow", "warn", "ask", "block"}[o] }

// Rule is one guardrail: a matcher (event, kind, conditions) and the message
// delivered when it matches. Identity is the file's basename.
type Rule struct {
	Name       string
	Path       string
	Tier       string
	ShadowedBy *Rule // the higher-tier rule replacing this one, if any
	Replaces   *Rule // the lower-tier rule this one shadows, if any
	DroppedBy  *Rule // the Global rule this Project-shared one may not replace
	// DemotedFrom is the tier this rule's path names when its supply put it in
	// another: Project-personal for a .handrail/local/ the repository supplies.
	DemotedFrom string
	Event       string
	Kind        string
	Message     string
	Conditions  []Condition
	Examples    []Example
	// fields names each field the conditions test once, in the order evaluation
	// chooses a Candidate for them. A Term's slot is its field's index here.
	fields []string
	Action Outcome
	// agentOnlyLine is where agent_only was set, for the tier's check.
	agentOnlyLine int
	Enabled       bool
	// AgentOnly withholds the human's line, which a warn alone may do.
	AgentOnly bool
	// LostAgentOnly marks a Project-shared rule that set agent_only, which
	// that tier refuses: the rule stays, and the human hears it.
	LostAgentOnly bool
	// Trial evaluates the rule and delivers nothing (ADR 0016).
	Trial bool
	// trialLine is where trial was set, for the disabled rule it is refused on.
	trialLine int
}

// Live reports whether this rule can fire: enabled, not shadowed by a higher
// tier, and not dropped. A rule that is loaded is not thereby a rule that
// enforces anything, and every caller asking which is which asks here. check
// reads the fields directly, because it reports the distinction rather than
// acts on it.
func (r *Rule) Live() bool { return r.Enabled && r.ShadowedBy == nil && r.DroppedBy == nil }

// Condition is one entry of a rule's implicit-AND condition list: one or more
// Terms, which OR together. A bare field test parses to a one-Term condition
// and an any group to a many-Term one, so nothing downstream has to ask which
// way the rule file wrote it.
type Condition struct {
	Terms []Term
}

// Term is one operator applied to one canonical payload field. re is compiled
// by the parser for the two pattern operators, matches and glob, in either
// polarity, and is unexported because a pattern reaching matches without having
// gone through the parser is a pattern nothing validated. That leaves the
// parser as the only place a pattern Term can be built: a hand-built one is a
// nil re, and there is no spelling of it that is merely wrong instead.
type Term struct {
	re    *regexp.Regexp
	Field string
	Op    string
	Value string
	line  int
	slot  int
}

// The closed sets below are switches and functions, not package variables: the
// hook path pays for every byte of startup work, and code the linker lays out
// has no init to run before main.

// IsEvent reports whether name is one of the eight core events. Which of them a
// harness has, and what a hook can do on each, is its Adapter's Capability
// matrix.
func IsEvent(name string) bool {
	switch name {
	case eventPreToolUse, eventPostToolUse, eventUserPromptSubmit, eventSessionStart, eventSessionEnd,
		eventStop, eventSubagentStart, eventSubagentStop:
		return true
	}

	return false
}

// IsKind reports whether name is a canonical tool kind.
func IsKind(name string) bool {
	switch name {
	case kindShell, kindFileEdit, kindFileRead, "mcp", "agent", "network", "other":
		return true
	}

	return false
}

// IsField reports whether name is a canonical payload field a condition may
// address. raw.* is a v1 non-goal, so this set is closed.
func IsField(name string) bool {
	switch name {
	case fieldCommand, fieldPath, fieldContent, "removed_content", "writes_empty", "deletes",
		"server", fieldTool, fieldPrompt, fieldResponse, fieldAgentType, "agent_prompt", "model",
		fieldURL, "domain", fieldNetworkGrant, "unsandboxed", fieldUnreadable:
		return true
	}

	return false
}

// operators is the condition vocabulary, and a list rather than a switch so a
// test can walk it. Parsing an operator is only half of supporting one: the
// other half is Term.matches, whose switch has no default and cannot have one
// that fails loudly on the hot path. A seventh operator added here alone would
// parse clean, pass check, and then never match, or, negated, always match.
// Walking this list is what turns that into a test failure.
func operators() []string {
	return []string{opMatches, opContains, opEquals, opStartsWith, opEndsWith, opGlob}
}

// isOperator reports whether key is a condition operator, negated or not.
func isOperator(key string) bool {
	return slices.Contains(operators(), strings.TrimPrefix(key, "not_"))
}

// Parse reads one rule file. name is the rule's identity, the file's basename
// without its extension.
func Parse(name string, data []byte) (*Rule, error) {
	doc, body, err := parseFrontmatter(data)
	if err != nil {
		return nil, err
	}

	parsed := &Rule{
		Name: name, Path: "", Tier: "", ShadowedBy: nil, Replaces: nil, DroppedBy: nil, DemotedFrom: "",
		Event: "", Kind: "", Message: strings.TrimSpace(body), Conditions: nil, Examples: nil, fields: nil,
		Action: Warn, agentOnlyLine: 0, Enabled: true, AgentOnly: false, LostAgentOnly: false, Trial: false, trialLine: 0,
	}
	seen := make(map[string]bool, len(doc.mapping))

	var kindLine, actionLine int

	for _, entry := range doc.mapping {
		if seen[entry.key] {
			return nil, fmt.Errorf("line %d: %w %q", entry.line, errDuplicateField, entry.key)
		}

		seen[entry.key] = true
		switch entry.key {
		case keyKind:
			kindLine = entry.line
		case keyAction:
			actionLine = entry.line
		}

		err = parsed.setField(entry)
		if err != nil {
			return nil, err
		}
	}

	err = parsed.checkEvent(kindLine, actionLine)
	if err != nil {
		return nil, err
	}

	err = parsed.checkRequired()
	if err != nil {
		return nil, err
	}

	return parsed, nil
}

// setField reads one frontmatter field into the rule.
func (r *Rule) setField(entry pair) error {
	var err error

	switch entry.key {
	case "event":
		return oneOf(entry, &r.Event, IsEvent)
	case keyKind:
		return oneOf(entry, &r.Kind, IsKind)
	case keyAction:
		return r.setAction(entry)
	case "enabled":
		return boolInto(entry, &r.Enabled)
	case "agent_only":
		r.agentOnlyLine = entry.line

		return boolInto(entry, &r.AgentOnly)
	case "trial":
		r.trialLine = entry.line

		return boolInto(entry, &r.Trial)
	case "conditions":
		return r.setConditions(entry)
	case "examples":
		r.Examples, err = parseExamples(entry)

		return err
	}

	return fmt.Errorf("line %d: %w frontmatter field %q", entry.line, errUnknown, entry.key)
}

// oneOf is scalarInto for a field whose value must be one of a closed set.
func oneOf(entry pair, dst *string, valid func(string) bool) error {
	err := scalarInto(entry, dst)
	if err != nil {
		return err
	}

	if !valid(*dst) {
		return fmt.Errorf("line %d: %w %s %q", entry.line, errUnknown, entry.key, *dst)
	}

	return nil
}

// setAction reads the action, which is any Outcome but allow: allow is the
// absence of a match, not something a rule can do.
func (r *Rule) setAction(entry pair) error {
	var value string

	err := scalarInto(entry, &value)
	if err != nil {
		return err
	}

	for action := Warn; action <= Block; action++ {
		if action.String() == value {
			r.Action = action

			return nil
		}
	}

	return fmt.Errorf("line %d: %w action %q", entry.line, errUnknown, value)
}

// setConditions reads the condition list and gives each Term the slot of its
// field, adding the field to r.fields the first time a Term names it.
func (r *Rule) setConditions(entry pair) error {
	if entry.val.seq == nil {
		return fmt.Errorf("line %d: conditions %w", entry.line, errNotList)
	}

	var err error

	r.Conditions, err = parseConditions(entry.val)
	if err != nil {
		return err
	}

	for _, c := range r.Conditions {
		for i := range c.Terms {
			t := &c.Terms[i]

			t.slot = slices.Index(r.fields, t.Field)
			if t.slot < 0 {
				t.slot = len(r.fields)
				r.fields = append(r.fields, t.Field)
			}
		}
	}

	return nil
}

// checkRequired holds an enabled rule to the two fields it cannot fire without.
func (r *Rule) checkRequired() error {
	// A disabled rule is exempt from matcher validation; whatever fields it
	// does carry Parse has already validated.
	if !r.Enabled && r.Trial {
		return fmt.Errorf("line %d: %w", r.trialLine, errTrialDisabled)
	}

	if !r.Enabled {
		return nil
	}

	if r.Event == "" {
		return fmt.Errorf("line 1: %w", errMissingEvent)
	}
	// The message is the product: a rule that matches and says nothing
	// blocks or warns with an empty reason.
	if r.Message == "" {
		return fmt.Errorf("line 1: %w", errNoMessage)
	}

	return nil
}

// checkAgentOnly holds agent_only to a warn. It runs after the tier has had its
// say, since the Project-shared tier drops the field rather than the rule. A
// denial whose reason the human cannot see is a support ticket, and an ask
// without the human has nobody to ask.
func (r *Rule) checkAgentOnly() error {
	switch {
	case r.AgentOnly && r.Action != Warn:
		return fmt.Errorf("line %d: %w", r.agentOnlyLine, errAgentOnlyNotWarn)
	case r.AgentOnly && (StopEvent(r.Event) || r.Event == eventSessionEnd):
		return fmt.Errorf("line %d: %w on %s, where a warn tells only the human",
			r.agentOnlyLine, errAgentOnlyRefused, r.Event)
	}

	return nil
}

// StopEvent reports whether event ends a turn, where any message to the agent
// makes it continue.
func StopEvent(event string) bool { return event == eventStop || event == eventSubagentStop }

// checkEvent holds the kind, the action, the conditions and the Examples to
// what the rule's event can carry, and gives each Example the kind it takes:
// the one it writes, else the rule's. With neither, the spec's other and no
// kind read alike, since only a rule naming a kind reads one. A rule with no
// event is a disabled stub, with no event to hold anything to.
func (r *Rule) checkEvent(kindLine, actionLine int) error {
	if r.Event == "" {
		return nil
	}

	tool := ToolEvent(r.Event)
	if r.Kind != "" && !tool {
		return fmt.Errorf("line %d: %w", kindLine, errKindNotTool)
	}
	// Only a tool call not yet made is something a human can approve.
	if r.Action == Ask && r.Event != eventPreToolUse {
		return fmt.Errorf("line %d: %w", actionLine, errAskNotPreToolUse)
	}
	// A block no harness honours is a mislabelled warn. A block one harness
	// lacks degrades there instead.
	switch r.Event {
	case eventPostToolUse, eventSessionStart, eventSessionEnd, eventSubagentStart:
		if r.Action == Block {
			return fmt.Errorf("line %d: %w on %s, where no harness can deny", actionLine, errBlockRefused, r.Event)
		}
	}

	err := r.checkConditions()
	if err != nil {
		return err
	}

	return r.checkExamples()
}

// checkConditions holds every Term to a field the rule's event carries.
func (r *Rule) checkConditions() error {
	for _, c := range r.Conditions {
		for _, t := range c.Terms {
			if !carries(r.Event, t.Field) {
				return fmt.Errorf("line %d: %s %w %s", t.line, r.Event, errNeverCarries, t.Field)
			}
		}
	}

	return nil
}

// checkExamples holds every Example to a call the rule's event could carry,
// and gives each the kind it takes.
func (r *Rule) checkExamples() error {
	for i := range r.Examples {
		example := &r.Examples[i]

		err := CheckCall(r.Event, example.Fields)
		if err != nil {
			return fmt.Errorf("line %d: %w", example.Line, err)
		}

		if example.Kind == "" {
			example.Kind = r.Kind
		}
	}

	return nil
}

// ToolEvent reports whether event is about a tool call, the only events that
// carry a kind.
func ToolEvent(event string) bool { return event == eventPreToolUse || event == eventPostToolUse }

// carries is the per-event field table: whether a payload of event can hold
// field on either harness. unreadable is on every event, since any read can
// fail.
func carries(event, field string) bool {
	switch {
	case field == fieldUnreadable:
		return true
	case event == eventUserPromptSubmit:
		return field == fieldPrompt
	case event == eventStop:
		return field == fieldResponse
	case event == eventSubagentStart:
		return field == fieldAgentType
	case event == eventSubagentStop:
		return field == fieldAgentType || field == fieldResponse
	}

	return ToolEvent(event) && field != fieldPrompt
}

// parseFrontmatter reads a markdown file's YAML frontmatter as a mapping, and
// returns it with the body below. The Importer reads upstream files with it too:
// their frontmatter is the same block-style subset.
func parseFrontmatter(data []byte) (*node, string, error) {
	text := strings.TrimPrefix(strings.ReplaceAll(string(data), "\r\n", "\n"), "\ufeff")

	lines := strings.Split(text, "\n")
	if !isFence(lines[0]) {
		return nil, "", fmt.Errorf("line 1: %w", errNoFrontmatter)
	}

	end := slices.IndexFunc(lines[1:], isFence) + 1
	if end == 0 {
		return nil, "", fmt.Errorf("line 1: %w", errUnterminatedFrontmatter)
	}
	// parseYAML rewrites the lines it is handed in place, and the body's lie
	// past them, so the shared array is safe to hand it.
	body := strings.Join(lines[end+1:], "\n")

	doc, err := parseYAML(lines[1:end])
	if err != nil {
		return nil, "", err
	}

	if !doc.isMapping() {
		return nil, "", fmt.Errorf("line 2: frontmatter %w", errNotMapping)
	}

	return doc, body, nil
}

// isFence reports whether line is the "---" that opens or closes frontmatter.
func isFence(line string) bool { return strings.TrimRight(line, " ") == "---" }

func scalarInto(kv pair, dst *string) error {
	if !kv.val.isScalar {
		return fmt.Errorf("line %d: %s %w", kv.line, kv.key, errNotSingle)
	}

	*dst = kv.val.scalar

	return nil
}

func boolInto(entry pair, dst *bool) error {
	var value string

	err := scalarInto(entry, &value)
	if err != nil {
		return err
	}

	switch value {
	case "true":
		*dst = true
	case "false":
		*dst = false
	default:
		return fmt.Errorf("line %d: %s %w", entry.line, entry.key, errNotBool)
	}

	return nil
}

func parseConditions(list *node) ([]Condition, error) {
	out := make([]Condition, 0, len(list.seq))
	for _, item := range list.seq {
		if !item.isMapping() {
			return nil, fmt.Errorf("line %d: condition %w", item.line, errNotMapping)
		}

		if !item.has("any") {
			t, err := parseTerm(item)
			if err != nil {
				return nil, err
			}

			out = append(out, Condition{Terms: []Term{*t}})

			continue
		}

		c, err := parseAny(item)
		if err != nil {
			return nil, err
		}

		out = append(out, c)
	}

	return out, nil
}

// parseAny reads a condition that is an any group: one Term per entry.
func parseAny(item *node) (Condition, error) {
	if len(item.mapping) != 1 {
		return Condition{}, fmt.Errorf("line %d: %w", item.line, errAnyNotAlone)
	}

	group := item.mapping[0]
	if group.val.seq == nil {
		return Condition{}, fmt.Errorf("line %d: any %w", group.line, errNotList)
	}

	terms := make([]Term, 0, len(group.val.seq))
	for _, sub := range group.val.seq {
		if !sub.isMapping() {
			return Condition{}, fmt.Errorf("line %d: condition %w", sub.line, errNotMapping)
		}

		if sub.has("any") {
			return Condition{}, fmt.Errorf("line %d: %w", sub.line, errAnyNested)
		}

		t, err := parseTerm(sub)
		if err != nil {
			return Condition{}, err
		}

		terms = append(terms, *t)
	}

	return Condition{Terms: terms}, nil
}

func parseTerm(item *node) (*Term, error) {
	parsed := &Term{re: nil, Field: "", Op: "", Value: "", line: 0, slot: 0}

	var ops []string

	seen := make(map[string]bool, len(item.mapping))
	for _, entry := range item.mapping {
		if seen[entry.key] {
			return nil, fmt.Errorf("line %d: %w %q", entry.line, errDuplicateField, entry.key)
		}

		seen[entry.key] = true
		if !entry.val.isScalar {
			return nil, fmt.Errorf("line %d: %s %w", entry.line, entry.key, errNotSingle)
		}

		switch {
		case entry.key == "field":
			if !IsField(entry.val.scalar) {
				return nil, fmt.Errorf("line %d: %w condition field %q", entry.line, errUnknown, entry.val.scalar)
			}

			parsed.Field = entry.val.scalar
		case isOperator(entry.key):
			ops = append(ops, entry.key)
			parsed.Op, parsed.Value, parsed.line = entry.key, entry.val.scalar, entry.line
		default:
			return nil, fmt.Errorf("line %d: %w condition key %q", entry.line, errUnknown, entry.key)
		}
	}

	err := parsed.complete(item.line, ops)
	if err != nil {
		return nil, err
	}

	err = parsed.compile()
	if err != nil {
		return nil, err
	}

	return parsed, nil
}

// complete reports a condition at line that names no field, or does not name
// exactly one of the operators ops.
func (t *Term) complete(line int, ops []string) error {
	switch {
	case t.Field == "":
		return fmt.Errorf("line %d: %w", line, errNoField)
	case len(ops) == 0:
		return fmt.Errorf("line %d: %w", line, errNoOperator)
	case len(ops) > 1:
		return fmt.Errorf("line %d: condition has %d %w: %s", line, len(ops), errOperators, strings.Join(ops, ", "))
	}

	return nil
}

// compile validates the Term's value against its operator and compiles the
// pattern the two pattern operators match with.
func (t *Term) compile() error {
	// A pattern that cannot compile would never match, which is the silent
	// failure the format exists to prevent: catch it here, while authoring.
	// The same goes for a path glob or equals value no cleaned path can meet.
	op := strings.TrimPrefix(t.Op, "not_")
	if clean := path.Clean(t.Value); t.Field == fieldPath && (op == opGlob || op == opEquals) && clean != t.Value {
		return fmt.Errorf("line %d: path value %q %w, write %q", t.line, t.Value, errNotClean, clean)
	}

	switch op {
	case opMatches:
		re, err := regexp.Compile(t.Value)
		if err != nil {
			return fmt.Errorf("line %d: invalid regexp: %w", t.line, err)
		}

		t.re = re
	case opGlob:
		re, err := globToRegexp(t.Value)
		if err != nil {
			return fmt.Errorf("line %d: invalid glob: %w", t.line, err)
		}

		t.re = re
	}

	return nil
}

// globToRegexp compiles a glob into an anchored regexp, which is both the
// validation (a pattern that cannot compile would never match) and the matcher.
// The dialect is path.Match plus **, spelled out in docs/spec.md section 2.
func globToRegexp(pattern string) (*regexp.Regexp, error) {
	var out strings.Builder
	out.WriteByte('^')

	for pos := 0; pos < len(pattern); pos++ {
		switch pattern[pos] {
		case '\\':
			if pos == len(pattern)-1 {
				return nil, errTrailingBackslash
			}

			pos++
			out.WriteString(regexp.QuoteMeta(pattern[pos : pos+1]))
		case '*':
			pos = globStar(&out, pattern, pos)
		case '?':
			out.WriteString(`[^/]`)
		case '[':
			end, err := globClass(&out, pattern, pos)
			if err != nil {
				return nil, err
			}

			pos = end
		default:
			out.WriteString(regexp.QuoteMeta(pattern[pos : pos+1]))
		}
	}

	out.WriteByte('$')

	return regexp.Compile(out.String())
}

// globStar writes the translation of the star at pattern[pos], alone or the
// first of a **, and returns the index of the last byte it consumed.
func globStar(out *strings.Builder, pattern string, pos int) int {
	// ** only crosses separators on its own segment, so it needs a
	// boundary on both sides: in foo**/bar and in **.env the stars
	// belong to their neighbour, and treating them as a segment skip
	// would quietly match foobar and nested/x.env.
	atBoundary := pos == 0 || pattern[pos-1] == '/'
	switch {
	case atBoundary && strings.HasPrefix(pattern[pos:], "**/"):
		// Zero directories included, so **/*.env covers a root file.
		out.WriteString(`(?:[^/]*/)*`)

		return pos + len("**/") - 1
	case atBoundary && pattern[pos:] == "**":
		out.WriteString(`.*`)

		return pos + 1
	}

	out.WriteString(`[^/]*`)

	return pos
}

// globClass writes the translation of the character class starting at
// pattern[start] and returns the index of its closing bracket. Negation is ^,
// as in path.Match; ! is an ordinary member.
func globClass(out *strings.Builder, pattern string, start int) (int, error) {
	out.WriteByte('[')

	pos := start + 1
	if pos < len(pattern) && pattern[pos] == '^' {
		out.WriteByte('^')

		pos++
	}

	if pos >= len(pattern) || pattern[pos] == ']' {
		return 0, errEmptyClass
	}

	for ; pos < len(pattern) && pattern[pos] != ']'; pos++ {
		if pattern[pos] != '\\' {
			out.WriteByte(pattern[pos])

			continue
		}

		if pos == len(pattern)-1 {
			return 0, errTrailingBackslash
		}

		pos++
		writeClassEscape(out, pattern[pos])
	}

	if pos >= len(pattern) {
		return 0, errUnterminatedClass
	}

	out.WriteByte(']')

	return pos, nil
}

// writeClassEscape writes the member a backslash escaped inside a class.
func writeClassEscape(out *strings.Builder, member byte) {
	// QuoteMeta leaves - alone, which inside a class turns an escaped
	// member into a range: [a\-z] would silently become [a-z].
	if !isAlphanumeric(member) {
		out.WriteByte('\\')
	}

	out.WriteByte(member)
}

// isAlphanumeric reports whether c is a byte RE2 reads as itself, so escaping
// it would name an escape sequence (\d, \a) rather than the literal character.
func isAlphanumeric(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
