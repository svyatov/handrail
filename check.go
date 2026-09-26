package main

import (
	"cmp"
	"flag"
	"fmt"
	"io"
	"slices"
	"text/tabwriter"

	"github.com/svyatov/handrail/internal/harness"
	"github.com/svyatov/handrail/internal/rule"
)

// checkRule is one rule in docs/spec.md section 6's check shape.
type checkRule struct {
	Rule        string        `json:"rule"`
	Tier        string        `json:"tier"`
	Event       string        `json:"event"`
	Kind        string        `json:"kind"`
	Action      string        `json:"action"`
	Enabled     bool          `json:"enabled"`
	Trial       bool          `json:"trial"`
	ShadowedBy  *string       `json:"shadowed_by"`
	DroppedBy   *string       `json:"dropped_by"`
	DemotedFrom *string       `json:"demoted_from"`
	Path        string        `json:"path"`
	Examples    checkExamples `json:"examples"`
	// Stats is an effective rule's Decision log history, under --stats.
	Stats *ruleStats `json:"stats,omitempty"`
}

type checkExamples struct {
	Passed int            `json:"passed"`
	Failed []checkExample `json:"failed"`
}

type checkExample struct {
	Expect string         `json:"expect"`
	Fields map[string]any `json:"fields"`
	Line   int            `json:"line"`
	From   *string        `json:"from"` // the replaced rule's file for a drifted Example, else null
}

type checkError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type checkOutput struct {
	Rules  []checkRule  `json:"rules"`
	Stats  *logStats    `json:"stats,omitempty"`
	Errors []checkError `json:"errors"`
}

func cmdCheck(args []string, _ io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("check", flag.ContinueOnError)
	flags.SetOutput(stderr)

	asJSON := flags.Bool("json", false, "print the effective ruleset as JSON")
	withStats := flags.Bool("stats", false, "add each effective rule's Decision log history")

	if !parseFlags(flags, args, stderr) {
		return 1
	}

	ruleset, err := loadRules(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)

		return 1
	}

	problems := ruleset.Invalid()

	// past is nil without --stats, which reads no history.
	var past *history

	if *withStats {
		noteGrant(ruleset.Root, stderr)

		past = new(history(projectLines(ruleset.Root, false)))
	}

	var examplesFailed bool

	if *asJSON {
		var out checkOutput

		out, examplesFailed = checkReport(ruleset, problems, past)
		if code := writeJSON(stdout, stderr, out); code != 0 {
			return code
		}
	} else {
		examplesFailed, err = printCheck(stdout, stderr, ruleset, problems, past)
		if err != nil {
			fmt.Fprintf(stderr, "handrail: %v\n", err)

			return 1
		}
	}

	if examplesFailed || len(problems) > 0 {
		return 1
	}

	return 0
}

// printCheck is check's human report: the annotated ruleset, its history when
// past is not nil, and on stderr every problem, tier move and failing
// Example. It reports whether any Example failed.
func printCheck(
	stdout, stderr io.Writer, ruleset *rule.Ruleset, problems []rule.Problem, past *history,
) (bool, error) {
	err := printRuleset(stdout, ruleset.Rules)
	if err == nil && past != nil {
		err = printStats(stdout, ruleset.Effective(), *past)
	}

	if err != nil {
		return false, err
	}

	reportProblems(problems, stderr)
	reportTierMoves(ruleset, stderr)

	return reportExamples(slices.Concat(ruleset.Rules, ruleset.Untrusted), stderr), nil
}

// checkReport is check --json's shape of the ruleset and its problems, with
// its history when past is not nil, and whether any Example failed.
func checkReport(ruleset *rule.Ruleset, problems []rule.Problem, past *history) (checkOutput, bool) {
	var examplesFailed bool

	out := checkOutput{
		Rules:  make([]checkRule, 0, len(ruleset.Rules)),
		Stats:  nil,
		Errors: make([]checkError, 0, len(problems)),
	}
	for _, r := range ruleset.Rules {
		entry := checkRuleOf(r)
		if past != nil && r.Live() {
			entry.Stats = new(past.of(r.Name))
		}

		examplesFailed = examplesFailed || len(entry.Examples.Failed) > 0
		out.Rules = append(out.Rules, entry)
	}

	if past != nil {
		out.Stats = new(past.stats())
	}

	for _, p := range problems {
		out.Errors = append(out.Errors, checkError{Path: p.Path, Message: p.Message})
	}
	// An untrusted tier's rules are not in the effective ruleset, so a
	// failing Example there has no rule entry to sit on.
	for _, r := range ruleset.Untrusted {
		for _, e := range harness.FailingExamples(r) {
			examplesFailed = true

			out.Errors = append(out.Errors, checkError{Path: r.Path, Message: exampleFailure(r, e)})
		}
	}

	return out, examplesFailed
}

