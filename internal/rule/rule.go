// Package rule parses handrail rule files and discovers the tiers they live in.
package rule

import (
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// The Action and Outcome vocabulary, one set for both: an Outcome takes the
// strongest Action among the matched rules, so warn and block are the same two
// values under either name. Allow is exported although no Rule.Action may hold
// it, because it belongs to the vocabulary rather than to one field.
const (
	Allow = "allow"
	Warn  = "warn"
	Block = "block"
)

// Rule is one guardrail: a matcher (event, kind, conditions) and the message
// delivered when it matches. Identity is the file's basename.
type Rule struct {
	Name       string
	Path       string
	Tier       string
	ShadowedBy *Rule // the higher-tier rule replacing this one, if any
	Event      string
	Kind       string
	Action     string
	Enabled    bool
	Conditions []Condition
	Message    string
}

// Live reports whether this rule can fire: enabled, and not shadowed by a
// higher tier. A rule that is loaded is not thereby a rule that enforces
// anything, and every caller asking which is which asks here. check reads the
// two fields directly, because it reports the distinction rather than acts on
// it.
func (r *Rule) Live() bool { return r.Enabled && r.ShadowedBy == nil }

// Condition is one entry of a rule's implicit-AND condition list: either a
// single Term, or a one-level any group that ORs its Terms.
type Condition struct {
	Term *Term
	Any  []Term
}

// Term is one operator applied to one canonical payload field.
type Term struct {
	Field string
	Op    string
	Value string
	Re    *regexp.Regexp // compiled, for matches and not_matches
	line  int
}

// events holds the six core events, in the order sync writes hook entries for
// them. An array of constants is static data, so nothing runs before main to
// build it: the hook path pays for every byte of startup work.
var events = [...]string{"PreToolUse", "PostToolUse", "UserPromptSubmit", "SessionStart", "SessionEnd", "Stop"}

// Events lists the six core events. Only sync needs the list, and it gets a
// copy so no caller can reorder the array IsEvent reads.
func Events() []string { return slices.Clone(events[:]) }

// IsEvent reports whether name is one of the six core events.
func IsEvent(name string) bool { return slices.Contains(events[:], name) }

// The three sets below are switches rather than package-level maps for the same
// reason: nothing may run before main.

// IsKind reports whether name is a canonical tool kind.
func IsKind(name string) bool {
	switch name {
	case "shell", "file_edit", "file_read", "mcp", "other":
		return true
	}
	return false
}

// IsField reports whether name is a canonical payload field a condition may
// address. raw.* is a v1 non-goal, so this set is closed.
func IsField(name string) bool {
	switch name {
	case "command", "path", "content", "server", "tool", "prompt":
		return true
	}
	return false
}

// isOperator reports whether key is a condition operator, negated or not.
func isOperator(key string) bool {
	switch strings.TrimPrefix(key, "not_") {
	case "matches", "contains", "equals", "starts_with", "ends_with", "glob":
		return true
	}
	return false
}

// Parse reads one rule file. name is the rule's identity, the file's basename
// without its extension.
func Parse(name string, data []byte) (*Rule, error) {
	doc, body, err := parseFrontmatter(data)
	if err != nil {
		return nil, err
	}

	r := &Rule{Name: name, Action: Warn, Enabled: true, Message: strings.TrimSpace(body)}
	seen := make(map[string]bool, len(doc.mapping))
	for _, kv := range doc.mapping {
		if seen[kv.key] {
			return nil, fmt.Errorf("line %d: duplicate field %q", kv.line, kv.key)
		}
		seen[kv.key] = true

		switch kv.key {
		case "event":
			if err := scalarInto(kv, &r.Event); err != nil {
				return nil, err
			}
			if !IsEvent(r.Event) {
				return nil, fmt.Errorf("line %d: unknown event %q", kv.line, r.Event)
			}
		case "kind":
			if err := scalarInto(kv, &r.Kind); err != nil {
				return nil, err
			}
			if !IsKind(r.Kind) {
				return nil, fmt.Errorf("line %d: unknown kind %q", kv.line, r.Kind)
			}
		case "action":
			if err := scalarInto(kv, &r.Action); err != nil {
				return nil, err
			}
			if r.Action != Warn && r.Action != Block {
				return nil, fmt.Errorf("line %d: unknown action %q", kv.line, r.Action)
			}
		case "enabled":
			var v string
			if err := scalarInto(kv, &v); err != nil {
				return nil, err
			}
			if v != "true" && v != "false" {
				return nil, fmt.Errorf("line %d: enabled must be true or false", kv.line)
			}
			r.Enabled = v == "true"
		case "conditions":
			if kv.val.seq == nil {
				return nil, fmt.Errorf("line %d: conditions must be a list", kv.line)
			}
			if r.Conditions, err = parseConditions(kv.val); err != nil {
				return nil, err
			}
		default:
			return nil, fmt.Errorf("line %d: unknown frontmatter field %q", kv.line, kv.key)
		}
	}

	// A disabled rule is exempt from matcher validation; whatever fields it
	// does carry have already been validated above.
	if r.Enabled {
		if r.Event == "" {
			return nil, errors.New(`line 1: missing required field "event"`)
		}
		// The message is the product: a rule that matches and says nothing
		// blocks or warns with an empty reason.
		if r.Message == "" {
			return nil, errors.New("line 1: rule has no message")
		}
	}
	return r, nil
}

// parseFrontmatter reads a markdown file's YAML frontmatter as a mapping, and
// returns it with the body below. The Importer reads upstream files with it too:
// their frontmatter is the same block-style subset.
func parseFrontmatter(data []byte) (doc *node, body string, err error) {
	fm, body, err := splitFrontmatter(strings.ReplaceAll(string(data), "\r\n", "\n"))
	if err != nil {
		return nil, "", err
	}
	doc, err = parseYAML(strings.Split(fm, "\n"))
	if err != nil {
		return nil, "", err
	}
	if doc.isScalar || doc.seq != nil {
		return nil, "", errors.New("line 2: frontmatter must be a mapping")
	}
	return doc, body, nil
}

func splitFrontmatter(s string) (frontmatter, body string, err error) {
	lines := strings.Split(strings.TrimPrefix(s, "\ufeff"), "\n")
	if strings.TrimRight(lines[0], " ") != "---" {
		return "", "", errors.New("line 1: missing YAML frontmatter")
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " ") == "---" {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n"), nil
		}
	}
	return "", "", errors.New("line 1: unterminated YAML frontmatter")
}

