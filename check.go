package main

import (
	"cmp"
	"flag"
	"fmt"
	"io"
	"text/tabwriter"

	"github.com/svyatov/handrail/internal/rule"
)

type checkRule struct {
	Rule       string  `json:"rule"`
	Tier       string  `json:"tier"`
	Event      string  `json:"event"`
	Kind       string  `json:"kind"`
	Action     string  `json:"action"`
	Enabled    bool    `json:"enabled"`
	ShadowedBy *string `json:"shadowed_by"`
	Path       string  `json:"path"`
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

	if *asJSON {
		out := checkOutput{
			Rules:  make([]checkRule, 0, len(rs.Rules)),
			Errors: make([]checkError, 0, len(rs.Problems)),
		}
		for _, r := range rs.Rules {
			var shadowedBy *string
			if r.ShadowedBy != nil {
				shadowedBy = &r.ShadowedBy.Path
			}
			out.Rules = append(out.Rules, checkRule{
				Rule:       r.Name,
				Tier:       r.Tier,
				Event:      r.Event,
				Kind:       r.Kind,
				Action:     r.Action.String(),
				Enabled:    r.Enabled,
				ShadowedBy: shadowedBy,
				Path:       r.Path,
			})
		}
		for _, p := range rs.Problems {
			out.Errors = append(out.Errors, checkError{Path: p.Path, Message: p.Message})
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
	}

	if len(rs.Problems) > 0 {
		return 1
	}
	return 0
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
		case !r.Enabled:
			status = "disabled"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Tier, r.Name, cmp.Or(r.Event, "-"), cmp.Or(r.Kind, "*"), r.Action, status)
	}
	return tw.Flush()
}
