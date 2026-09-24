package rule

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Example is one call a rule carries as its own proof, and whether the rule's
// matcher selects it: Expect is "match" or "no_match". Fields are the fields
// as written, kind included; Kind is the kind the call takes.
type Example struct {
	Expect string
	Kind   string
	Fields []ExampleField
	Line   int
}

// ExampleField is one field an Example writes, with every Candidate it lists.
type ExampleField struct {
	Name   string
	Values []string
}

// Selects reports whether the rule's matcher selects any payload of one call,
// whatever the rule's state: an Example's question, which asks nothing about
// whether the rule can fire.
func (r *Rule) Selects(payloads []Payload) bool {
	return slices.ContainsFunc(withFiles(payloads), r.matches)
}

// parseExamples reads the examples: mapping, a match: and a no_match: list of
// calls. An Example no live call could be is an error, because it would prove
// the rule against a call that never arrives.
func parseExamples(kv pair) ([]Example, error) {
	if kv.val.isScalar || kv.val.seq != nil {
		return nil, fmt.Errorf("line %d: examples must be a mapping", kv.line)
	}
	var out []Example
	for _, list := range kv.val.mapping {
		if list.key != "match" && list.key != "no_match" {
			return nil, fmt.Errorf("line %d: unknown examples key %q", list.line, list.key)
		}
		if list.val.seq == nil {
			return nil, fmt.Errorf("line %d: %s must be a list", list.line, list.key)
		}
		for _, item := range list.val.seq {
			if item.isScalar || item.seq != nil {
				return nil, fmt.Errorf("line %d: example must be a mapping", item.line)
			}
			e, err := parseExample(list.key, item)
			if err != nil {
				return nil, err
			}
			out = append(out, e)
		}
	}
	return out, nil
}

func parseExample(expect string, item *node) (Example, error) {
	e := Example{Expect: expect, Line: item.line}
	seen := make(map[string]bool, len(item.mapping))
	for _, f := range item.mapping {
		if seen[f.key] {
			return e, fmt.Errorf("line %d: duplicate field %q", f.line, f.key)
		}
		seen[f.key] = true
		values, err := exampleValues(f)
		if err != nil {
			return e, err
		}
		if err := checkField(f.key, values); err != nil {
			return e, fmt.Errorf("line %d: %w", f.line, err)
		}
		if f.key == "kind" {
			e.Kind = values[0]
		}
		e.Fields = append(e.Fields, ExampleField{Name: f.key, Values: values})
	}
	if !slices.ContainsFunc(e.Fields, func(f ExampleField) bool { return f.Name != "kind" }) {
		return e, fmt.Errorf("line %d: example has no fields", item.line)
	}
	return e, nil
}

// exampleValues reads one written field's YAML as its values: a scalar, or a
// list of scalars.
func exampleValues(f pair) ([]string, error) {
	switch {
	case f.val.isScalar:
		return []string{f.val.scalar}, nil
	case f.val.seq == nil:
		return nil, fmt.Errorf("line %d: %s must be a single value", f.line, f.key)
	}
	var values []string
	for _, v := range f.val.seq {
		if !v.isScalar {
			return nil, fmt.Errorf("line %d: %s entries must be single values", v.line, f.key)
		}
		values = append(values, v.scalar)
	}
	return values, nil
}

// CheckCall holds fields written for one call on event, as an Example or
// test --field writes them, to what a live call could carry.
func CheckCall(event string, fields []ExampleField) error {
	for _, f := range fields {
		if err := checkField(f.Name, f.Values); err != nil {
			return err
		}
		switch {
		case f.Name == "kind" && !ToolEvent(event):
			return errors.New("kind applies only to PreToolUse and PostToolUse")
		case f.Name != "kind" && !carries(event, f.Name):
			return fmt.Errorf("%s never carries %s", event, f.Name)
		}
	}
	return nil
}

// checkField reports why a live call could not carry the values written for
// one field: a list only where a field holds one Candidate per entry, and
// never a value the Adapter would not present.
func checkField(name string, values []string) error {
	switch {
	case name == "domain":
		return errors.New("domain comes from a written url, so write the url")
	case name == "kind":
	case !IsField(name):
		return fmt.Errorf("unknown field %q", name)
	}
	switch {
	case len(values) > 1 && !slices.Contains([]string{"url", "network_grant", "unreadable", "path"}, name):
		return fmt.Errorf("%s must be a single value", name)
	case name == "path" && len(values) > 2:
		return errors.New("a path list is a rename: its source and its destination")
	}
	for _, v := range values {
		switch {
		case v == "" || name == "network_grant" && grant(v) == "":
			return fmt.Errorf("%s needs a value", name)
		case name == "kind" && !IsKind(v):
			return fmt.Errorf("unknown kind %q", v)
		case (name == "writes_empty" || name == "deletes" || name == "unsandboxed") && v != "true":
			return fmt.Errorf("%s can only be true", name)
		case name == "unreadable" && !IsField(v) && v != "payload" && v != "rules":
			return fmt.Errorf("unknown unreadable value %q", v)
		}
	}
	return nil
}

// String is the Example's fields as a report names them, each quoted, a list
// as a list.
func (e Example) String() string {
	parts := make([]string, 0, len(e.Fields))
	for _, f := range e.Fields {
		parts = append(parts, fmt.Sprintf("%s=%q", f.Name, f.Value()))
	}
	return strings.Join(parts, ", ")
}

// Value is the field as written: one string, or the list it wrote.
func (f ExampleField) Value() any {
	if len(f.Values) == 1 {
		return f.Values[0]
	}
	return f.Values
}
