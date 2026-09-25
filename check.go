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

// checkRule is one rule in docs/spec.md section 6's check shape. Trial and
// DemotedFrom hold their zero values until trial rules and the supply check
// land.
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
	Errors []checkError `json:"errors"`
}

func cmdCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the effective ruleset as JSON")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "handrail check: unexpected argument %q\n", fs.Arg(0))
		return 1
	}

	rs, err := loadRules(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)
		return 1
	}

	var examplesFailed bool
	if *asJSON {
		out := checkOutput{
			Rules:  make([]checkRule, 0, len(rs.Rules)),
			Errors: make([]checkError, 0, len(rs.Problems)),
		}
		for _, r := range rs.Rules {
			failing := harness.FailingExamples(r)
			examplesFailed = examplesFailed || len(failing) > 0
			examples := checkExamples{Passed: len(r.Examples), Failed: []checkExample{}}
			for _, e := range failing {
				// A drifted Example belongs to the rule this one replaces, so
				// it fails here and counts nothing toward this rule's passes.
				var from *string
				if e.From == r {
					examples.Passed--
				} else {
					from = &e.From.Path
				}
				fields := make(map[string]any, len(e.Fields))
				for _, f := range e.Fields {
					fields[f.Name] = f.Value()
				}
				examples.Failed = append(examples.Failed, checkExample{Expect: e.Expect, Fields: fields, Line: e.Line, From: from})
			}
			out.Rules = append(out.Rules, checkRule{
				Rule:       r.Name,
				Tier:       r.Tier,
				Event:      r.Event,
				Kind:       r.Kind,
				Action:     r.Action.String(),
				Enabled:    r.Enabled,
				ShadowedBy: pathOf(r.ShadowedBy),
				DroppedBy:  pathOf(r.DroppedBy),
				Path:       r.Path,
				Examples:   examples,
			})
		}
		for _, p := range rs.Problems {
			out.Errors = append(out.Errors, checkError{Path: p.Path, Message: p.Message})
		}
		// An untrusted tier's rules are not in the effective ruleset, so a
		// failing Example there has no rule entry to sit on.
		for _, r := range rs.Untrusted {
			for _, e := range harness.FailingExamples(r) {
				examplesFailed = true
				out.Errors = append(out.Errors, checkError{Path: r.Path, Message: exampleFailure(r, e)})
			}
		}
		if code := writeJSON(stdout, stderr, out); code != 0 {
			return code
		}
	} else {
		if err := printRuleset(stdout, rs.Rules); err != nil {
			fmt.Fprintf(stderr, "handrail: %v\n", err)
			return 1
		}
		for _, p := range rs.Problems {
			fmt.Fprintf(stderr, "handrail: %s: %s\n", p.Path, p.Message)
		}
		reportDropped(rs.Rules, stderr)
		examplesFailed = reportExamples(slices.Concat(rs.Rules, rs.Untrusted), stderr)
	}

	if examplesFailed || len(rs.Problems) > 0 {
		return 1
	}
	return 0
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
	failed := false
	for _, r := range rules {
		for _, e := range harness.FailingExamples(r) {
			fmt.Fprintf(stderr, "handrail: %s: %s\n", r.Path, exampleFailure(r, e))
			failed = true
		}
	}
	return failed
}

// reportDropped names each Project-shared file dropped for naming a Global
// rule, with both paths. It is no error: the repository's author cannot see
// the Global file, so the user could not fix it either.
func reportDropped(rules []*rule.Rule, stderr io.Writer) {
	for _, r := range rules {
		if r.DroppedBy != nil {
			fmt.Fprintf(stderr, "handrail: %s: dropped: a Project-shared rule may not replace the Global rule %s\n",
				r.Path, r.DroppedBy.Path)
		}
	}
}

// exampleFailure names a failing Example of r the way a rule file problem is
// named, adding the file that holds it when that is the rule r replaces.
func exampleFailure(r *rule.Rule, e harness.Failure) string {
	line := fmt.Sprintf("line %d", e.Line)
	if e.From != r {
		line += " of " + e.From.Path
	}
	return fmt.Sprintf("%s: %s example fails: %s", line, e.Expect, e.Example)
}

// printRuleset renders the effective ruleset annotated with tier, shadowing,
// and disabling: what check reports, and what sync repeats once it has written.
func printRuleset(w io.Writer, rules []*rule.Rule) error {
	if len(rules) == 0 {
		return nil
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TIER\tRULE\tEVENT\tKIND\tACTION\tSTATUS")
	for _, r := range rules {
		status := "enabled"
		switch {
		case r.ShadowedBy != nil:
			status = "shadowed by " + r.ShadowedBy.Tier
		case r.DroppedBy != nil:
			status = "dropped by " + r.DroppedBy.Tier
		case !r.Enabled:
			status = "disabled"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Tier, r.Name, cmp.Or(r.Event, "-"), cmp.Or(r.Kind, "*"), r.Action, status)
	}
	return tw.Flush()
}
