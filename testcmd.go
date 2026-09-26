package main

import (
	"errors"
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
	key, value, ok := strings.Cut(s, "=")
	if !ok {
		return fmt.Errorf("%w, got %q", errNotKeyValue, s)
	}

	for i := range *f {
		if (*f)[i].Name == key {
			(*f)[i].Values = append((*f)[i].Values, value)

			return nil
		}
	}

	*f = append(*f, rule.ExampleField{Name: key, Values: []string{value}})

	return nil
}

var (
	errNotKeyValue = errors.New("expected key=value")
	errCaptureKind = errors.New("a capture's tool names its kind, so --stdin takes no kind")
	errNotOneCall  = errors.New("--field varies one call; write the call with --field alone")
)

type testMatch struct {
	Rule   string `json:"rule"`
	Tier   string `json:"tier"`
	Action string `json:"action"`
	// DegradedFrom is the action the rule file names where the harness
	// delivers another, else null.
	DegradedFrom *string `json:"degraded_from"`
	Message      string  `json:"message"`
	// Trial marks a rule on trial, which contributes nothing to the outcome.
	Trial bool `json:"trial"`
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
	// Enforcement is the Enforcement state. The rest of the report is what
	// hook does under enforce, since under another it delivers nothing.
	Enforcement string `json:"enforcement"`
}

func cmdTest(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("test", flag.ContinueOnError)
	flags.SetOutput(stderr)
	kind := flags.String("kind", "", "tool kind of the synthetic payload")

	var fields fieldSet
	flags.Var(&fields, "field", "canonical payload field as key=value, repeatable")
	fromStdin := flags.Bool("stdin", false, "read a harness payload JSON from stdin")
	only := flags.String("harness", "claude", "read the payload as this harness sends it")
	asJSON := flags.Bool("json", false, "print the result as JSON")

	event := leadingArg(args)
	if event != "" {
		args = args[1:]
	}

	if !parseFlags(flags, args, stderr) {
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

	adapter, known := harness.Lookup(*only)
	if !known {
		fmt.Fprintf(stderr, "handrail test: unknown harness %q; known: %s\n",
			*only, strings.Join(harness.Names(), ", "))

		return 1
	}

	payloads, failures, err := testCall(adapter, event, *kind, fields, *fromStdin, stdin)
	if err != nil {
		fmt.Fprintf(stderr, "handrail test: %v\n", err)

		return 1
	}

	// test is an authoring-time surface, so it is strict like check: the loud
	// fail-open belongs to the event-time hook path, not here.
	ruleset := loadValidRules(stderr)
	if ruleset == nil {
		return 1
	}

	out, outcome := testReport(adapter, ruleset, event, payloads, failures)
	if !*asJSON {
		printTest(stdout, out)
	} else if code := writeJSON(stdout, stderr, out); code != 0 {
		return code
	}

	return testExit(outcome)
}

// test's exit codes for the Outcome it reports, past the 1 of a failed run.
const (
	exitBlocked = 2
	exitAsked   = 3
)

// testExit is test's exit code for the Outcome it reports; allow is 0.
func testExit(outcome rule.Outcome) int {
	switch outcome {
	case rule.Block:
		return exitBlocked
	case rule.Ask:
		return exitAsked
	default:
		return 0
	}
}

// leadingArg is a command's leading positional argument, test's event or
// mode's state, or "" when args opens with a flag. It is pulled before flag
// parsing: the stdlib flag package stops at the first non-flag argument.
func leadingArg(args []string) string {
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		return args[0]
	}

	return ""
}

// testCall builds the payloads test evaluates from its --kind and --field
// flags, and from the capture on stdin when fromStdin is set, with the
// capture's own failures to read it. The error is why test cannot build one.
func testCall(
	adapter harness.Adapter, event, kind string, fields fieldSet, fromStdin bool, stdin io.Reader,
) ([]rule.Payload, []string, error) {
	// --kind writes the kind field, so a second kind written beside it is a
	// list where one value goes.
	if kind != "" {
		_ = fields.Set("kind=" + kind)
	}
	// A call test builds is one an Example could write, so hook never meets a
	// payload test cannot build, and test never builds one hook never meets.
	err := rule.CheckCall(event, fields)
	if err != nil {
		return nil, nil, err
	}

	for _, f := range fields {
		if f.Name == "kind" {
			kind = f.Values[0]
		}
	}

	if fromStdin {
		return captureCall(adapter, event, kind, fields, stdin)
	}
	// Without a capture, the call is the one an Example with these fields
	// is, and one that names no kind is a call handrail does not classify.
	call := rule.Payload{Event: event, Kind: kind, StopHookActive: false}
	if call.Kind == "" && rule.ToolEvent(event) {
		call.Kind = "other"
	}

	return adapter.WithFields(call, fields), nil, nil
}

// captureCall reads the capture on stdin, with the fields written over its one
// payload.
func captureCall(
	adapter harness.Adapter, event, kind string, fields fieldSet, stdin io.Reader,
) ([]rule.Payload, []string, error) {
	// A capture's kind is its tool's, and a kind written over it would
	// build a payload the Adapter never builds.
	if kind != "" {
		return nil, nil, errCaptureKind
	}
	// The hook path's own reading, so a capture read here reads exactly as
	// it will there, a broken one included. The cwd it reports is dropped,
	// because test answers for the ruleset in the working directory rather
	// than the one the capture was taken under.
	payloads, _, err := readCall(adapter, event, stdin)

	var failures []string
	if err != nil {
		failures = append(failures, err.Error())
	}

	switch {
	case len(fields) == 0:
	case len(payloads) != 1:
		// Several payloads, such as a patch's, are no one call to vary:
		// each is its own edit. None, from an internal agent, is no call.
		return nil, nil, fmt.Errorf("the capture yields %d payloads, and %w", len(payloads), errNotOneCall)
	default:
		// Flags win over the capture's one payload, and the call is read
		// again, so a written command yields what the harness makes of it.
		payloads = adapter.WithFields(payloads[0], fields)
	}

	return payloads, failures, nil
}

