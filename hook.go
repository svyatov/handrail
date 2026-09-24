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

	// An engine that cannot answer lets the event through rather than wedge the
	// harness, so every failure below delivers a message and never blocks.
	failOpen := func(format string, args ...any) int {
		return a.Deliver(event, fmt.Sprintf(format, args...), rule.Allow, stdout, stderr)
	}
	// Two different faults, so two different messages: stdin never arrived, or it
	// arrived and was not the payload. Reporting the second as the first sends the
	// reader to look at the pipe when the harness's JSON is what to fix.
	data, err := io.ReadAll(stdin)
	if err != nil {
		return failOpen("handrail: could not read the %s payload, so no rule was evaluated: %v", event, err)
	}
	payload, cwd, err := a.Normalize(event, data)
	if err != nil {
		return failOpen("handrail: could not parse the %s payload, so no rule was evaluated: %v", event, err)
	}
	// The payload names the directory the event happened in; the process's own is
	// the fallback for a harness that leaves it out. That is process state rather
	// than payload, so it is answered here and not in Normalize.
	if !filepath.IsAbs(cwd) {
		if cwd, err = os.Getwd(); err != nil {
			return failOpen("handrail: no working directory, so no rule was evaluated: %v", err)
		}
	}
	rs := rule.Load(cwd)
	matched, outcome := rs.Evaluate(payload)
	return a.Deliver(event, agentMessage(rs, matched), outcome, stdout, stderr)
}

// agentMessage is the wire format the hook path delivers: everything the agent
// should hear, which is the matched messages, then whatever handrail had to
// skip to get there. It stays in the CLI because it is the hook command's own
// output format, with one caller and nothing to disagree with.
func agentMessage(rs *rule.Ruleset, matched []*rule.Rule) string {
	var sections []string
	for _, r := range matched {
		sections = append(sections, fmt.Sprintf("handrail %s: %s (%s)\n%s", r.Action, r.Name, r.Tier, r.Message))
	}
	// Loud fail-open: a rule that cannot be parsed is skipped, and the skipping
	// is named. A guardrail that guards nothing must never look like one that did.
	for _, p := range rs.Problems {
		sections = append(sections, fmt.Sprintf("handrail: skipped the broken rule %s: %s", p.Path, p.Message))
	}
	if notice := rs.TrustNotice(); notice != "" {
		sections = append(sections, notice)
	}
	return strings.Join(sections, "\n\n")
}
