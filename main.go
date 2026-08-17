// Command handrail enforces declarative guardrails across agentic coding harnesses.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
)

// Injected at build time by GoReleaser via -ldflags -X.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

const usage = `Usage: handrail <command> [arguments]

Commands:
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
