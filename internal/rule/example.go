package rule

import (
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
		switch {
		case f.key == "domain":
			return e, fmt.Errorf("line %d: domain comes from a written url, so write the url", f.line)
		case f.key == "kind":
			if !IsKind(f.val.scalar) {
				return e, fmt.Errorf("line %d: unknown kind %q", f.line, f.val.scalar)
			}
			e.Kind = f.val.scalar
		case !IsField(f.key):
			return e, fmt.Errorf("line %d: unknown example field %q", f.line, f.key)
		}
		values, err := exampleValues(f)
		if err != nil {
			return e, err
		}
		e.Fields = append(e.Fields, ExampleField{Name: f.key, Values: values})
	}
	if !slices.ContainsFunc(e.Fields, func(f ExampleField) bool { return f.Name != "kind" }) {
		return e, fmt.Errorf("line %d: example has no fields", item.line)
	}
	return e, nil
}

// exampleValues reads one written field as the values a live call could carry
// in it: a list only where a field holds one Candidate per entry, and never a
// value the Adapter would not present.
func exampleValues(f pair) ([]string, error) {
	values := []string{f.val.scalar}
	switch {
	case f.val.seq != nil && slices.Contains([]string{"url", "network_grant", "unreadable", "path"}, f.key):
		values = values[:0]
		for _, v := range f.val.seq {
			if !v.isScalar {
				return nil, fmt.Errorf("line %d: %s entries must be single values", v.line, f.key)
			}
			values = append(values, v.scalar)
		}
		if f.key == "path" && len(values) != 2 {
			return nil, fmt.Errorf("line %d: a path list is a rename: its source and its destination", f.line)
		}
	case !f.val.isScalar:
		return nil, fmt.Errorf("line %d: %s must be a single value", f.line, f.key)
	}
	for _, v := range values {
		switch {
		case v == "" || f.key == "network_grant" && grant(v) == "":
			return nil, fmt.Errorf("line %d: %s needs a value", f.line, f.key)
		case (f.key == "writes_empty" || f.key == "deletes" || f.key == "unsandboxed") && v != "true":
			return nil, fmt.Errorf("line %d: %s can only be true", f.line, f.key)
		case f.key == "unreadable" && !IsField(v) && v != "payload" && v != "rules":
			return nil, fmt.Errorf("line %d: unknown unreadable value %q", f.line, v)
		}
	}
	return values, nil
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
