package harness

import (
	"slices"

	"github.com/svyatov/handrail/internal/rule"
	"github.com/svyatov/handrail/internal/shell"
)

// FailingExamples evaluates each of the rule's Examples against the rule alone
// and returns the ones whose expectation the matcher does not meet. An Example
// has no harness, so it reads with every Adapter's knowledge.
func FailingExamples(r *rule.Rule) []rule.Example {
	var failed []rule.Example
	for _, e := range r.Examples {
		if r.Selects(ExamplePayloads(r.Event, e)) != (e.Expect == "match") {
			failed = append(failed, e)
		}
	}
	return failed
}

// ExamplePayloads is every payload a live call with the Example's fields
// yields on some harness.
func ExamplePayloads(event string, e rule.Example) []rule.Payload {
	return callPayloads(event, e, adapters)
}

// Payloads is every payload a live call with these fields yields on this
// harness: the call test --field writes.
func (a Adapter) Payloads(event string, e rule.Example) []rule.Payload {
	return callPayloads(event, e, []Adapter{a})
}

// SetFields writes fields onto p as this harness carries them, each replacing
// what p carried under that name.
func (a Adapter) SetFields(p *rule.Payload, fields []rule.ExampleField) {
	setFields(p, fields, []Adapter{a})
}

// callPayloads is every payload a live call with these fields yields, read
// with the knowledge of the Adapters from.
func callPayloads(event string, e rule.Example, from []Adapter) []rule.Payload {
	p := rule.Payload{Event: event, Kind: e.Kind}
	tools, command := setFields(&p, e.Fields, from)
	// Codex applies this form itself, so only its knowledge reads the patch.
	if slices.ContainsFunc(from, func(a Adapter) bool { return a.patchInShell }) && p.Kind == "shell" {
		if dir, patch, ok := shell.Patch(command); ok {
			return append([]rule.Payload{p}, patchPayloads(event, tools, dir, patch)...)
		}
	}
	return []rule.Payload{p}
}

// setFields writes fields onto p as the Adapters from carry them, and returns
// the tool names and the command it wrote.
func setFields(p *rule.Payload, fields []rule.ExampleField, from []Adapter) (tools []string, command string) {
	// Every name is cleared before any is written, so a command's derived
	// unreadable merges with a written one whatever their order.
	for _, f := range fields {
		p.Unset(f.Name)
	}
	for _, f := range fields {
		switch {
		case f.Name == "kind":
		case f.Name == "tool":
			for _, a := range from {
				tools = append(tools, a.toolNames(f.Values[0])...)
			}
			setTool(p, tools)
		case f.Name == "path" && len(f.Values) == 2:
			p.SetRename(f.Values[0], f.Values[1])
		default:
			for _, v := range f.Values {
				p.SetField(f.Name, v)
			}
			if f.Name == "command" {
				command = f.Values[0]
			}
		}
	}
	return tools, command
}
