// Command handrail enforces declarative guardrails across agentic coding harnesses.
package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

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
  sync      Install handrail's hook entries into every detected harness
  hook      Evaluate one harness event (installed by sync; not for humans)
  check     Validate the rules and print the effective ruleset
  test      Dry-run a synthetic payload against the rules
  trust     Grant this repo's Project-shared rules
  import    Convert upstream hookify rules into Project-personal rules
  doctor    Diagnose this machine's install, offline
  version   Print version, commit, and build date
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
	case "sync":
		return cmdSync(args[1:], stdout, stderr)
	case "hook":
		return cmdHook(args[1:], stdin, stdout, stderr)
	case "check":
		return cmdCheck(args[1:], stdout, stderr)
	case "test":
		return cmdTest(args[1:], stdin, stdout, stderr)
	case "trust":
		return cmdTrust(args[1:], stdout, stderr)
	case "import":
		return cmdImport(args[1:], stdout, stderr)
	case "doctor":
		return cmdDoctor(args[1:], stdout, stderr)
	case "version":
		return cmdVersion(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "handrail: unknown command %q\n\n", args[0])
		fmt.Fprint(stderr, usage)
		return 1
	}
}

// loadRules reads the tiers that apply to the working directory and reports an
// untrusted shared tier, which is skipped rather than silently missing.
func loadRules(stderr io.Writer) (*rule.Ruleset, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	rs := rule.Load(cwd)
	if notice := rs.TrustNotice(); notice != "" {
		fmt.Fprintln(stderr, notice)
	}
	return rs, nil
}

// loadValidRules is test's authoring-time contract: every tier parses, or the
// command stops without acting. check and sync report problems and keep going,
// check because reporting them is its whole job, sync because its hooks
// depend on no rule.
func loadValidRules(stderr io.Writer) (*rule.Ruleset, int) {
	rs, err := loadRules(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)
		return nil, 1
	}
	if problems := rs.Invalid(); len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintf(stderr, "handrail: %s: %s\n", p.Path, p.Message)
		}
		return nil, 1
	}
	return rs, 0
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
