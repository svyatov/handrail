// Command handrail enforces declarative guardrails across agentic coding harnesses.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"text/tabwriter"

	"github.com/svyatov/handrail/internal/harness"
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
  advise    Recommend native harness entries for the rules that translate
  import    Convert upstream hookify rules into Project-personal rules
  doctor    Diagnose this machine's install, offline
  version   Print version, commit, and build date
`

const importUsage = `Usage: handrail import hookify [path]

Converts upstream hookify's .claude/hookify.*.local.md rules. Anything the rule
format cannot express is skipped and reported, never written.
`

const testUsage = `Usage: handrail test <event> [--kind kind] [--field key=value]... [--stdin] [--json]
`

const hookUsage = `Usage: handrail hook <harness> <event>

Reads the harness's payload on stdin. Sync installs this; humans want test.
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
	case "advise":
		return cmdAdvise(args[1:], stdout, stderr)
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

func cmdVersion(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	fmt.Fprintf(stdout, "handrail %s\ncommit: %s\ndate: %s\n", version, commit, date)
	return 0
}

// cmdDoctor is the first command for "why is nothing firing". Everything it
// checks is already on this machine, so it answers with no network at all: the
// binary answering, each harness's hook entries, this repo's tiers, trust state
// and exclusion line, and whether the rules parse. It exits 1 when anything is
// wrong, so the answer is actionable without reading the report.
func cmdDoctor(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "handrail doctor: unexpected argument %q\n", fs.Arg(0))
		return 1
	}

	r := &report{w: stdout}

	bin, err := os.Executable()
	if err != nil {
		r.bad("cannot locate this binary: %v", err)
	} else {
		r.ok("handrail %s at %s", version, bin)
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)
		return 1
	}
	rules, ruleProblems, d := rule.LoadTiers(cwd)

	for _, a := range harness.Adapters() {
		fmt.Fprintln(stdout)
		if !a.Installed() {
			r.note("%s: not installed", a.Name)
			continue
		}
		r.ok("%s: config at %s", a.Name, a.ConfigPath())
		r.checkEntries(a, bin)
		// Degradation is reported at sync time and reprintable here: a rule
		// weakened months ago is exactly the kind that reads as not firing.
		for _, deg := range a.Degradations(rules) {
			r.note("%s: %s", a.Name, deg)
		}
		for _, q := range a.Quirks {
			r.note("%s: %s", a.Name, q)
		}
	}

	fmt.Fprintln(stdout)
	r.ok("project root %s", d.Root)
	r.checkTiers(rules, d)
	r.checkExclusion(d.Root)

	for _, p := range ruleProblems {
		r.bad("%s: %s", p.Path, p.Message)
	}
	r.ok("%s valid", countRules(len(rules)))

	if r.problems > 0 {
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

func (r *report) line(status, format string, a ...any) {
	fmt.Fprintf(r.w, "%-8s %s\n", status, fmt.Sprintf(format, a...))
}

func (r *report) ok(format string, a ...any)   { r.line("ok", format, a...) }
func (r *report) note(format string, a ...any) { r.line("note", format, a...) }

func (r *report) bad(format string, a ...any) {
	r.problems++
	r.line("problem", format, a...)
}

// checkEntries answers the question a broken install turns into: is there an
// entry for every event, and does it invoke a binary that is here, runnable,
// and this one? A silent harness usually has one of those four wrong.
func (r *report) checkEntries(a harness.Adapter, bin string) {
	entries, err := a.Entries()
	if err != nil {
		r.bad("%s: %v", a.Name, err)
		return
	}
	current := 0
	for _, e := range entries {
		switch {
		case e.Binary == "":
			r.bad("%s: no hook entry for %s; run handrail sync", a.Name, e.Event)
		case !runnable(e.Binary):
			// An install that loses the exec bit leaves every entry in place and
			// every rule unenforced, which is the failure that looks like none.
			r.bad("%s: the %s entry names %s, which is not a runnable file; run handrail sync",
				a.Name, e.Event, e.Binary)
		case e.Binary != bin:
			r.bad("%s: the %s entry names %s, and this binary is %s; run handrail sync",
				a.Name, e.Event, e.Binary, bin)
		default:
			current++
		}
	}
	if current == len(entries) {
		r.ok("%s: %d hook entries current", a.Name, current)
	}
}

// checkTiers reports what tier discovery found for this working directory, with
// the directory each tier was read from: a rule in the wrong place and a repo
// root that is not the one expected look identical from the outside.
func (r *report) checkTiers(rules []*rule.Rule, d rule.Discovery) {
	counts := map[string]int{}
	for _, rl := range rules {
		counts[rl.Tier]++
	}
	if global := rule.ConfigDir(); global == "" {
		r.bad("%s: no config directory: set HOME or XDG_CONFIG_HOME", rule.TierGlobal)
	} else {
		r.ok("%s: %s in %s", rule.TierGlobal, countRules(counts[rule.TierGlobal]), global)
	}

	shared := rule.SharedDir(d.Root)
	if d.Skipped {
		r.bad("%s: %s holds rules this machine has not trusted; run handrail trust",
			rule.TierProjectShared, shared)
	} else {
		trusted := ""
		if rule.IsTrusted(d.Root) {
			trusted = ", trusted"
		}
		r.ok("%s: %s in %s%s",
			rule.TierProjectShared, countRules(counts[rule.TierProjectShared]), shared, trusted)
	}

	r.ok("%s: %s in %s",
		rule.TierProjectPersonal, countRules(counts[rule.TierProjectPersonal]), rule.LocalDir(d.Root))
}

// checkExclusion reports the line sync writes to keep the Project-personal tier
// out of version control. Outside a working tree there is nothing to exclude,
// which is not the same as an exclusion that went missing.
func (r *report) checkExclusion(root string) {
	switch excluded, path, err := rule.LocalExcluded(root); {
	case err != nil:
		r.bad("cannot read the exclude file of %s: %v", root, err)
	case path == "":
		r.ok("%s is not a git working tree, so nothing needs excluding", root)
	case excluded:
		r.ok(".handrail/local/ is excluded in .git/info/exclude")
	default:
		r.bad(".handrail/local/ is not excluded in .git/info/exclude; run handrail sync")
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

type adviceOutput struct {
	Rule          string   `json:"rule"`
	Tier          string   `json:"tier"`
	Harness       string   `json:"harness"`
	Mechanism     string   `json:"mechanism"`
	Entry         string   `json:"entry"`
	Location      string   `json:"location"`
	ScopeWidening *string  `json:"scope_widening"`
	Caveats       []string `json:"caveats"`
}

// cmdAdvise is the Advisor: it recommends promoting a rule to a harness-native
// mechanism where the matcher translates exactly, and stops there. Nothing is
// written, and an accepted entry becomes the user's own config, which handrail
// never owns, updates, or garbage-collects (ADR 0005).
func cmdAdvise(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("advise", flag.ContinueOnError)
	fs.SetOutput(stderr)
	only := fs.String("harness", "", "advise only for this harness")
	asJSON := fs.Bool("json", false, "print the advice as JSON")

	// The rule name is positional and leads, as test's event does: the stdlib
	// flag package stops at the first non-flag argument.
	var name string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		name, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "handrail advise: unexpected argument %q\n", fs.Arg(0))
		return 1
	}
	if *only != "" {
		if _, ok := harness.Lookup(*only); !ok {
			fmt.Fprintf(stderr, "handrail advise: unknown harness %q; known: %s\n",
				*only, strings.Join(harness.Names(), ", "))
			return 1
		}
	}

	// An authoring-time surface, so it is strict like check and test.
	rules, code := loadValidRules(stderr)
	if code != 0 {
		return code
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)
		return 1
	}
	root := rule.RepoRoot(cwd)

	var targets []*rule.Rule
	for _, r := range rules {
		// Advice is about what is live: a shadowed or disabled rule enforces
		// nothing, so promoting it would install a deny nothing asked for.
		if !r.Enabled || r.ShadowedBy != "" || (name != "" && r.Name != name) {
			continue
		}
		targets = append(targets, r)
	}
	if name != "" && len(targets) == 0 {
		fmt.Fprintf(stderr, "handrail advise: no enabled, unshadowed rule named %q\n", name)
		return 1
	}

	out := []adviceOutput{}
	for _, r := range targets {
		widening := scopeWidening(r.Tier, root)
		for _, a := range harness.Adapters() {
			if *only != "" && a.Name != *only {
				continue
			}
			adv, ok := a.Advise(r)
			if !ok {
				continue
			}
			out = append(out, adviceOutput{
				Rule: r.Name, Tier: r.Tier, Harness: a.Name,
				Mechanism: adv.Mechanism, Entry: adv.Entry, Location: adv.Location,
				ScopeWidening: widening, Caveats: adv.Caveats,
			})
		}
	}

	if *asJSON {
		return writeJSON(stdout, stderr, out)
	}
	if len(out) == 0 {
		fmt.Fprintln(stdout, "handrail advise: no rule translates to a native entry")
		return 0
	}
	for _, adv := range out {
		fmt.Fprintf(stdout, "%s  %s  %s  %s\n", adv.Rule, adv.Tier, adv.Harness, adv.Mechanism)
		fmt.Fprintf(stdout, "  add to %s:\n", adv.Location)
		for line := range strings.SplitSeq(adv.Entry, "\n") {
			fmt.Fprintf(stdout, "    %s\n", line)
		}
		if adv.ScopeWidening != nil {
			fmt.Fprintf(stdout, "  scope: %s\n", *adv.ScopeWidening)
		}
		for _, c := range adv.Caveats {
			fmt.Fprintf(stdout, "  caveat: %s\n", c)
		}
		fmt.Fprintln(stdout)
	}
	return 0
}

