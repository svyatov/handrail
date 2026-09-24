package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/svyatov/handrail/internal/harness"
	"github.com/svyatov/handrail/internal/rule"
)

const testUsage = `Usage: handrail test <event> [--kind kind] [--field key=value]... [--stdin] [--harness name] [--json]

With --stdin, reads a harness payload as that harness sends it, normalized the
way the hook path normalizes it. Flags override what the payload carries.
`

// fieldSet collects repeated --field key=value flags into the synthetic
// payload's canonical fields.
type fieldSet map[string]string

func (f fieldSet) String() string { return "" }

func (f fieldSet) Set(s string) error {
	k, v, ok := strings.Cut(s, "=")
	if !ok {
		return fmt.Errorf("expected key=value, got %q", s)
	}
	if !rule.IsField(k) {
		return fmt.Errorf("unknown canonical field %q", k)
	}
	// The Adapter never presents a field it carries empty, so accepting one here
	// would let test build a payload hook cannot, and answer for a case it would
	// get wrong: an empty value tests as present, an absent field never matches
	// in either polarity. Dropping it silently would leave that belief in place.
	if v == "" {
		return fmt.Errorf("field %q needs a value; a field carried empty is absent, so omit it", k)
	}
	f[k] = v
	return nil
}

type testMatch struct {
	Rule    string `json:"rule"`
	Tier    string `json:"tier"`
	Action  string `json:"action"`
	Message string `json:"message"`
}

type testOutput struct {
	Outcome string      `json:"outcome"`
	Matched []testMatch `json:"matched"`
}

func cmdTest(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kind := fs.String("kind", "", "tool kind of the synthetic payload")
	fields := fieldSet{}
	fs.Var(fields, "field", "canonical payload field as key=value, repeatable")
	fromStdin := fs.Bool("stdin", false, "read a harness payload JSON from stdin")
	only := fs.String("harness", "claude", "read the payload as this harness sends it")
	asJSON := fs.Bool("json", false, "print the result as JSON")

	// The event is positional and leads, so pull it before flag parsing: the
	// stdlib flag package stops at the first non-flag argument.
	var event string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		event, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "handrail test: unexpected argument %q\n", fs.Arg(0))
		return 1
	}
	if event == "" {
		fmt.Fprintf(stderr, "handrail test: missing event\n\n%s", testUsage)
		return 1
	}
	if !rule.IsEvent(event) {
		fmt.Fprintf(stderr, "handrail test: unknown event %q\n", event)
		return 1
	}
	if *kind != "" && !rule.IsKind(*kind) {
		fmt.Fprintf(stderr, "handrail test: unknown kind %q\n", *kind)
		return 1
	}
	a, known := harness.Lookup(*only)
	if !known {
		fmt.Fprintf(stderr, "handrail test: unknown harness %q; known: %s\n",
			*only, strings.Join(harness.Names(), ", "))
		return 1
	}

	payload := rule.Payload{Event: event}
	if *fromStdin {
		data, err := io.ReadAll(stdin)
		if err != nil {
			fmt.Fprintf(stderr, "handrail test: reading payload: %v\n", err)
			return 1
		}
		// The Adapter's own normalization, so a capture read here reads exactly
		// as it will on the hook path: the kind comes from the tool name, and
		// the fields come out of the tool input. The cwd it reports is dropped,
		// because test answers for the ruleset in the working directory rather
		// than the one the capture was taken under.
		payload, _, err = a.Normalize(event, data)
		if err != nil {
			fmt.Fprintf(stderr, "handrail test: reading payload: %v\n", err)
			return 1
		}
	}
	// Flags win over the payload, so one field can be varied against a capture.
	// --field already refuses an empty value, so nothing here is dropped.
	for k, v := range fields {
		payload.SetField(k, v)
	}
	if *kind != "" {
		payload.Kind = *kind
	}

	// test is an authoring-time surface, so it is strict like check: the loud
	// fail-open belongs to the event-time hook path, not here.
	rs, code := loadValidRules(stderr)
	if code != 0 {
		return code
	}

	// The same call the hook path makes, so what test reports is what hook does.
	matched, outcome := rs.Evaluate(payload)
	out := testOutput{Outcome: outcome, Matched: []testMatch{}}
	for _, r := range matched {
		out.Matched = append(out.Matched, testMatch{
			Rule: r.Name, Tier: r.Tier, Action: r.Action, Message: r.Message,
		})
	}

	if *asJSON {
		if code := writeJSON(stdout, stderr, out); code != 0 {
			return code
		}
	} else {
		for _, m := range out.Matched {
			fmt.Fprintf(stdout, "%s  %s  %s\n", m.Action, m.Rule, m.Tier)
			for line := range strings.SplitSeq(m.Message, "\n") {
				if line == "" {
					fmt.Fprintln(stdout)
					continue
				}
				fmt.Fprintf(stdout, "  %s\n", line)
			}
			fmt.Fprintln(stdout)
		}
		fmt.Fprintf(stdout, "outcome: %s\n", out.Outcome)
	}

	if out.Outcome == rule.Block {
		return 2
	}
	return 0
}
