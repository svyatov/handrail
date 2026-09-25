package rule

import (
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Why a written call is not one a live call could be.
var (
	errExampleEmpty     = errors.New("example has no fields")
	errEntriesNotSingle = errors.New("entries must be single values")
	errDomainWritten    = errors.New("domain comes from a written url, so write the url")
	errPathList         = errors.New("a path list is a rename: its source and its destination")
	errNeedsValue       = errors.New("needs a value")
	errOnlyTrue         = errors.New("can only be true")
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
func parseExamples(entry pair) ([]Example, error) {
	if !entry.val.isMapping() {
		return nil, fmt.Errorf("line %d: examples %w", entry.line, errNotMapping)
	}

	var out []Example

	for _, list := range entry.val.mapping {
		if list.key != "match" && list.key != "no_match" {
			return nil, fmt.Errorf("line %d: %w examples key %q", list.line, errUnknown, list.key)
		}

		if list.val.seq == nil {
			return nil, fmt.Errorf("line %d: %s %w", list.line, list.key, errNotList)
		}

		for _, item := range list.val.seq {
			if !item.isMapping() {
				return nil, fmt.Errorf("line %d: example %w", item.line, errNotMapping)
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
	example := Example{Expect: expect, Kind: "", Fields: nil, Line: item.line}

	seen := make(map[string]bool, len(item.mapping))
	for _, field := range item.mapping {
		if seen[field.key] {
			return example, fmt.Errorf("line %d: %w %q", field.line, errDuplicateField, field.key)
		}

		seen[field.key] = true

		values, err := exampleValues(field)
		if err != nil {
			return example, err
		}

		err = checkField(field.key, values, field.val.seq != nil)
		if err != nil {
			return example, fmt.Errorf("line %d: %w", field.line, err)
		}

		if field.key == keyKind {
			example.Kind = values[0]
		}

		example.Fields = append(example.Fields, ExampleField{Name: field.key, Values: values})
	}

	if !slices.ContainsFunc(example.Fields, func(f ExampleField) bool { return f.Name != keyKind }) {
		return example, fmt.Errorf("line %d: %w", item.line, errExampleEmpty)
	}

	return example, nil
}

// exampleValues reads one written field's YAML as its values: a scalar, or a
// list of scalars.
func exampleValues(field pair) ([]string, error) {
	switch {
	case field.val.isScalar:
		return []string{field.val.scalar}, nil
	case field.val.seq == nil:
		return nil, fmt.Errorf("line %d: %s %w", field.line, field.key, errNotSingle)
	}

	var values []string

	for _, v := range field.val.seq {
		if !v.isScalar {
			return nil, fmt.Errorf("line %d: %s %w", v.line, field.key, errEntriesNotSingle)
		}

		values = append(values, v.scalar)
	}

	return values, nil
}

// CheckCall holds fields written for one call on event, as an Example or
// test --field writes them, to what a live call could carry. A field written
// more than once is a list.
func CheckCall(event string, fields []ExampleField) error {
	for _, field := range fields {
		err := checkField(field.Name, field.Values, len(field.Values) > 1)
		if err != nil {
			return err
		}

		switch {
		case field.Name == keyKind && !ToolEvent(event):
			return errKindNotTool
		case field.Name != keyKind && !carries(event, field.Name):
			return fmt.Errorf("%s %w %s", event, errNeverCarries, field.Name)
		}
	}

	return nil
}

// checkField reports why a live call could not carry the values written for
// one field: a list only where a field holds one Candidate per entry, a path
// list only as a rename's two paths, and never a value the Adapter would not
// present. list says the values were written as a list, which one entry can
// be.
func checkField(name string, values []string, list bool) error {
	switch {
	case name == "domain":
		return errDomainWritten
	case name == keyKind:
	case !IsField(name):
		return fmt.Errorf("%w field %q", errUnknown, name)
	}

	if list {
		err := checkList(name, values)
		if err != nil {
			return err
		}
	}

	for _, value := range values {
		err := checkValue(name, value)
		if err != nil {
			return err
		}
	}

	return nil
}

// checkList reports why a field cannot be written as a list.
func checkList(name string, values []string) error {
	switch {
	case !slices.Contains([]string{fieldURL, fieldNetworkGrant, fieldUnreadable, fieldPath}, name):
		return fmt.Errorf("%s %w", name, errNotSingle)
	case name == fieldPath && len(values) != 2:
		return errPathList
	}

	return nil
}

// checkValue reports why a field could not carry one value.
func checkValue(name, value string) error {
	if blank(name, value) {
		return fmt.Errorf("%s %w", name, errNeedsValue)
	}

	switch name {
	case keyKind:
		if !IsKind(value) {
			return fmt.Errorf("%w kind %q", errUnknown, value)
		}
	case "writes_empty", "deletes", "unsandboxed":
		if value != "true" {
			return fmt.Errorf("%s %w", name, errOnlyTrue)
		}
	case fieldUnreadable:
		if !IsField(value) && value != "payload" && value != "rules" {
			return fmt.Errorf("%w unreadable value %q", errUnknown, value)
		}
	}

	return nil
}

// blank reports whether value is no value for the field, as written or once the
// Adapter has normalized it.
func blank(name, value string) bool {
	switch name {
	case fieldNetworkGrant:
		return grant(value) == ""
	case fieldResponse:
		return strings.TrimSpace(value) == ""
	}

	return value == ""
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

// SameCall reports whether two Examples write the same call: the same kind and
// the same fields, written in the same order.
func (e Example) SameCall(o Example) bool {
	return e.Kind == o.Kind && e.String() == o.String()
}

// Value is the field as written: one string, or the list it wrote.
func (f ExampleField) Value() any {
	if len(f.Values) == 1 {
		return f.Values[0]
	}

	return f.Values
}
