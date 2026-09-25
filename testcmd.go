package main

import (
	"flag"
	"fmt"
	"io"
	"maps"
	"slices"
	"strings"

	"github.com/svyatov/handrail/internal/harness"
	"github.com/svyatov/handrail/internal/rule"
)

const testUsage = `Usage: handrail test <event> [--kind kind] [--field key=value]... [--stdin] [--harness name] [--json]

--field takes what a rule's Example takes; repeat it to give a list. With
--stdin, reads a harness payload as that harness sends it, normalized the way
the hook path normalizes it. A --field then replaces that field of the call,
which needs a capture that yields one payload; its tool names its kind, so
--kind does not apply.
`

// fieldSet collects --field key=value flags as an Example writes its fields: a
// repeated key is a list, in the order given.
type fieldSet []rule.ExampleField

func (f *fieldSet) String() string { return "" }

func (f *fieldSet) Set(s string) error {
	k, v, ok := strings.Cut(s, "=")
	if !ok {
		return fmt.Errorf("expected key=value, got %q", s)
	}

	for i := range *f {
		if (*f)[i].Name == k {
			(*f)[i].Values = append((*f)[i].Values, v)

			return nil
		}
	}

	*f = append(*f, rule.ExampleField{Name: k, Values: []string{v}})

	return nil
}

type testMatch struct {
	Rule   string `json:"rule"`
	Tier   string `json:"tier"`
	Action string `json:"action"`
	// DegradedFrom is the action the rule file names where the harness
	// delivers another, else null.
	DegradedFrom *string `json:"degraded_from"`
	Message      string  `json:"message"`
}

// testPayload is one payload the event yields, as handrail read it.
type testPayload struct {
	Kind       string                          `json:"kind,omitempty"`
	Fields     map[string][]rule.CandidateView `json:"fields"`
	Unreadable []string                        `json:"unreadable"`
}

type testOutput struct {
	Outcome  string        `json:"outcome"`
	Payloads []testPayload `json:"payloads"`
	Matched  []testMatch   `json:"matched"`
	// Human is the text hook would show the user, verbatim, and "" for none.
	Human string `json:"human"`
}

func cmdTest(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kind := fs.String("kind", "", "tool kind of the synthetic payload")

	var fields fieldSet
	fs.Var(&fields, "field", "canonical payload field as key=value, repeatable")
	fromStdin := fs.Bool("stdin", false, "read a harness payload JSON from stdin")
	only := fs.String("harness", "claude", "read the payload as this harness sends it")
	asJSON := fs.Bool("json", false, "print the result as JSON")

	event, args := leadingEvent(args)
	if !parseFlags(fs, args, stderr) {
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

	a, known := harness.Lookup(*only)
	if !known {
		fmt.Fprintf(stderr, "handrail test: unknown harness %q; known: %s\n",
			*only, strings.Join(harness.Names(), ", "))

		return 1
	}

	payloads, failures, code := testCall(a, event, *kind, fields, *fromStdin, stdin, stderr)
	if code != 0 {
		return code
	}

	// test is an authoring-time surface, so it is strict like check: the loud
	// fail-open belongs to the event-time hook path, not here.
	rs, code := loadValidRules(stderr)
	if code != 0 {
		return code
	}

	out, outcome := testReport(a, rs, event, payloads, failures)
	if *asJSON {
		if code := writeJSON(stdout, stderr, out); code != 0 {
			return code
		}
	} else {
		printTest(stdout, out)
	}

	return testExit[outcome]
}

// testExit is test's exit code for the Outcome it reports; allow is 0.
var testExit = map[rule.Outcome]int{rule.Block: 2, rule.Ask: 3}

// leadingEvent splits off the event. It is positional and leads, so it is
// pulled before flag parsing: the stdlib flag package stops at the first
// non-flag argument.
func leadingEvent(args []string) (event string, rest []string) {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:]
	}

	return "", args
}

// testCall builds the payloads test evaluates from its --kind and --field
// flags, and from the capture on stdin when fromStdin is set, with the
// capture's own failures to read it.
func testCall(a harness.Adapter, event, kind string, fields fieldSet, fromStdin bool, stdin io.Reader, stderr io.Writer) ([]rule.Payload, []string, int) {
	// --kind writes the kind field, so a second kind written beside it is a
	// list where one value goes.
	if kind != "" {
		_ = fields.Set("kind=" + kind)
	}
	// A call test builds is one an Example could write, so hook never meets a
	// payload test cannot build, and test never builds one hook never meets.
	if err := rule.CheckCall(event, fields); err != nil {
		fmt.Fprintf(stderr, "handrail test: %v\n", err)

		return nil, nil, 1
	}

	for _, f := range fields {
		if f.Name == "kind" {
			kind = f.Values[0]
		}
	}

	if fromStdin {
		return captureCall(a, event, kind, fields, stdin, stderr)
	}
	// Without a capture, the call is the one an Example with these fields
	// is, and one that names no kind is a call handrail does not classify.
	call := rule.Payload{Event: event, Kind: kind}
	if call.Kind == "" && rule.ToolEvent(event) {
		call.Kind = "other"
	}

	return a.WithFields(call, fields), nil, 0
}

