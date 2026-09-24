package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/svyatov/handrail/internal/rule"
)

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
	// The grant is keyed by the project root alone, so trust reads no rules:
	// rules this machine has not trusted yet are exactly what it must not need
	// to parse before granting them.
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