// cmdImport is the one-shot converter from another tool's rules. It writes into
// the Project-personal tier, which is where somebody else's guardrails belong
// until their new owner has read them: nothing lands in a committed tier, and
// nothing that cannot be expressed lands at all.
func cmdImport(args []string, stdout, stderr io.Writer) int {
	// The format leads, so a flag in its place is a request for the usage rather
	// than the name of something to convert.
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprint(stderr, importUsage)
		return 1
	}
	if args[0] != "hookify" {
		fmt.Fprintf(stderr, "handrail import: unknown format %q; known: hookify\n", args[0])
		return 1
	}
	fs := flag.NewFlagSet("import", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args[1:]); err != nil {
		return 1
	}
	if fs.NArg() > 1 {
		fmt.Fprintf(stderr, "handrail import: unexpected argument %q\n", fs.Arg(1))
		return 1
	}
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)
		return 1
	}
	root := rule.RepoRoot(cwd)

	// Upstream reads .claude/ from the repo root, so that is where the import
	// looks unless told otherwise.
	src := filepath.Join(root, ".claude")
	if fs.NArg() == 1 {
		if src = fs.Arg(0); !filepath.IsAbs(src) {
			src = filepath.Join(cwd, src)
		}
	}
	results, err := rule.ImportHookify(src, filepath.Join(root, ".handrail", "local"))
	if err != nil {
		fmt.Fprintf(stderr, "handrail import: %v\n", err)
		return 1
	}

	imported, skipped := 0, 0
	for _, r := range results {
		if r.Reason != "" {
			skipped++
			fmt.Fprintf(stdout, "skipped  %s: %s\n", relTo(root, r.Source), r.Reason)
			continue
		}
		imported++
		fmt.Fprintf(stdout, "imported %s -> %s\n", relTo(root, r.Source), relTo(root, r.Target))
	}
	fmt.Fprintf(stdout, "%d imported, %d skipped\n", imported, skipped)
	return 0
}

