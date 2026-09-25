package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/svyatov/handrail/internal/harness"
	"github.com/svyatov/handrail/internal/rule"
)

// cmdSync installs handrail into the machine's harnesses. It is per-machine,
// not per-project: the hook entries are user-level, so every repo holding rules
// is enforced once this has run, and no harness config is written into any repo.
func cmdSync(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	only := fs.String("harness", "", "sync only this harness")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "handrail sync: unexpected argument %q\n", fs.Arg(0))
		return 1
	}
	if *only != "" {
		if _, ok := harness.Lookup(*only); !ok {
			fmt.Fprintf(stderr, "handrail sync: unknown harness %q; known: %s\n",
				*only, strings.Join(harness.Names(), ", "))
			return 1
		}
	}

	// Validation first: a machine synced against half a ruleset is worse than an
	// unsynced one, so nothing reaches disk until every tier parses.
	rs, code := loadValidRules(stderr)
	if code != 0 {
		return code
	}

	var targets []harness.Adapter
	for _, a := range harness.Adapters() {
		if (*only == "" || a.Name == *only) && a.Installed() {
			targets = append(targets, a)
		}
	}
	if len(targets) == 0 {
		found := "no harness found; install Claude Code or Codex CLI and run it once"
		if *only != "" {
			found = *only + " not found; install it and run it once"
		}
		fmt.Fprintf(stderr, "handrail sync: %s\n", found)
		return 1
	}
	// The hook entry names the binary absolutely, so a harness with its own PATH
	// still finds the one that wrote the entry. Whatever symlinks lie under that
	// path stay unresolved on purpose: a package manager's stable shim is the
	// path that survives the next upgrade, and the versioned file behind it is not.
	bin, err := os.Executable()
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)
		return 1
	}

	// One harness's broken config must not leave the others unsynced, so the
	// loop reports the failure, names the harness, and carries on.
	failed := false
	for _, a := range targets {
		entries, changed, err := a.Install(bin)
		if err != nil {
			fmt.Fprintf(stderr, "handrail: %s: %v\n", a.Name, err)
			failed = true
			continue
		}
		if changed {
			fmt.Fprintf(stdout, "%s: wrote %d hook entries to %s\n", a.Name, entries, a.ConfigPath())
		} else {
			fmt.Fprintf(stdout, "%s: %d hook entries already current in %s\n", a.Name, entries, a.ConfigPath())
		}
		for _, d := range a.Degradations(rs.Effective()) {
			fmt.Fprintf(stdout, "%s: %s\n", a.Name, d)
		}
		for _, q := range a.Quirks {
			fmt.Fprintf(stdout, "%s: %s\n", a.Name, q)
		}
	}

	added, err := rule.ExcludeLocal(rs.Root)
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)
		return 1
	}
	if added {
		fmt.Fprintln(stdout, "handrail: added .handrail/local/ to .git/info/exclude")
	}

	fmt.Fprintln(stdout)
	if err := printRuleset(stdout, rs.Rules); err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)
		return 1
	}
	reportTierMoves(rs, stderr)
	// A failing Example changes nothing sync writes, so it is reported after.
	if reportExamples(slices.Concat(rs.Rules, rs.Untrusted), stderr) || failed {
		return 1
	}
	return 0
}