func scalarInto(kv pair, dst *string) error {
	if !kv.val.isScalar {
		return fmt.Errorf("line %d: %s must be a single value", kv.line, kv.key)
	}
	*dst = kv.val.scalar
	return nil
}

func parseConditions(list *node) ([]Condition, error) {
	out := make([]Condition, 0, len(list.seq))
	for _, item := range list.seq {
		if item.isScalar || item.seq != nil {
			return nil, fmt.Errorf("line %d: condition must be a mapping", item.line)
		}
		if !item.has("any") {
			t, err := parseTerm(item)
			if err != nil {
				return nil, err
			}
			out = append(out, Condition{Term: t})
			continue
		}
		if len(item.mapping) != 1 {
			return nil, fmt.Errorf("line %d: any must be a condition's only key", item.line)
		}
		group := item.mapping[0]
		if group.val.seq == nil {
			return nil, fmt.Errorf("line %d: any must be a list", group.line)
		}
		terms := make([]Term, 0, len(group.val.seq))
		for _, sub := range group.val.seq {
			if sub.isScalar || sub.seq != nil {
				return nil, fmt.Errorf("line %d: condition must be a mapping", sub.line)
			}
			if sub.has("any") {
				return nil, fmt.Errorf("line %d: any groups cannot nest", sub.line)
			}
			t, err := parseTerm(sub)
			if err != nil {
				return nil, err
			}
			terms = append(terms, *t)
		}
		out = append(out, Condition{Any: terms})
	}
	return out, nil
}

