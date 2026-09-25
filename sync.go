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
func cmdSync(args []string, _ io.Reader, stdout, stderr io.Writer) int {
	only, ok := parseSyncFlags(args, stderr)
	if !ok {
		return 1
	}

	// The hook entries depend on no rule, so an invalid one is reported and
	// sync still writes: one bad file must not leave the machine with no hooks.
	ruleset, err := loadRules(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)

		return 1
	}

	problems := ruleset.Invalid()
	reportProblems(problems, stderr)

	targets := syncTargets(only, stderr)
	if targets == nil {
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

	failed := installHooks(targets, bin, ruleset.Effective(), stdout, stderr)

	err = excludeLocal(ruleset.Root, stdout)
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)

		return 1
	}

	fmt.Fprintln(stdout)

	err = printRuleset(stdout, ruleset.Rules)
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)

		return 1
	}

	reportTierMoves(ruleset, stderr)
	// A failing Example changes nothing sync writes, so it is reported after.
	if reportExamples(slices.Concat(ruleset.Rules, ruleset.Untrusted), stderr) || failed || len(problems) > 0 {
		return 1
	}

	return 0
}

// parseSyncFlags reads sync's arguments into the one harness it is limited
// to, "" for all of them, and reports whether sync may go on.
func parseSyncFlags(args []string, stderr io.Writer) (string, bool) {
	fs := flag.NewFlagSet("sync", flag.ContinueOnError)
	fs.SetOutput(stderr)
	only := fs.String("harness", "", "sync only this harness")

	if !parseFlags(fs, args, stderr) {
		return "", false
	}

	if *only != "" {
		if _, ok := harness.Lookup(*only); !ok {
			fmt.Fprintf(stderr, "handrail sync: unknown harness %q; known: %s\n",
				*only, strings.Join(harness.Names(), ", "))

			return "", false
		}
	}

	return *only, true
}

// syncTargets is every installed harness sync writes to, or only the one
// named. It names the missing harness and returns nil when there is none.
func syncTargets(only string, stderr io.Writer) []harness.Adapter {
	var targets []harness.Adapter

	for _, a := range harness.Adapters() {
		if (only == "" || a.Name == only) && a.Installed() {
			targets = append(targets, a)
		}
	}

	if len(targets) == 0 {
		found := "no harness found; install Claude Code or Codex CLI and run it once"
		if only != "" {
			found = only + " not found; install it and run it once"
		}

		fmt.Fprintf(stderr, "handrail sync: %s\n", found)
	}

	return targets
}

// installHooks writes the hook entries naming bin into each target, then
// reports what the harness does with the effective ruleset, and whether any
// target failed.
func installHooks(targets []harness.Adapter, bin string, effective []*rule.Rule, stdout, stderr io.Writer) bool {
	// One harness's broken config must not leave the others unsynced, so the
	// loop reports the failure, names the harness, and carries on.
	failed := false

	for _, adapter := range targets {
		entries, changed, err := adapter.Install(bin)
		if err != nil {
			fmt.Fprintf(stderr, "handrail: %s: %v\n", adapter.Name, err)

			failed = true

			continue
		}

		if changed {
			fmt.Fprintf(stdout, "%s: wrote %d hook entries to %s\n", adapter.Name, entries, adapter.ConfigPath())
		} else {
			fmt.Fprintf(stdout, "%s: %d hook entries already current in %s\n", adapter.Name, entries, adapter.ConfigPath())
		}

		for _, line := range adapter.Report(effective) {
			fmt.Fprintf(stdout, "%s: %s\n", adapter.Name, line)
		}
	}

	return failed
}

// excludeLocal keeps the Project-personal tier out of git, and says so when
// it adds the line.
func excludeLocal(root string, stdout io.Writer) error {
	added, err := rule.ExcludeLocal(root)
	if err != nil {
		return err
	}

	if added {
		fmt.Fprintln(stdout, "handrail: added .handrail/local/ to .git/info/exclude")
	}

	return nil
}
