// Command handrail enforces declarative guardrails across agentic coding harnesses.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"
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
  test      Dry-run a synthetic payload against the rules
  trust     Grant this repo's Project-shared rules
  version   Print version, commit, and build date
`

const testUsage = `Usage: handrail test <event> [--kind kind] [--field key=value]... [--stdin] [--json]
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run is the CLI seam: every command dispatches from here, and the exit code
// is the return value rather than an os.Exit deep in a subcommand.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprint(stderr, usage)
		return 1
	}
	switch args[0] {
	case "check":
		return cmdCheck(args[1:], stdout, stderr)
	case "test":
		return cmdTest(args[1:], stdin, stdout, stderr)
	case "trust":
		return cmdTrust(args[1:], stdout, stderr)
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

func cmdTrust(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("trust", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "handrail trust: unexpected argument %q\n", fs.Arg(0))
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)
		return 1
	}
	root := rule.RepoRoot(cwd)
	added, err := rule.Trust(root)
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)
		return 1
	}
	if added {
		fmt.Fprintf(stdout, "trusted %s\n", root)
	} else {
		fmt.Fprintf(stdout, "already trusted %s\n", root)
	}
	return 0
}

// loadRules reads the tiers that apply to the working directory and reports an
// untrusted shared tier, which is skipped rather than silently missing.
func loadRules(stderr io.Writer) ([]*rule.Rule, []rule.Problem, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}
	rules, problems, d := rule.LoadTiers(cwd)
	if d.Skipped {
		fmt.Fprintf(stderr, "handrail: skipping the untrusted Project-shared rules in %s; run handrail trust to enable them\n", d.Root)
	}
	return rules, problems, nil
}

func writeJSON(stdout, stderr io.Writer, v any) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)
		return 1
	}
	return 0
}

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

	rules, problems, err := loadRules(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)
		return 1
	}

	if *asJSON {
		out := checkOutput{
			Rules:  make([]checkRule, 0, len(rules)),
			Errors: make([]checkError, 0, len(problems)),
		}
		for _, r := range rules {
			var shadowedBy *string
			if r.ShadowedBy != "" {
				shadowedBy = &r.ShadowedBy
			}
			out.Rules = append(out.Rules, checkRule{
				Rule:       r.Name,
				Tier:       r.Tier,
				Event:      r.Event,
				Kind:       r.Kind,
				Action:     r.Action,
				Enabled:    r.Enabled,
				ShadowedBy: shadowedBy,
				Path:       r.Path,
			})
		}
		for _, p := range problems {
			out.Errors = append(out.Errors, checkError{Path: p.Path, Message: p.Message})
		}
		if code := writeJSON(stdout, stderr, out); code != 0 {
			return code
		}
	} else {
		if len(rules) > 0 {
			// Rules arrive in tier order, so the last one under a name is the
			// effective one: the tier that shadows every earlier namesake.
			effective := make(map[string]string, len(rules))
			for _, r := range rules {
				effective[r.Name] = r.Tier
			}
			w := tabwriter.NewWriter(stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "TIER\tRULE\tEVENT\tKIND\tACTION\tSTATUS")
			for _, r := range rules {
				status := "enabled"
				switch {
				case r.ShadowedBy != "":
					status = "shadowed by " + effective[r.Name]
				case !r.Enabled:
					status = "disabled"
				}
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
					r.Tier, r.Name, orDefault(r.Event, "-"), orDefault(r.Kind, "*"), r.Action, status)
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
	fromStdin := fs.Bool("stdin", false, "read a canonical payload JSON from stdin")
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

	payload := rule.Payload{Event: event, Fields: map[string]string{}}
	if *fromStdin {
		var raw map[string]any
		if err := json.NewDecoder(stdin).Decode(&raw); err != nil {
			fmt.Fprintf(stderr, "handrail test: reading payload: %v\n", err)
			return 1
		}
		if k, ok := raw["tool_kind"].(string); ok {
			if !rule.IsKind(k) {
				fmt.Fprintf(stderr, "handrail test: unknown kind %q\n", k)
				return 1
			}
			payload.Kind = k
		}
		// Anything the matcher cannot address, raw.* included, is ignored here.
		for k, v := range raw {
			if s, ok := v.(string); ok && rule.IsField(k) {
				payload.Fields[k] = s
			}
		}
	}
	// Flags win over the payload, so one field can be varied against a capture.
	for k, v := range fields {
		payload.Fields[k] = v
	}
	if *kind != "" {
		payload.Kind = *kind
	}

	rules, problems, err := loadRules(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)
		return 1
	}
	// test is an authoring-time surface, so it is strict like check: the loud
	// fail-open belongs to the event-time hook path, not here.
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintf(stderr, "handrail: %s: %s\n", p.Path, p.Message)
		}
		return 1
	}

	out := testOutput{Outcome: "allow", Matched: []testMatch{}}
	for _, r := range rules {
		// A shadowed rule is not the effective rule; its namesake is.
		if r.ShadowedBy != "" || !r.Matches(payload) {
			continue
		}
		out.Matched = append(out.Matched, testMatch{
			Rule: r.Name, Tier: r.Tier, Action: r.Action, Message: r.Message,
		})
		if r.Action == "block" {
			out.Outcome = "block"
		} else if out.Outcome == "allow" {
			out.Outcome = "warn"
		}
	}

	if *asJSON {
		if code := writeJSON(stdout, stderr, out); code != 0 {
			return code
		}
	} else {
		for _, m := range out.Matched {
			fmt.Fprintf(stdout, "%s  %s  %s\n", m.Action, m.Rule, m.Tier)
			for _, line := range strings.Split(m.Message, "\n") {
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

	if out.Outcome == "block" {
		return 2
	}
	return 0
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