// relTo shortens a path for the report, and leaves it alone when it lies
// outside the project.
func relTo(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}
	return rel
}

// scopeWidening states what a promotion changes about a rule's reach. Native
// entries are user-level, so promoting a project-tier rule spreads it to every
// repo on the machine; a Global rule was already there.
func scopeWidening(tier, root string) *string {
	if tier == rule.TierGlobal {
		return nil
	}
	s := fmt.Sprintf("this rule applies only in %s, and the entry applies in every repo on this machine", root)
	return &s
}

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
	rules, code := loadValidRules(stderr)
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
		for _, d := range a.Degradations(rules) {
			fmt.Fprintf(stdout, "%s: %s\n", a.Name, d)
		}
		for _, q := range a.Quirks {
			fmt.Fprintf(stdout, "%s: %s\n", a.Name, q)
		}
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)
		return 1
	}
	added, err := rule.ExcludeLocal(rule.RepoRoot(cwd))
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)
		return 1
	}
	if added {
		fmt.Fprintln(stdout, "handrail: added .handrail/local/ to .git/info/exclude")
	}

	fmt.Fprintln(stdout)
	if err := printRuleset(stdout, rules); err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)
		return 1
	}
	if failed {
		return 1
	}
	return 0
}

// cmdHook is the entrypoint sync installs into each harness. Everything it can
// get wrong past argument parsing fails open and says so: a guardrail manager
// that wedges the harness is worse than the harness without it.
func cmdHook(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("hook", flag.ContinueOnError)
	fs.SetOutput(stderr)
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() != 2 {
		fmt.Fprint(stderr, hookUsage)
		return 1
	}
	name, event := fs.Arg(0), fs.Arg(1)
	// The hook entry is handrail's own writing, so a wrong harness or event is a
	// bug in the installed config rather than something to soldier through.
	a, ok := harness.Lookup(name)
	if !ok {
		fmt.Fprintf(stderr, "handrail hook: unknown harness %q\n", name)
		return 1
	}
	if !rule.IsEvent(event) {
		fmt.Fprintf(stderr, "handrail hook: unknown event %q\n", event)
		return 1
	}

	data, err := io.ReadAll(stdin)
	if err == nil {
		var payload rule.Payload
		var cwd string
		if payload, cwd, err = a.Normalize(event, data); err == nil {
			message, block := evaluate(payload, cwd)
			return a.Deliver(event, message, block, stdout, stderr)
		}
	}
	return a.Deliver(event,
		fmt.Sprintf("handrail: could not read the %s payload, so no rule was evaluated: %v", event, err),
		false, stdout, stderr)
}