// checkRuleOf is one rule of the effective ruleset in check --json's shape,
// with its Examples run.
func checkRuleOf(entry *rule.Rule) checkRule {
	examples := checkExamples{Passed: len(entry.Examples), Failed: []checkExample{}}
	for _, failure := range harness.FailingExamples(entry) {
		// A drifted Example belongs to the rule this one replaces, so
		// it fails here and counts nothing toward this rule's passes.
		var from *string

		if failure.From == entry {
			examples.Passed--
		} else {
			from = &failure.From.Path
		}

		fields := make(map[string]any, len(failure.Fields))
		for _, f := range failure.Fields {
			fields[f.Name] = f.Value()
		}

		examples.Failed = append(examples.Failed,
			checkExample{Expect: failure.Expect, Fields: fields, Line: failure.Line, From: from})
	}

	var demoted *string
	if entry.DemotedFrom != "" {
		demoted = &entry.DemotedFrom
	}

	return checkRule{
		Rule:        entry.Name,
		Tier:        entry.Tier,
		Event:       entry.Event,
		Kind:        entry.Kind,
		Action:      entry.Action.String(),
		Enabled:     entry.Enabled,
		Trial:       entry.Trial,
		ShadowedBy:  pathOf(entry.ShadowedBy),
		DroppedBy:   pathOf(entry.DroppedBy),
		DemotedFrom: demoted,
		Path:        entry.Path,
		Examples:    examples,
		Stats:       nil,
	}
}

// pathOf is the file a rule reference names in check --json, null for none.
func pathOf(r *rule.Rule) *string {
	if r == nil {
		return nil
	}

	return &r.Path
}

// reportExamples runs every rule's Examples, names each failing one on stderr,
// and reports whether any failed.
func reportExamples(rules []*rule.Rule, stderr io.Writer) bool {
	failures := exampleFailures(rules)
	for _, f := range failures {
		fmt.Fprintf(stderr, "handrail: %s\n", f)
	}

	return len(failures) > 0
}

// exampleFailures runs every rule's Examples and names each failing one with
// the file that holds its rule.
func exampleFailures(rules []*rule.Rule) []string {
	var out []string

	for _, r := range rules {
		for _, e := range harness.FailingExamples(r) {
			out = append(out, r.Path+": "+exampleFailure(r, e))
		}
	}

	return out
}

// reportTierMoves names each Project-shared file dropped for naming a Global
// rule, with both paths, and each Project-personal file read as Project-shared,
// with the reason. Neither is an error: the repository's author cannot see the
// Global file, so the user could not fix it either, and a demoted file is still
// read.
func reportTierMoves(ruleset *rule.Ruleset, stderr io.Writer) {
	for _, entry := range slices.Concat(ruleset.Rules, ruleset.Untrusted) {
		if entry.DemotedFrom != "" {
			fmt.Fprintf(stderr, "handrail: %s: read as Project-shared: %s\n", entry.Path, ruleset.Demoted)
		}

		if entry.DroppedBy != nil {
			fmt.Fprintf(stderr, "handrail: %s: dropped: a Project-shared rule may not replace the Global rule %s\n",
				entry.Path, entry.DroppedBy.Path)
		}
	}
}

// exampleFailure names a failing Example of r the way a rule file problem is
// named, adding the file that holds it when that is the rule r replaces.
func exampleFailure(r *rule.Rule, failure harness.Failure) string {
	line := fmt.Sprintf("line %d", failure.Line)
	if failure.From != r {
		line += " of " + failure.From.Path
	}

	return fmt.Sprintf("%s: %s example fails: %s", line, failure.Expect, failure.Example)
}

// columnGap is the spaces between printRuleset's columns.
const columnGap = 2

// printRuleset renders the effective ruleset annotated with tier, shadowing,
// disabling and trial: what check reports, and what sync repeats once it has written.
func printRuleset(w io.Writer, rules []*rule.Rule) error {
	if len(rules) == 0 {
		return nil
	}

	table := tabwriter.NewWriter(w, 0, 0, columnGap, ' ', 0)
	fmt.Fprintln(table, "TIER\tRULE\tEVENT\tKIND\tACTION\tSTATUS")

	for _, entry := range rules {
		status := "enabled"

		switch {
		case entry.ShadowedBy != nil:
			status = "shadowed by " + entry.ShadowedBy.Tier
		case entry.DroppedBy != nil:
			status = "dropped by " + entry.DroppedBy.Tier
		case !entry.Enabled:
			status = "disabled"
		case entry.Trial:
			status = "trial"
		}

		fmt.Fprintf(table, "%s\t%s\t%s\t%s\t%s\t%s\n",
			entry.Tier, entry.Name, cmp.Or(entry.Event, "-"), cmp.Or(entry.Kind, "*"), entry.Action, status)
	}

	return table.Flush()
}
