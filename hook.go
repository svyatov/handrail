package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/svyatov/handrail/internal/harness"
	"github.com/svyatov/handrail/internal/rule"
)

const hookUsage = `Usage: handrail hook <harness> <event>

Reads the harness's payload on stdin. Sync installs this; humans want test.
`

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

	// handrail's own failures never decide the event: each is declared as
	// unreadable, where a rule may fail closed on it, and named on both channels.
	var failures []string
	var payloads []rule.Payload
	var cwd string
	// Two different faults, so two different messages: stdin never arrived, or it
	// arrived and was not the payload. Reporting the second as the first sends the
	// reader to look at the pipe when the harness's JSON is what to fix.
	data, err := io.ReadAll(stdin)
	if err != nil {
		failures = append(failures, fmt.Sprintf("handrail: could not read the %s payload: %v", event, err))
	} else if payloads, cwd, err = a.Normalize(event, data); err != nil {
		failures = append(failures, fmt.Sprintf("handrail: could not parse the %s payload: %v", event, err))
	}
	// The payload names the directory the event happened in; the process's own is
	// the fallback for a harness that leaves it out. That is process state rather
	// than payload, so it is answered here and not in Normalize. A directory
	// that is not there names no project, whoever named it.
	if !filepath.IsAbs(cwd) {
		cwd, _ = os.Getwd()
	}
	if _, err := os.Stat(cwd); err != nil {
		cwd = ""
		failures = append(failures, fmt.Sprintf("handrail: no working directory, so no project rule was evaluated: %v", err))
	}
	// A payload handrail could not take whole, or one with no directory to
	// place it in, is one kind-less payload, which only a rule naming no kind
	// reaches.
	if failures != nil {
		p := rule.Payload{Event: event}
		p.SetField("unreadable", "payload")
		payloads = []rule.Payload{p}
	}
	rs := rule.Load(cwd)
	matched, outcome := rs.Evaluate(payloads)
	// Loud fail-open: a rule that cannot be parsed is skipped, and the skipping
	// is named. A guardrail that guards nothing must never look like one that
	// did. A tier trust skipped loses nothing, so its broken files stay quiet.
	for _, t := range rs.Tiers {
		if t.Name == rule.TierGlobal && t.Dir == "" {
			failures = append(failures, "handrail: no Global tier to read: set HOME or XDG_CONFIG_HOME")
		}
	}
	for _, p := range rs.Problems {
		if !p.Untrusted {
			failures = append(failures, fmt.Sprintf("handrail: skipped the broken rule %s: %s", p.Path, p.Message))
		}
	}
	human := strings.Join(failures, "\n")
	return a.Deliver(event, agentMessage(rs, matched, failures), human, outcome, stdout, stderr)
}

// listedFiles is how many matched files a rule's message names before it
// counts the rest (docs/spec.md section 2, Several edits in one call).
const listedFiles = 10

// agentMessage is the wire format the hook path delivers: everything the agent
// should hear, which is the matched messages, then handrail's own failures,
// then the trust notice. It stays in the CLI because it is the hook command's own
// output format, with one caller and nothing to disagree with.
func agentMessage(rs *rule.Ruleset, matched []rule.Match, failures []string) string {
	var sections []string
	for _, m := range matched {
		s := fmt.Sprintf("handrail %s: %s (%s)\n%s", m.Action, m.Name, m.Tier, m.Message)
		if len(m.Files) > 0 {
			s += "\nMatched files:\n  " + strings.Join(m.Files[:min(len(m.Files), listedFiles)], "\n  ")
			if len(m.Files) > listedFiles {
				s += fmt.Sprintf("\n  and %d more", len(m.Files)-listedFiles)
			}
		}
		sections = append(sections, s)
	}
	sections = append(sections, failures...)
	if notice := rs.TrustNotice(); notice != "" {
		sections = append(sections, notice)
	}
	return strings.Join(sections, "\n\n")
}