// evaluate merges the tiers and collects everything the agent should hear: the
// matched messages, then whatever handrail had to skip to get there.
func evaluate(payload rule.Payload, cwd string) (message string, block bool) {
	// The payload names the directory the event happened in; the process's own
	// is the fallback for a harness that leaves it out.
	if !filepath.IsAbs(cwd) {
		var err error
		if cwd, err = os.Getwd(); err != nil {
			return fmt.Sprintf("handrail: no working directory, so no rule was evaluated: %v", err), false
		}
	}
	rules, problems, d := rule.LoadTiers(cwd)

	var sections []string
	for _, r := range rule.Effective(rules, payload) {
		if r.Action == "block" {
			block = true
		}
		sections = append(sections, fmt.Sprintf("handrail %s: %s (%s)\n%s", r.Action, r.Name, r.Tier, r.Message))
	}
	// Loud fail-open: a rule that cannot be parsed is skipped, and the skipping
	// is named. A guardrail that guards nothing must never look like one that did.
	for _, p := range problems {
		sections = append(sections, fmt.Sprintf("handrail: skipped the broken rule %s: %s", p.Path, p.Message))
	}
	if d.Skipped {
		sections = append(sections, trustNotice(d.Root))
	}
	return strings.Join(sections, "\n\n"), block
}

// loadRules reads the tiers that apply to the working directory and reports an
// untrusted shared tier, which is skipped rather than silently missing.
func loadRules(stderr io.Writer) ([]*rule.Rule, []rule.Problem, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return nil, nil, err
	}
	rules, problems, d := rule.LoadTiers(cwd)
	if d.Skipped {
		fmt.Fprintln(stderr, trustNotice(d.Root))
	}
	return rules, problems, nil
}

