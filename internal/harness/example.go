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
		if r.Selects(examplePayloads(r.Event, e)) != (e.Expect == "match") {
			failed = append(failed, e)
		}
	}
	return failed
}

// examplePayloads is every payload a live call with the Example's fields
// yields.
func examplePayloads(event string, e rule.Example) []rule.Payload {
	p := rule.Payload{Event: event, Kind: e.Kind}
	var tools []string
	var command string
	for _, f := range e.Fields {
		switch {
		case f.Name == "kind":
		case f.Name == "tool":
			for _, a := range adapters {
				tools = append(tools, a.toolNames(f.Values[0])...)
			}
			setTool(&p, tools)
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
	// Codex applies this form itself, and an Example takes its knowledge too.
	if dir, patch, ok := shell.Patch(command); ok && p.Kind == "shell" {
		return append([]rule.Payload{p}, patchPayloads(event, tools, dir, patch)...)
	}
	return []rule.Payload{p}
}
