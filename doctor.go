package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"slices"

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
		out.checkBypass(adapter, ruleset.Root, cwd)
		// Degradation is reported at sync time and reprintable here: a rule
		// weakened months ago is exactly the kind that reads as not firing.
		for _, line := range adapter.Report(ruleset.Effective()) {
			out.notef("%s: %s", adapter.Name, line)
		}
	}

	fmt.Fprintln(stdout)
	out.checkProject(ruleset)

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

// checkProject reports what this repo's rules depend on: where they are read
// from, what is granted and set for it, and whether they parse and pass their
// Examples.
func (r *report) checkProject(ruleset *rule.Ruleset) {
	r.okf("project root %s", ruleset.Root)
	r.checkTiers(ruleset)

	// A demoted tier is still read, only gated as shared, so it is a fact to
	// know rather than a fault.
	if ruleset.Demoted != "" {
		r.notef(".handrail/local/ is read as Project-shared: %s", ruleset.Demoted)
	} else {
		r.okf(".handrail/local/ is not supplied by the repository")
	}

	r.checkExclusion(ruleset)
	r.checkLog(ruleset.Root)

	// A state that is not enforce is the user's to set, and loud everywhere.
	state := describeState(ruleset.State, ruleset.StateScope, ruleset.Root)
	if ruleset.State == rule.StateEnforce {
		r.okf("enforcement state: %s", state)
	} else {
		r.notef("enforcement state: %s", state)
	}

	for _, p := range ruleset.Invalid() {
		r.badf("%s: %s", p.Path, p.Message)
	}

	trial := 0

	for _, entry := range ruleset.Effective() {
		if entry.Trial {
			trial++
		}
	}

	r.okf("%s valid, %d on trial", countRules(len(ruleset.Rules)), trial)
	r.checkExamples(slices.Concat(ruleset.Rules, ruleset.Untrusted))
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
		case entry.Narrowed:
			r.badf("%s: the %s entry is narrowed by its if or its group's matcher, so it misses calls; run handrail sync",
				adapter.Name, entry.Event)
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

// checkBypass reports a setting that keeps every current entry from running,
// which no entry check can see.
func (r *report) checkBypass(adapter harness.Adapter, root, cwd string) {
	bypass, err := adapter.Bypass(root, cwd)
	if err != nil {
		r.badf("%s: %v", adapter.Name, err)

		return
	}

	for _, reason := range bypass.Disabled {
		r.badf("%s: %s", adapter.Name, reason)
	}
	// handrail cannot stop another hook approving its ask, so this is a fact
	// to know rather than a fault to fix.
	for _, path := range bypass.Approvers {
		r.notef("%s: a PermissionRequest hook in %s can answer a handrail ask without the human", adapter.Name, path)
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
		case tier.Name == rule.TierProjectShared:
			trusted = ", not trusted"
		}

		r.okf("%s: %s in %s%s", tier.Name, countRules(tier.Count), tier.Dir, trusted)
	}
}

// checkExclusion reports the line sync writes to keep the Project-personal tier
// out of version control. Outside a working tree there is nothing to exclude,
// which is not the same as an exclusion that went missing.
func (r *report) checkExclusion(rs *rule.Ruleset) {
	switch file, err := rule.LocalExcluded(rs.Root); {
	case err != nil:
		r.badf("cannot read the exclude file of %s: %v", rs.Root, err)
	case file.Path == "":
		r.okf("%s is not a git working tree, so nothing needs excluding", rs.Root)
	case file.Excluded:
		r.okf(".handrail/local/ is excluded in .git/info/exclude")
	default:
		r.badf(".handrail/local/ is not excluded in .git/info/exclude; run handrail sync")
	}
}

// checkExamples runs every rule file's Examples, as check does, and names each
// one that fails.
func (r *report) checkExamples(rules []*rule.Rule) {
	failures := exampleFailures(rules)
	for _, f := range failures {
		r.badf("%s", f)
	}

	if len(failures) == 0 {
		total := 0
		for _, entry := range rules {
			total += len(entry.Examples)
		}

		r.okf("%d Examples pass", total)
	}
}

// checkLog reports root's Decision log grant, with the file the log writes and
// its size. A log that is off is the user's choice, so it is a note.
func (r *report) checkLog(root string) {
	file := rule.LogPaths().Current
	if file == "" {
		r.notef("no Decision log: set HOME or XDG_STATE_HOME")

		return
	}

	size := "not written yet"

	info, err := os.Stat(file)
	if err == nil {
		size = fmt.Sprintf("%d bytes", info.Size())
	}

	if rule.Logging(root) {
		r.okf("the Decision log is on for this project: %s, %s", file, size)
	} else {
		r.notef("the Decision log is off for this project, so only trial matches are recorded: %s, %s", file, size)
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
