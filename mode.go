package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/svyatov/handrail/internal/harness"
	"github.com/svyatov/handrail/internal/rule"
)

// cmdMode reads or sets the Enforcement state, for this project or, with
// --global, machine-wide.
func cmdMode(args []string, _ io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("mode", flag.ContinueOnError)
	flags.SetOutput(stderr)
	global := flags.Bool("global", false, "the machine-wide state, which applies where no project sets one")

	name := leadingArg(args)
	if name != "" {
		args = args[1:]
	}

	if !parseFlags(flags, args, stderr) {
		return 1
	}

	root := ""

	if !*global {
		cwd, err := os.Getwd()
		if err != nil {
			fmt.Fprintf(stderr, "handrail: %v\n", err)

			return 1
		}

		root = rule.RepoRoot(cwd)
	}

	if name != "" && !setMode(root, name, stderr) {
		return 1
	}

	switch state, scope := rule.StateOf(root); scope {
	case rule.ScopeDefault:
		fmt.Fprintf(stdout, "%s (default)\n", state)
	case rule.ScopeMachine:
		fmt.Fprintf(stdout, "%s (machine-wide)\n", state)
	case rule.ScopeProject:
		fmt.Fprintf(stdout, "%s (this project: %s)\n", state, root)
	}

	return 0
}

// setMode records the state name for root, "" being machine-wide, and reports
// whether it did, the reason on stderr when it did not.
func setMode(root, name string, stderr io.Writer) bool {
	// Not a boundary: env -u gets past it. It stops an agent switching handrail
	// off in passing, and the SessionStart notice shows one that went around it.
	if session := harness.Session(); session != "" {
		fmt.Fprintf(stderr, "handrail mode: refusing to set the enforcement state inside a harness session "+
			"(%s is set); run it in your own terminal\n", session)

		return false
	}

	state, ok := rule.ParseState(name)
	if !ok {
		fmt.Fprintf(stderr, "handrail mode: unknown state %q; known: enforce, trial, off\n", name)

		return false
	}

	err := rule.SetState(root, state)
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)

		return false
	}

	return true
}

// modeAlias is mode with its state already written.
func modeAlias(state string) func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	return func(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
		return cmdMode(append([]string{state}, args...), stdin, stdout, stderr)
	}
}
