package main

import (
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/svyatov/handrail/internal/harness"
	"github.com/svyatov/handrail/internal/rule"
)

// cmdDoctor is the first command for "why is nothing firing". Everything it
// checks is already on this machine, so it answers with no network at all: the
// binary answering, each harness's hook entries, this repo's tiers, trust state
// and exclusion line, and whether the rules parse. It exits 1 when anything is
// wrong, so the answer is actionable without reading the report.
func cmdDoctor(args []string, _ io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)

	if !parseFlags(fs, args, stderr) {
		return 1
	}

	out := &report{w: stdout, problems: 0}

	bin, err := os.Executable()
	if err != nil {
		out.badf("cannot locate this binary: %v", err)
	} else {
		out.okf("handrail %s at %s", version, bin)
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)

		return 1
	}

	ruleset := rule.Load(cwd)

	for _, adapter := range harness.Adapters() {
		fmt.Fprintln(stdout)

		if !adapter.Installed() {
			out.notef("%s: not installed", adapter.Name)

			continue
		}

		out.okf("%s: config at %s", adapter.Name, adapter.ConfigPath())
		out.checkEntries(adapter, bin)
		// Degradation is reported at sync time and reprintable here: a rule
		// weakened months ago is exactly the kind that reads as not firing.
		for _, line := range adapter.Report(ruleset.Effective()) {
			out.notef("%s: %s", adapter.Name, line)
		}
	}

	fmt.Fprintln(stdout)
	out.okf("project root %s", ruleset.Root)
	out.checkTiers(ruleset)
	out.checkExclusion(ruleset)

	for _, p := range ruleset.Invalid() {
		out.badf("%s: %s", p.Path, p.Message)
	}

	out.okf("%s valid", countRules(len(ruleset.Rules)))

	if out.problems > 0 {
		return 1
	}

	return 0
}

// report is doctor's output, and the count of faults among it that the exit
// code answers for. A problem is something the user can fix; a note is
// something they can only know. Both belong in the report, and only one of
// them is a fault.
type report struct {
	w        io.Writer
	problems int
}

func (r *report) linef(status, format string, a ...any) {
	fmt.Fprintf(r.w, "%-8s %s\n", status, fmt.Sprintf(format, a...))
}

func (r *report) okf(format string, a ...any)   { r.linef("ok", format, a...) }
func (r *report) notef(format string, a ...any) { r.linef("note", format, a...) }

func (r *report) badf(format string, a ...any) {
	r.problems++
	r.linef("problem", format, a...)
}

// checkEntries answers the question a broken install turns into: is there an
// entry for every event, and does it invoke a binary that is here, runnable,
// and this one? A silent harness usually has one of those four wrong.
func (r *report) checkEntries(adapter harness.Adapter, bin string) {
	entries, err := adapter.Entries()
	if err != nil {
		r.badf("%s: %v", adapter.Name, err)

		return
	}

	current := 0

	for _, entry := range entries {
		switch {
		case entry.Binary == "":
			r.badf("%s: no hook entry for %s; run handrail sync", adapter.Name, entry.Event)
		case !runnable(entry.Binary):
			// An install that loses the exec bit leaves every entry in place and
			// every rule unenforced, which is the failure that looks like none.
			r.badf("%s: the %s entry names %s, which is not a runnable file; run handrail sync",
				adapter.Name, entry.Event, entry.Binary)
		case entry.Binary != bin:
			r.badf("%s: the %s entry names %s, and this binary is %s; run handrail sync",
				adapter.Name, entry.Event, entry.Binary, bin)
		default:
			current++
		}
	}

	if current == len(entries) {
		r.okf("%s: %d hook entries current", adapter.Name, current)
	}
}

// checkTiers reports what tier discovery found for this working directory, with
// the directory each tier was read from: a rule in the wrong place and a repo
// root that is not the one expected look identical from the outside.
func (r *report) checkTiers(rs *rule.Ruleset) {
	for _, tier := range rs.Tiers {
		trusted := ""

		switch {
		case tier.Dir == "":
			r.badf("%s: no config directory: set HOME or XDG_CONFIG_HOME", tier.Name)

			continue
		case tier.Skipped:
			r.badf("%s: %s holds rules this machine has not trusted; run handrail trust", tier.Name, tier.Dir)

			continue
		case tier.Name == rule.TierProjectShared && tier.Trusted:
			trusted = ", trusted"
		}

		r.okf("%s: %s in %s%s", tier.Name, countRules(tier.Count), tier.Dir, trusted)
	}
}

// checkExclusion reports the line sync writes to keep the Project-personal tier
// out of version control. Outside a working tree there is nothing to exclude,
// which is not the same as an exclusion that went missing.
func (r *report) checkExclusion(rs *rule.Ruleset) {
	switch excluded, path, err := rule.LocalExcluded(rs.Root); {
	case err != nil:
		r.badf("cannot read the exclude file of %s: %v", rs.Root, err)
	case path == "":
		r.okf("%s is not a git working tree, so nothing needs excluding", rs.Root)
	case excluded:
		r.okf(".handrail/local/ is excluded in .git/info/exclude")
	default:
		r.badf(".handrail/local/ is not excluded in .git/info/exclude; run handrail sync")
	}
}

// runnable reports whether a hook entry's binary is a file this machine can
// execute. A directory or a lost exec bit is a hook entry that fires nothing.
func runnable(path string) bool {
	fi, err := os.Stat(path)

	return err == nil && fi.Mode().IsRegular() && fi.Mode().Perm()&0o111 != 0
}

func countRules(n int) string {
	if n == 1 {
		return "1 rule"
	}

	return fmt.Sprintf("%d rules", n)
}
