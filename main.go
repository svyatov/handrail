// Command handrail enforces declarative guardrails across agentic coding harnesses.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/svyatov/handrail/internal/rule"
)

// Injected at build time by GoReleaser via -ldflags -X.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const usage = `Usage: handrail <command> [arguments]

Commands:
  check     Validate the rules and print the effective ruleset
  version   Print version, commit, and build date
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

// run is the CLI seam: every command dispatches from here, and the exit code
// is the return value rather than an os.Exit deep in a subcommand.
func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 1
	}
	switch args[0] {
	case "check":
		return cmdCheck(args[1:], stdout, stderr)
	case "version":
		return cmdVersion(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "handrail: unknown command %q\n\n", args[0])
		fmt.Fprint(stderr, usage)
		return 1
	}
}

func cmdVersion(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	fmt.Fprintf(stdout, "handrail %s\ncommit: %s\ndate: %s\n", version, commit, date)
	return 0
}

// tierProjectShared is the only tier check reads today; the Global and
// Project-personal tiers land with tier discovery.
const tierProjectShared = "project-shared"

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

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)
		return 1
	}
	rules, problems := rule.Load(filepath.Join(rule.RepoRoot(cwd), ".handrail"))

	if *asJSON {
		out := checkOutput{
			Rules:  make([]checkRule, 0, len(rules)),
			Errors: make([]checkError, 0, len(problems)),
		}
		for _, r := range rules {
			out.Rules = append(out.Rules, checkRule{
				Rule:    r.Name,
				Tier:    tierProjectShared,
				Event:   r.Event,
				Kind:    r.Kind,
				Action:  r.Action,
				Enabled: r.Enabled,
				Path:    r.Path,
			})
		}
		for _, p := range problems {
			out.Errors = append(out.Errors, checkError{Path: p.Path, Message: p.Message})
		}
		enc := json.NewEncoder(stdout)
		enc.SetIndent("", "  ")
		if err := enc.Encode(out); err != nil {
			fmt.Fprintf(stderr, "handrail: %v\n", err)
			return 1
		}
	} else {
		if len(rules) > 0 {
			w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "TIER\tRULE\tEVENT\tKIND\tACTION\tSTATUS")
			for _, r := range rules {
				status := "enabled"
				if !r.Enabled {
					status = "disabled"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					tierProjectShared, r.Name, orDefault(r.Event, "-"), orDefault(r.Kind, "*"), r.Action, status)
			}
			if err := w.Flush(); err != nil {
				fmt.Fprintf(stderr, "handrail: %v\n", err)
				return 1
			}
		}
		for _, p := range problems {
			fmt.Fprintf(stderr, "handrail: %s: %s\n", p.Path, p.Message)
		}
	}

	if len(problems) > 0 {
		return 1
	}
	return 0
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
