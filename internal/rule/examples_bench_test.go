package rule

import (
	"fmt"
	"testing"
)

// BenchmarkExamples is T36's SessionStart case against today's engine: parse
// 50 rules, then evaluate each rule's two Examples against that rule alone.
// The shell parse T2 and T31 add is measured apart, in the bench module.
func BenchmarkExamples(b *testing.B) {
	files := make([][]byte, 50)
	for i := range files {
		files[i] = fmt.Appendf(nil, "---\nevent: PreToolUse\nkind: shell\nconditions:\n"+
			"  - field: command\n    matches: ^rm -rf /srv/app-%d\\b\n"+
			"  - field: command\n    not_contains: --dry-run\n---\nDo not remove app %d.\n", i, i)
	}
	for b.Loop() {
		for i, data := range files {
			r, err := Parse(fmt.Sprintf("rule-%d.md", i), data)
			if err != nil {
				b.Fatal(err)
			}
			rs := &Ruleset{Rules: []*Rule{r}}
			for _, cmd := range []string{fmt.Sprintf("rm -rf /srv/app-%d", i), "rm -rf /srv/app-x --dry-run"} {
				p := Payload{Event: "PreToolUse", Kind: "shell"}
				p.SetField("command", cmd)
				rs.Evaluate(p)
			}
		}
	}
}