// loadValidRules is the authoring-time contract sync and test share: every tier
// parses, or the command stops without acting. Only check reports problems and
// keeps going, because reporting them is the whole of its job.
func loadValidRules(stderr io.Writer) ([]*rule.Rule, int) {
	rules, problems, err := loadRules(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)
		return nil, 1
	}
	if len(problems) > 0 {
		for _, p := range problems {
			fmt.Fprintf(stderr, "handrail: %s: %s\n", p.Path, p.Message)
		}
		return nil, 1
	}
	return rules, 0
}

func trustNotice(root string) string {
	return fmt.Sprintf("handrail: skipping the untrusted Project-shared rules in %s; run handrail trust to enable them", root)
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

type checkRule struct {
	Rule       string  `json:"rule"`
	Tier       string  `json:"tier"`
	Event      string  `json:"event"`
	Kind       string  `json:"kind"`
	Action     string  `json:"action"`
	Enabled    bool    `json:"enabled"`
	ShadowedBy *string `json:"shadowed_by"`
	Path       string  `json:"path"`
}

type checkError struct {
	Path    string `json:"path"`
	Message string `json:"message"`
}

type checkOutput struct {
	Rules  []checkRule  `json:"rules"`
	Errors []checkError `json:"errors"`
}

func cmdCheck(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("check", flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print the effective ruleset as JSON")
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "handrail check: unexpected argument %q\n", fs.Arg(0))
		return 1
	}

	rules, problems, err := loadRules(stderr)
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)
		return 1
	}

	if *asJSON {
		out := checkOutput{
			Rules:  make([]checkRule, 0, len(rules)),
			Errors: make([]checkError, 0, len(problems)),
		}
		for _, r := range rules {
			var shadowedBy *string
			if r.ShadowedBy != "" {
				shadowedBy = &r.ShadowedBy
			}
			out.Rules = append(out.Rules, checkRule{
				Rule:       r.Name,
				Tier:       r.Tier,
				Event:      r.Event,
				Kind:       r.Kind,
				Action:     r.Action,
				Enabled:    r.Enabled,
				ShadowedBy: shadowedBy,
				Path:       r.Path,
			})
		}
		for _, p := range problems {
			out.Errors = append(out.Errors, checkError{Path: p.Path, Message: p.Message})
		}
		if code := writeJSON(stdout, stderr, out); code != 0 {
			return code
		}
	} else {
		if err := printRuleset(stdout, rules); err != nil {
			fmt.Fprintf(stderr, "handrail: %v\n", err)
			return 1
		}
		for _, p := range problems {
			fmt.Fprintf(stderr, "handrail: %s: %s\n", p.Path, p.Message)
		}
	}

	if len(problems) > 0 {
		return 1
	}
	return 0
}

// fieldSet collects repeated --field key=value flags into the synthetic
// payload's canonical fields.
type fieldSet map[string]string

func (f fieldSet) String() string { return "" }

func (f fieldSet) Set(s string) error {
	k, v, ok := strings.Cut(s, "=")
	if !ok {
		return fmt.Errorf("expected key=value, got %q", s)
	}
	if !rule.IsField(k) {
		return fmt.Errorf("unknown canonical field %q", k)
	}
	f[k] = v
	return nil
}

type testMatch struct {
	Rule    string `json:"rule"`
	Tier    string `json:"tier"`
	Action  string `json:"action"`
	Message string `json:"message"`
}

type testOutput struct {
	Outcome string      `json:"outcome"`
	Matched []testMatch `json:"matched"`
}

