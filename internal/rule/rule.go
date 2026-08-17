// Package rule parses handrail rule files and discovers the tiers they live in.
package rule

import (
	"fmt"
	"regexp"
	"strings"
)

// Rule is one guardrail: a matcher (event, kind, conditions) and the message
// delivered when it matches. Identity is the file's basename.
type Rule struct {
	Name       string
	Path       string
	Event      string
	Kind       string
	Action     string
	Enabled    bool
	Conditions []Condition
	Message    string
}

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

// These four sets are switches rather than package-level maps so that nothing
// runs before main: the hook path pays for every byte of startup work.

// isEvent reports whether name is one of the six core events.
func isEvent(name string) bool {
	switch name {
	case "PreToolUse", "PostToolUse", "UserPromptSubmit", "SessionStart", "SessionEnd", "Stop":
		return true
	}
	return false
}

// isKind reports whether name is a canonical tool kind.
func isKind(name string) bool {
	switch name {
	case "shell", "file_edit", "file_read", "mcp", "other":
		return true
	}
	return false
}

// isField reports whether name is a canonical payload field a condition may
// address. raw.* is a v1 non-goal, so this set is closed.
func isField(name string) bool {
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
	fm, body, err := splitFrontmatter(strings.ReplaceAll(string(data), "\r\n", "\n"))
	if err != nil {
		return nil, err
	}
	doc, err := parseYAML(strings.Split(fm, "\n"))
	if err != nil {
		return nil, err
	}
	if doc.isScalar || doc.seq != nil {
		return nil, fmt.Errorf("line 2: frontmatter must be a mapping")
	}

	r := &Rule{Name: name, Action: "warn", Enabled: true, Message: strings.TrimSpace(body)}
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
			if !isEvent(r.Event) {
				return nil, fmt.Errorf("line %d: unknown event %q", kv.line, r.Event)
			}
		case "kind":
			if err := scalarInto(kv, &r.Kind); err != nil {
				return nil, err
			}
			if !isKind(r.Kind) {
				return nil, fmt.Errorf("line %d: unknown kind %q", kv.line, r.Kind)
			}
		case "action":
			if err := scalarInto(kv, &r.Action); err != nil {
				return nil, err
			}
			if r.Action != "warn" && r.Action != "block" {
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
			return nil, fmt.Errorf(`line 1: missing required field "event"`)
		}
		// The message is the product: a rule that matches and says nothing
		// blocks or warns with an empty reason.
		if r.Message == "" {
			return nil, fmt.Errorf("line 1: rule has no message")
		}
	}
	return r, nil
}

func splitFrontmatter(s string) (frontmatter, body string, err error) {
	lines := strings.Split(strings.TrimPrefix(s, "\ufeff"), "\n")
	if strings.TrimRight(lines[0], " ") != "---" {
		return "", "", fmt.Errorf("line 1: missing YAML frontmatter")
	}
	for i := 1; i < len(lines); i++ {
		if strings.TrimRight(lines[i], " ") == "---" {
			return strings.Join(lines[1:i], "\n"), strings.Join(lines[i+1:], "\n"), nil
		}
	}
	return "", "", fmt.Errorf("line 1: unterminated YAML frontmatter")
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
			if !isField(kv.val.scalar) {
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
			return nil, fmt.Errorf("line %d: invalid regexp: %v", t.line, err)
		}
		t.Re = re
	case "glob":
		if err := validateGlob(t.Value); err != nil {
			return nil, fmt.Errorf("line %d: invalid glob: %v", t.line, err)
		}
	}
	return t, nil
}

// validateGlob rejects the two ways a path.Match pattern can be malformed: a
// character class that never closes and a trailing escape. ** is deliberately
// nothing special here; matching gives it meaning.
func validateGlob(pattern string) error {
	for i := 0; i < len(pattern); i++ {
		switch pattern[i] {
		case '\\':
			if i == len(pattern)-1 {
				return fmt.Errorf("trailing backslash")
			}
			i++
		case '[':
			i++
			if i < len(pattern) && pattern[i] == '^' {
				i++
			}
			if i >= len(pattern) || pattern[i] == ']' {
				return fmt.Errorf("empty character class")
			}
			for ; i < len(pattern) && pattern[i] != ']'; i++ {
				if pattern[i] == '\\' {
					i++
				}
			}
			if i >= len(pattern) {
				return fmt.Errorf("unterminated character class")
			}
		}
	}
	return nil
}