// testReport evaluates the call and is what test shows of it: the payloads the
// ruleset yields, the rules they matched with the action the harness
// delivers, the Outcome, and the human line.
func testReport(
	adapter harness.Adapter, ruleset *rule.Ruleset, event string, payloads []rule.Payload, failures []string,
) (testOutput, rule.Outcome) {
	// An authoring surface that reported allow under every state but enforce
	// would answer nothing, so test evaluates as enforce and names the state.
	out := testOutput{
		Outcome: "", Payloads: []testPayload{}, Matched: []testMatch{}, Human: "", Enforcement: ruleset.State.String(),
	}
	ruleset.State = rule.StateEnforce
	// The same call the hook path makes, so what test reports is what hook does.
	matched, _ := ruleset.Evaluate(payloads)

	for _, p := range ruleset.Yield(payloads) {
		view := testPayload{Kind: p.Kind, Fields: p.Fields(), Unreadable: []string{}}
		for _, c := range view.Fields["unreadable"] {
			view.Unreadable = append(view.Unreadable, c.Spellings[0])
		}

		delete(view.Fields, "unreadable")
		out.Payloads = append(out.Payloads, view)
	}

	for _, found := range matched {
		action := adapter.Action(found.Rule)

		match := testMatch{
			Rule: found.Name, Tier: found.Tier, Action: action.String(), DegradedFrom: nil, Message: found.Message,
			Trial: found.Trial,
		}
		if action != found.Action {
			match.DegradedFrom = new(found.Action.String())
		}

		out.Matched = append(out.Matched, match)
	}
	// The Outcome reported is the one the harness delivers, since that is what
	// hook does with the evaluated one.
	outcome := adapter.Delivered(matched)
	out.Outcome = outcome.String()

	failures = append(failures, loadNotices(ruleset)...)
	out.Human = messages(adapter, ruleset, event, matched, failures).human

	return out, outcome
}

// printTest is test's human report: each payload, each matched rule, the
// outcome, and the human line.
func printTest(stdout io.Writer, out testOutput) {
	for i, p := range out.Payloads {
		printPayload(stdout, i, len(out.Payloads), p)
	}

	for _, match := range out.Matched {
		fmt.Fprintf(stdout, "%s  %s  %s", match.Action, match.Rule, match.Tier)

		if match.DegradedFrom != nil {
			fmt.Fprintf(stdout, "  degraded from %s", *match.DegradedFrom)
		}

		if match.Trial {
			fmt.Fprint(stdout, "  trial")
		}

		fmt.Fprintln(stdout)
		printIndented(stdout, match.Message)
		fmt.Fprintln(stdout)
	}

	fmt.Fprintf(stdout, "outcome: %s\n", out.Outcome)

	if out.Enforcement != rule.StateEnforce.String() {
		fmt.Fprintf(stdout, "enforcement: %s, so hook delivers nothing\n", out.Enforcement)
	}

	if out.Human == "" {
		fmt.Fprintln(stdout, "human: none")
	} else {
		fmt.Fprintln(stdout, "human:")
		printIndented(stdout, out.Human)
	}
}

// printPayload writes payload i of n: its kind, each field's Candidates by
// field name, and what handrail could not read.
func printPayload(stdout io.Writer, i, n int, payload testPayload) {
	fmt.Fprintf(stdout, "payload %d of %d", i+1, n)

	if payload.Kind != "" {
		fmt.Fprintf(stdout, ": %s", payload.Kind)
	}

	fmt.Fprintln(stdout)

	for _, name := range slices.Sorted(maps.Keys(payload.Fields)) {
		for _, c := range payload.Fields[name] {
			printCandidate(stdout, name, c)
		}
	}

	if len(payload.Unreadable) > 0 {
		fmt.Fprintf(stdout, "  unreadable: %s\n", strings.Join(payload.Unreadable, ", "))
	}

	fmt.Fprintln(stdout)
}

// printCandidate writes one Candidate of the named field with its Spellings.
func printCandidate(stdout io.Writer, name string, candidate rule.CandidateView) {
	switch {
	case candidate.Whole:
		fmt.Fprintf(stdout, "  %s (whole line, positive terms only)\n", name)
	case len(candidate.Spellings) == 0:
		fmt.Fprintf(stdout, "  %s (runs no command)\n", name)
	default:
		fmt.Fprintf(stdout, "  %s\n", name)
	}

	for _, s := range candidate.Spellings {
		for line := range strings.SplitSeq(s, "\n") {
			fmt.Fprintf(stdout, "    %s\n", line)
		}
	}
}

// printIndented writes text two spaces in, keeping its blank lines blank.
func printIndented(stdout io.Writer, text string) {
	for line := range strings.SplitSeq(text, "\n") {
		if line == "" {
			fmt.Fprintln(stdout)

			continue
		}

		fmt.Fprintf(stdout, "  %s\n", line)
	}
}