func cmdTest(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	fs.SetOutput(stderr)
	kind := fs.String("kind", "", "tool kind of the synthetic payload")
	fields := fieldSet{}
	fs.Var(fields, "field", "canonical payload field as key=value, repeatable")
	fromStdin := fs.Bool("stdin", false, "read a canonical payload JSON from stdin")
	asJSON := fs.Bool("json", false, "print the result as JSON")

	// The event is positional and leads, so pull it before flag parsing: the
	// stdlib flag package stops at the first non-flag argument.
	var event string
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		event, args = args[0], args[1:]
	}
	if err := fs.Parse(args); err != nil {
		return 1
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "handrail test: unexpected argument %q\n", fs.Arg(0))
		return 1
	}
	if event == "" {
		fmt.Fprintf(stderr, "handrail test: missing event\n\n%s", testUsage)
		return 1
	}
	if !rule.IsEvent(event) {
		fmt.Fprintf(stderr, "handrail test: unknown event %q\n", event)
		return 1
	}
	if *kind != "" && !rule.IsKind(*kind) {
		fmt.Fprintf(stderr, "handrail test: unknown kind %q\n", *kind)
		return 1
	}

	payload := rule.Payload{Event: event, Fields: map[string]string{}}
	if *fromStdin {
		var raw map[string]any
		if err := json.NewDecoder(stdin).Decode(&raw); err != nil {
			fmt.Fprintf(stderr, "handrail test: reading payload: %v\n", err)
			return 1
		}
		if k, ok := raw["tool_kind"].(string); ok {
			if !rule.IsKind(k) {
				fmt.Fprintf(stderr, "handrail test: unknown kind %q\n", k)
				return 1
			}
			payload.Kind = k
		}
		// Anything the matcher cannot address, raw.* included, is ignored here.
		for k, v := range raw {
			if s, ok := v.(string); ok && rule.IsField(k) {
				payload.Fields[k] = s
			}
		}
	}
	// Flags win over the payload, so one field can be varied against a capture.
	maps.Copy(payload.Fields, fields)
	if *kind != "" {
		payload.Kind = *kind
	}

	// test is an authoring-time surface, so it is strict like check: the loud
	// fail-open belongs to the event-time hook path, not here.
	rules, code := loadValidRules(stderr)
	if code != 0 {
		return code
	}

	out := testOutput{Outcome: "allow", Matched: []testMatch{}}
	for _, r := range rule.Effective(rules, payload) {
		out.Matched = append(out.Matched, testMatch{
			Rule: r.Name, Tier: r.Tier, Action: r.Action, Message: r.Message,
		})
		if r.Action == "block" {
			out.Outcome = "block"
		} else if out.Outcome == "allow" {
			out.Outcome = "warn"
		}
	}

	if *asJSON {
		if code := writeJSON(stdout, stderr, out); code != 0 {
			return code
		}
	} else {
		for _, m := range out.Matched {
			fmt.Fprintf(stdout, "%s  %s  %s\n", m.Action, m.Rule, m.Tier)
			for line := range strings.SplitSeq(m.Message, "\n") {
				if line == "" {
					fmt.Fprintln(stdout)
					continue
				}
				fmt.Fprintf(stdout, "  %s\n", line)
			}
			fmt.Fprintln(stdout)
		}
		fmt.Fprintf(stdout, "outcome: %s\n", out.Outcome)
	}

	if out.Outcome == "block" {
		return 2
	}
	return 0
}

// printRuleset renders the effective ruleset annotated with tier, shadowing,
// and disabling: what check reports, and what sync repeats once it has written.
func printRuleset(w io.Writer, rules []*rule.Rule) error {
	if len(rules) == 0 {
		return nil
	}
	// Rules arrive in tier order, so the last one under a name is the effective
	// one: the tier that shadows every earlier namesake.
	effective := make(map[string]string, len(rules))
	for _, r := range rules {
		effective[r.Name] = r.Tier
	}
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "TIER\tRULE\tEVENT\tKIND\tACTION\tSTATUS")
	for _, r := range rules {
		status := "enabled"
		switch {
		case r.ShadowedBy != "":
			status = "shadowed by " + effective[r.Name]
		case !r.Enabled:
			status = "disabled"
		}
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			r.Tier, r.Name, orDefault(r.Event, "-"), orDefault(r.Kind, "*"), r.Action, status)
	}
	return tw.Flush()
}

func orDefault(s, fallback string) string {
	if s == "" {
		return fallback
	}
	return s
}
