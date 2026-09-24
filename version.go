package main

import (
	"flag"
	"fmt"
	"io"
)

func cmdVersion(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	fmt.Fprintf(stdout, "handrail %s\ncommit: %s\ndate: %s\n", version, commit, date)
	return 0
}
