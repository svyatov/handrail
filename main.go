// Command handrail enforces declarative guardrails across agentic coding harnesses.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
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
  log       Read the Decision log, or turn it on or off for this project
  mode      Read or set whether handrail enforces: enforce, trial, or off
  on, off   mode enforce and mode off
  import    Convert upstream hookify rules into Project-personal rules
  doctor    Diagnose this machine's install, offline
  survey    Print this repo's Repo signals and instruction files, as JSON
  version   Print version, commit, and build date
  help      Print this usage, or a command's own with help <command>
`

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

// run is the CLI seam: every command dispatches from here, and the exit code
// is the return value rather than an os.Exit deep in a subcommand.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	// commands are the entrypoints run dispatches to. Only hook and test read
	// stdin; the rest take it to share one signature. run is called once per
	// process, so building the table here costs what a package-level one would.
	commands := map[string]func(args []string, stdin io.Reader, stdout, stderr io.Writer) int{
		"sync":      cmdSync,
		"hook":      cmdHook,
		"check":     cmdCheck,
		"test":      cmdTest,
		"trust":     cmdTrust,
		"log":       cmdLog,
		"mode":      cmdMode,
		"on":        modeAlias("enforce"),
		"off":       modeAlias("off"),
		"import":    cmdImport,
		"doctor":    cmdDoctor,
		"survey":    cmdSurvey,
		"version":   cmdVersion,
		"--version": cmdVersion,
		"-v":        cmdVersion,
		"help":      cmdHelp,
		"--help":    cmdHelp,
		"-h":        cmdHelp,
	}

	if len(args) == 0 {
		fmt.Fprint(stderr, usage)

		return 1
	}

	cmd, ok := commands[args[0]]
	if !ok {
		fmt.Fprintf(stderr, "handrail: unknown command %q\n\n", args[0])
		fmt.Fprint(stderr, usage)

		return 1
	}

	return cmd(args[1:], stdin, stdout, stderr)
}

// cmdHelp prints the usage on stdout: asked for, it is an answer, not an error.
// Given a command, it asks that command for its own. A flag in its place is no
// command, so help asked about itself, which sends it -h, answers once.
func cmdHelp(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	if name := leadingArg(args); name != "" {
		return run([]string{name, "-h"}, stdin, stdout, stderr)
	}

	fmt.Fprint(stdout, usage)

	return 0
}

// parseArgs parses a command's flags, reporting whether the command may go on
// and, when it may not, its exit code. A help request is answered on stdout
// with 0; a bad flag is a usage error on stderr with 1.
func parseArgs(flags *flag.FlagSet, args []string, stdout, stderr io.Writer) (int, bool) {
	// Which stream the usage belongs on is only known once Parse has written it.
	var out bytes.Buffer
	flags.SetOutput(&out)

	err := flags.Parse(args)
	switch {
	case errors.Is(err, flag.ErrHelp):
		fmt.Fprint(stdout, out.String())

		return 0, false
	case err != nil:
		fmt.Fprint(stderr, out.String())

		return 1, false
	}

	return 0, true
}

// parseFlags is parseArgs for a command that takes no positional argument.
func parseFlags(flags *flag.FlagSet, args []string, stdout, stderr io.Writer) (int, bool) {
	if code, ok := parseArgs(flags, args, stdout, stderr); !ok {
		return code, false
	}

	if flags.NArg() > 0 {
		fmt.Fprintf(stderr, "handrail %s: unexpected argument %q\n", flags.Name(), flags.Arg(0))

		return 1, false
	}

	return 0, true
}

// reportProblems names each rule file problem on stderr.
func reportProblems(problems []rule.Problem, stderr io.Writer) {
	for _, p := range problems {
		fmt.Fprintf(stderr, "handrail: %s: %s\n", p.Path, p.Message)
	}
}

// loadRules reads the tiers that apply to the working directory and reports a
// state that is not enforce and an untrusted shared tier, which is skipped
// rather than silently missing.
func loadRules(stderr io.Writer) (*rule.Ruleset, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}

	ruleset := rule.Load(cwd)
	for _, notice := range []string{ruleset.StateNotice(), ruleset.TrustNotice()} {
		if notice != "" {
			fmt.Fprintln(stderr, notice)
		}
	}

	return ruleset, nil
}

// loadValidRules is test's authoring-time contract: every tier parses, or the
// command stops without acting. check and sync report problems and keep going,
// check because reporting them is its whole job, sync because its hooks
// depend on no rule. It is nil when the command stops, the reason already on
// stderr.
func loadValidRules(stderr io.Writer) *rule.Ruleset {
	ruleset, err := loadRules(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)

		return nil
	}

	if problems := ruleset.Invalid(); len(problems) > 0 {
		reportProblems(problems, stderr)

		return nil
	}

	return ruleset
}

func writeJSON(stdout, stderr io.Writer, v any) int {
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")

	err := enc.Encode(v)
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)

		return 1
	}

	return 0
}
