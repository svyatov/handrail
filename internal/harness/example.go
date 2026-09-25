package harness

import (
	"slices"

	"github.com/svyatov/handrail/internal/rule"
	"github.com/svyatov/handrail/internal/shell"
)

// Failure is one Example a rule's matcher does not meet. From is the rule
// whose file holds the Example: the rule itself, or the rule it replaces.
type Failure struct {
	rule.Example

	From *rule.Rule
}

// FailingExamples evaluates each of the rule's Examples against the rule alone
// and returns the ones whose expectation the matcher does not meet. An Example
// has no harness, so it reads with every Adapter's knowledge.
//
// An enabled shadow is also tested against the match Examples of the rule it
// replaces, so a narrowed copy that has drifted from a changed original fails
// too. A disabled stub is exempt, since switching the rule off is its purpose,
// and so is a call the shadow names in an Example of its own: a no_match one
// declares the exception the copy was made for, and a match one is tested as
// the shadow's own.
func FailingExamples(tested *rule.Rule) []Failure {
	failed := failing(tested, tested, tested.Examples)
	if tested.Enabled && tested.Replaces != nil {
		match := slices.DeleteFunc(slices.Clone(tested.Replaces.Examples), func(e rule.Example) bool {
			return e.Expect != "match" || slices.ContainsFunc(tested.Examples, e.SameCall)
		})
		failed = append(failed, failing(tested, tested.Replaces, match)...)
	}

	return failed
}

// failing returns the examples whose expectation r's matcher does not meet,
// each call made on the event of from, the rule the examples belong to.
func failing(r, from *rule.Rule, examples []rule.Example) []Failure {
	var failed []Failure

	for _, e := range examples {
		call := withFields(rule.Payload{Event: from.Event, Kind: e.Kind, StopHookActive: false}, e.Fields, adapters)
		if r.Selects(call) != (e.Expect == "match") {
			failed = append(failed, Failure{Example: e, From: from})
		}
	}

	return failed
}

// WithFields writes fields onto p as this harness carries them, each replacing
// what p carried under that name, and returns every payload the call then
// yields on this harness: the call test --field writes, over a capture or
// over nothing.
func (a Adapter) WithFields(p rule.Payload, fields []rule.ExampleField) []rule.Payload {
	return withFields(p, fields, []Adapter{a})
}

// withFields is WithFields read with the knowledge of the Adapters from.
func withFields(payload rule.Payload, fields []rule.ExampleField, from []Adapter) []rule.Payload {
	command := setFields(&payload, fields, from)
	// Codex applies this form itself, so only its knowledge reads the patch.
	if slices.ContainsFunc(from, func(a Adapter) bool { return a.patchInShell }) && payload.Kind == kindShell {
		if patch := shell.Patch(command); patch != nil {
			var tools []string
			for _, c := range payload.Fields()["tool"] {
				tools = append(tools, c.Spellings...)
			}

			return append([]rule.Payload{payload}, patchPayloads(payload.Event, tools, patch.Dir, patch.Body)...)
		}
	}

	return []rule.Payload{payload}
}

// setFields writes fields onto payload as the Adapters from carry them, and
// returns the command it wrote.
func setFields(payload *rule.Payload, fields []rule.ExampleField, from []Adapter) string {
	// Every name is cleared before any is written, so a command's derived
	// unreadable merges with a written one whatever their order.
	for _, f := range fields {
		payload.Unset(f.Name)
	}

	var command string

	for _, f := range fields {
		setField(payload, f, from)

		if f.Name == "command" {
			command = f.Values[0]
		}
	}

	return command
}

// setField writes one field onto payload as the Adapters from carry it.
func setField(payload *rule.Payload, field rule.ExampleField, from []Adapter) {
	switch {
	case field.Name == "kind":
	case field.Name == "tool":
		for _, a := range from {
			setTool(payload, a.toolNames(field.Values[0]))
		}
	case field.Name == "path" && len(field.Values) == 2:
		payload.SetRename(field.Values[0], field.Values[1])
	default:
		for _, v := range field.Values {
			payload.SetField(field.Name, v)
		}
	}
}
