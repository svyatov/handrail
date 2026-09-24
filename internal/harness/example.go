package harness

import (
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
// yields.
func ExamplePayloads(event string, e rule.Example) []rule.Payload {
	p := rule.Payload{Event: event, Kind: e.Kind}
	tools, command := SetFields(&p, e.Fields)
	// Codex applies this form itself, and an Example takes its knowledge too.
	if dir, patch, ok := shell.Patch(command); ok && p.Kind == "shell" {
		return append([]rule.Payload{p}, patchPayloads(event, tools, dir, patch)...)
	}
	return []rule.Payload{p}
}

// SetFields writes fields onto p the way an Example writes them, each replacing
// what p carried under that name, and returns the tool names and the command
// it wrote.
func SetFields(p *rule.Payload, fields []rule.ExampleField) (tools []string, command string) {
	// Every name is cleared before any is written, so a command's derived
	// unreadable merges with a written one whatever their order.
	for _, f := range fields {
		p.Unset(f.Name)
	}
	for _, f := range fields {
		switch {
		case f.Name == "kind":
		case f.Name == "tool":
			for _, a := range adapters {
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