// captureCall reads the capture on stdin, with the fields written over its one
// payload.
func captureCall(a harness.Adapter, event, kind string, fields fieldSet, stdin io.Reader, stderr io.Writer) ([]rule.Payload, []string, int) {
	// A capture's kind is its tool's, and a kind written over it would
	// build a payload the Adapter never builds.
	if kind != "" {
		fmt.Fprintln(stderr, "handrail test: a capture's tool names its kind, so --stdin takes no kind")

		return nil, nil, 1
	}
	// The hook path's own reading, so a capture read here reads exactly as
	// it will there, a broken one included. The cwd it reports is dropped,
	// because test answers for the ruleset in the working directory rather
	// than the one the capture was taken under.
	payloads, _, failures := readCall(a, event, stdin)
	switch {
	case len(fields) == 0:
	case len(payloads) != 1:
		// Several payloads, such as a patch's, are no one call to vary:
		// each is its own edit. None, from an internal agent, is no call.
		fmt.Fprintf(stderr, "handrail test: the capture yields %d payloads, and --field varies one call; write the call with --field alone\n", len(payloads))

		return nil, nil, 1
	default:
		// Flags win over the capture's one payload, and the call is read
		// again, so a written command yields what the harness makes of it.
		payloads = a.WithFields(payloads[0], fields)
	}

	return payloads, failures, 0
}

// testReport evaluates the call and is what test shows of it: the payloads the
// ruleset yields, the rules they matched with the action the harness
// delivers, the Outcome, and the human line.
func testReport(a harness.Adapter, rs *rule.Ruleset, event string, payloads []rule.Payload, failures []string) (testOutput, rule.Outcome) {
	// The same call the hook path makes, so what test reports is what hook does.
	matched, _ := rs.Evaluate(payloads)
	out := testOutput{Payloads: []testPayload{}, Matched: []testMatch{}}

	for _, p := range rs.Yield(payloads) {
		tp := testPayload{Kind: p.Kind, Fields: p.Fields(), Unreadable: []string{}}
		for _, c := range tp.Fields["unreadable"] {
			tp.Unreadable = append(tp.Unreadable, c.Spellings[0])
		}

		delete(tp.Fields, "unreadable")
		out.Payloads = append(out.Payloads, tp)
	}

	for _, r := range matched {
		action := a.Action(r.Rule)

		m := testMatch{Rule: r.Name, Tier: r.Tier, Action: action.String(), Message: r.Message}
		if action != r.Action {
			m.DegradedFrom = new(r.Action.String())
		}

		out.Matched = append(out.Matched, m)
	}
	// The Outcome reported is the one the harness delivers, since that is what
	// hook does with the evaluated one.
	outcome := a.Delivered(matched)
	out.Outcome = outcome.String()

	failures = append(failures, loadNotices(rs)...)
	_, out.Human = messages(a, rs, event, matched, failures)

	return out, outcome
}

// printTest is test's human report: each payload, each matched rule, the
// outcome, and the human line.
func printTest(w io.Writer, out testOutput) {
	for i, p := range out.Payloads {
		printPayload(w, i, len(out.Payloads), p)
	}

	for _, m := range out.Matched {
		fmt.Fprintf(w, "%s  %s  %s", m.Action, m.Rule, m.Tier)

		if m.DegradedFrom != nil {
			fmt.Fprintf(w, "  degraded from %s", *m.DegradedFrom)
		}

		fmt.Fprintln(w)
		printIndented(w, m.Message)
		fmt.Fprintln(w)
	}

	fmt.Fprintf(w, "outcome: %s\n", out.Outcome)

	if out.Human == "" {
		fmt.Fprintln(w, "human: none")
	} else {
		fmt.Fprintln(w, "human:")
		printIndented(w, out.Human)
	}
}

// printPayload writes payload i of n: its kind, each field's Candidates by
// field name, and what handrail could not read.
func printPayload(w io.Writer, i, n int, p testPayload) {
	fmt.Fprintf(w, "payload %d of %d", i+1, n)

	if p.Kind != "" {
		fmt.Fprintf(w, ": %s", p.Kind)
	}

	fmt.Fprintln(w)

	for _, name := range slices.Sorted(maps.Keys(p.Fields)) {
		for _, c := range p.Fields[name] {
			printCandidate(w, name, c)
		}
	}

	if len(p.Unreadable) > 0 {
		fmt.Fprintf(w, "  unreadable: %s\n", strings.Join(p.Unreadable, ", "))
	}

	fmt.Fprintln(w)
}

// printCandidate writes one Candidate of the named field with its Spellings.
func printCandidate(w io.Writer, name string, c rule.CandidateView) {
	switch {
	case c.Whole:
		fmt.Fprintf(w, "  %s (whole line, positive terms only)\n", name)
	case len(c.Spellings) == 0:
		fmt.Fprintf(w, "  %s (runs no command)\n", name)
	default:
		fmt.Fprintf(w, "  %s\n", name)
	}

	for _, s := range c.Spellings {
		for line := range strings.SplitSeq(s, "\n") {
			fmt.Fprintf(w, "    %s\n", line)
		}
	}
}

// printIndented writes text two spaces in, keeping its blank lines blank.
func printIndented(w io.Writer, text string) {
	for line := range strings.SplitSeq(text, "\n") {
		if line == "" {
			fmt.Fprintln(w)

			continue
		}

		fmt.Fprintf(w, "  %s\n", line)
	}
}