func parseTerm(n *node) (*Term, error) {
	t := &Term{}
	var ops []string
	seen := make(map[string]bool, len(n.mapping))
	for _, kv := range n.mapping {
		if seen[kv.key] {
			return nil, fmt.Errorf("line %d: duplicate field %q", kv.line, kv.key)
		}
		seen[kv.key] = true
		if !kv.val.isScalar {
			return nil, fmt.Errorf("line %d: %s must be a single value", kv.line, kv.key)
		}
		switch {
		case kv.key == "field":
			if !IsField(kv.val.scalar) {
				return nil, fmt.Errorf("line %d: unknown condition field %q", kv.line, kv.val.scalar)
			}
			t.Field = kv.val.scalar
		case isOperator(kv.key):
			ops = append(ops, kv.key)
			t.Op, t.Value, t.line = kv.key, kv.val.scalar, kv.line
		default:
			return nil, fmt.Errorf("line %d: unknown condition key %q", kv.line, kv.key)
		}
	}
	if t.Field == "" {
		return nil, fmt.Errorf("line %d: condition has no field", n.line)
	}
	switch len(ops) {
	case 1:
	case 0:
		return nil, fmt.Errorf("line %d: condition has no operator", n.line)
	default:
		return nil, fmt.Errorf("line %d: condition has %d operators: %s", n.line, len(ops), strings.Join(ops, ", "))
	}
	// A pattern that cannot compile would never match, which is the silent
	// failure the format exists to prevent: catch it here, while authoring.
	switch strings.TrimPrefix(t.Op, "not_") {
	case "matches":
		re, err := regexp.Compile(t.Value)
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid regexp: %w", t.line, err)
		}
		t.Re = re
	case "glob":
		re, err := globToRegexp(t.Value)
		if err != nil {
			return nil, fmt.Errorf("line %d: invalid glob: %w", t.line, err)
		}
		t.Re = re
	}
	return t, nil
}

// globToRegexp compiles a glob into an anchored regexp, which is both the
// validation (a pattern that cannot compile would never match) and the matcher.
// The dialect is path.Match plus **, spelled out in docs/spec.md section 2.
func globToRegexp(pattern string) (*regexp.Regexp, error) {
	var b strings.Builder
	b.WriteByte('^')
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			if i == len(pattern)-1 {
				return nil, errors.New("trailing backslash")
			}
			i++
			b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
		case '*':
			// ** only crosses separators on its own segment, so it needs a
			// boundary on both sides: in foo**/bar and in **.env the stars
			// belong to their neighbour, and treating them as a segment skip
			// would quietly match foobar and nested/x.env.
			atBoundary := i == 0 || pattern[i-1] == '/'
			switch {
			case atBoundary && i+2 < len(pattern) && pattern[i+1] == '*' && pattern[i+2] == '/':
				// Zero directories included, so **/*.env covers a root file.
				i += 2
				b.WriteString(`(?:[^/]*/)*`)
			case atBoundary && i+2 == len(pattern) && pattern[i+1] == '*':
				i++
				b.WriteString(`.*`)
			default:
				b.WriteString(`[^/]*`)
			}
		case '?':
			b.WriteString(`[^/]`)
		case '[':
			class, end, err := globClass(pattern, i)
			if err != nil {
				return nil, err
			}
			b.WriteString(class)
			i = end
		default:
			b.WriteString(regexp.QuoteMeta(pattern[i : i+1]))
		}
	}
	b.WriteByte('$')
	return regexp.Compile(b.String())
}

// globClass translates the character class starting at pattern[start] and
// returns it with the index of its closing bracket. Negation is ^, as in
// path.Match; ! is an ordinary member.
func globClass(pattern string, start int) (class string, end int, err error) {
	var b strings.Builder
	b.WriteByte('[')
	i := start + 1
	if i < len(pattern) && pattern[i] == '^' {
		b.WriteByte('^')
		i++
	}
	if i >= len(pattern) || pattern[i] == ']' {
		return "", 0, errors.New("empty character class")
	}
	for ; i < len(pattern) && pattern[i] != ']'; i++ {
		if pattern[i] != '\\' {
			b.WriteByte(pattern[i])
			continue
		}
		if i == len(pattern)-1 {
			return "", 0, errors.New("trailing backslash")
		}
		i++
		// QuoteMeta leaves - alone, which inside a class turns an escaped
		// member into a range: [a\-z] would silently become [a-z].
		if c := pattern[i]; isAlphanumeric(c) {
			b.WriteByte(c)
		} else {
			b.WriteByte('\\')
			b.WriteByte(c)
		}
	}
	if i >= len(pattern) {
		return "", 0, errors.New("unterminated character class")
	}
	b.WriteByte(']')
	return b.String(), i, nil
}

// isAlphanumeric reports whether c is a byte RE2 reads as itself, so escaping
// it would name an escape sequence (\d, \a) rather than the literal character.
func isAlphanumeric(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z'
}
