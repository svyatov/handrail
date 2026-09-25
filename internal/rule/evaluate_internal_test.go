// White box, because the gap this file guards is between two unexported sites
// that no rule file can put in disagreement: everything a rule file can express
// is tested through the compiled binary instead (ADR 0009).

package rule

import (
	"strings"
	"testing"
)

// An operator lives at three sites: the operators list the parser accepts from,
// the compile switch that turns a pattern into a regexp, and Term.matches,
// which applies it. A testscript case can only exercise operators that already
// exist at all three, so the operator added to the list and forgotten in
// matches is the one nothing catches: it parses, it passes check, and then it
// silently never fires, or, negated, always does. Only a walk of the list sees
// that.
//
// Each value below matches the same command, so the operator is the only thing
// under test. An operator added to the list without a value here fails on the
// missing entry, which is the reminder to think about what matching means for
// it.
func TestEveryOperatorTheParserAcceptsAlsoMatches(t *testing.T) {
	t.Parallel()

	const command = "deploy prod now"

	matching := map[string]string{
		"matches":     `^deploy\s`,
		"contains":    "prod",
		"equals":      command,
		"starts_with": "deploy",
		"ends_with":   "now",
		"glob":        "deploy*",
	}
	payload := Payload{Event: "PreToolUse", Kind: "shell", fields: nil, files: nil, StopHookActive: false}
	payload.SetField("command", command)

	for _, operator := range operators() {
		t.Run(operator, func(t *testing.T) {
			t.Parallel()

			value, ok := matching[operator]
			if !ok {
				t.Fatalf("no value that matches %q, so this operator is untested", operator)
			}
			// The negated spelling shares the switch and inverts the answer, so
			// an operator missing from it reads as "always matches" there.
			for _, c := range []struct {
				key  string
				want bool
			}{{operator, true}, {"not_" + operator, false}} {
				if got := parseOne(t, c.key, value).matches(payload); got != c.want {
					t.Errorf("%s: %s matches(%q) = %v, want %v", c.key, value, command, got, c.want)
				}
			}
		})
	}
}

// parseOne builds a one-Term rule the way a rule file does, so the compile
// switch is on the path too: an operator needing a regexp and not getting one
// panics in matches rather than quietly missing.
func parseOne(t *testing.T, operator, value string) *Rule {
	t.Helper()

	doc := strings.Join([]string{
		"---",
		"event: PreToolUse",
		"kind: shell",
		"conditions:",
		"  - field: command",
		"    " + operator + ": '" + value + "'",
		"---",
		"Say something.",
	}, "\n")

	parsed, err := Parse("op-under-test", []byte(doc))
	if err != nil {
		t.Fatalf("Parse(%s: %s) = %v", operator, value, err)
	}

	if len(parsed.Conditions) != 1 || len(parsed.Conditions[0].Terms) != 1 {
		t.Fatalf("Parse(%s) produced %d conditions, want one term", operator, len(parsed.Conditions))
	}

	return parsed
}
