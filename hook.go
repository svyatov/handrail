package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
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
	payloads, cwd, failures := readCall(a, event, stdin)
	// The payload names the directory the event happened in; the process's own is
	// the fallback for a harness that leaves it out. That is process state rather
	// than payload, so it is answered here and not in Normalize. A directory
	// that is not there names no project, whoever named it, but the call was
	// still read, so it keeps its kind and meets the Global tier.
	if !filepath.IsAbs(cwd) {
		cwd, _ = os.Getwd()
	}
	if fi, err := os.Stat(cwd); err != nil || !fi.IsDir() {
		if err == nil {
			err = fmt.Errorf("%s is not a directory", cwd)
		}
		cwd = ""
		failures = append(failures, fmt.Sprintf("handrail: no working directory, so no project rule was evaluated: %v", err))
	}
	if failures != nil {
		for i := range payloads {
			payloads[i].SetField("unreadable", "payload")
		}
	}
	rs := rule.Load(cwd)
	matched, outcome := rs.Evaluate(payloads)
	failures = append(failures, loadNotices(rs)...)
	agent, human := messages(a, rs, event, matched, failures)
	return a.Deliver(event, agent, human, outcome, stdout, stderr)
}

// readCall reads the harness's payload from stdin and normalizes it. A
// payload handrail could not take whole is one kind-less payload declaring
// unreadable: payload, which only a rule naming no kind reaches, and the
// failure is named. Two different faults, so two different messages: stdin
// never arrived, or it arrived and was not the payload. Reporting the second
// as the first sends the reader to look at the pipe when the harness's JSON is
// what to fix.
func readCall(a harness.Adapter, event string, stdin io.Reader) (payloads []rule.Payload, cwd string, failures []string) {
	data, err := io.ReadAll(stdin)
	if err != nil {
		failures = append(failures, fmt.Sprintf("handrail: could not read the %s payload: %v", event, err))
	} else if payloads, cwd, err = a.Normalize(event, data); err != nil {
		failures = append(failures, fmt.Sprintf("handrail: could not parse the %s payload: %v", event, err))
	}
	if failures != nil {
		// A stop it could not read may already be a continuation, so it is
		// read as one: no block rule may continue the agent on it.
		payloads = []rule.Payload{{Event: event, StopHookActive: rule.StopEvent(event)}}
		payloads[0].SetField("unreadable", "payload")
	}
	return payloads, cwd, failures
}

// loadNotices is what the load itself tells both audiences on every event. Loud
// fail-open: a rule that cannot be parsed is skipped, and the skipping is
// named. A guardrail that guards nothing must never look like one that did. A
// tier trust skipped loses nothing, so its broken files stay quiet.
func loadNotices(rs *rule.Ruleset) []string {
	var notices []string
	for _, t := range rs.Tiers {
		if t.Name == rule.TierGlobal && t.Dir == "" {
			notices = append(notices, "handrail: no Global tier to read: set HOME or XDG_CONFIG_HOME")
		}
	}
	for _, p := range rs.Problems {
		if !p.Untrusted {
			notices = append(notices, fmt.Sprintf("handrail: skipped the broken rule %s: %s", p.Path, p.Message))
		}
	}
	return notices
}

// standingNotices are the conditions that hold for the whole session rather
// than fail at one event, so they go out at every SessionStart and nowhere
// else, ahead of any rule's message.
func standingNotices(rs *rule.Ruleset, event string) []string {
	if event != "SessionStart" {
		return nil
	}
	var notices []string
	for _, notice := range []string{droppedNotice(rs.Rules), rs.TrustNotice(), agentOnlyNotice(rs.Rules), examplesNotice(rs.Rules)} {
		if notice != "" {
			notices = append(notices, notice)
		}
	}
	return notices
}

// agentOnlyNotice names the Project-shared rules that lost agent_only, and ""
// when none did. A shadowed or dropped one is named too, so the notice says
// what was dropped rather than who hears it.
func agentOnlyNotice(rules []*rule.Rule) string {
	var lost []string
	for _, r := range rules {
		if r.LostAgentOnly {
			lost = append(lost, r.Name)
		}
	}
	if len(lost) == 0 {
		return ""
	}
	count := fmt.Sprintf("%d rules", len(lost))
	if len(lost) == 1 {
		count = "1 rule"
	}
	return fmt.Sprintf("handrail: %s, so it was dropped from %s: %s; run handrail check",
		rule.RefusedAgentOnly, count, strings.Join(lost, ", "))
}

// droppedNotice counts the Project-shared files dropped for naming a Global
// rule, and "" when there are none. check names them.
func droppedNotice(rules []*rule.Rule) string {
	dropped := 0
	for _, r := range rules {
		if r.DroppedBy != nil {
			dropped++
		}
	}
	if dropped == 0 {
		return ""
	}
	count := fmt.Sprintf("%d Project-shared rules dropped for naming Global rules", dropped)
	if dropped == 1 {
		count = "1 Project-shared rule dropped for naming a Global rule"
	}
	return fmt.Sprintf("handrail: %s; run handrail check", count)
}

// examplesNotice names the rules whose Examples fail, and "" when none do. It
// carries no Example's text: the rule file is where that is read, by check.
func examplesNotice(rules []*rule.Rule) string {
	var failing []string
	for _, r := range rules {
		if harness.FailingExamples(r) != nil {
			failing = append(failing, fmt.Sprintf("%s (%s)", r.Name, r.Tier))
		}
	}
	if len(failing) == 0 {
		return ""
	}
	count := fmt.Sprintf("%d rules fail their", len(failing))
	if len(failing) == 1 {
		count = "1 rule fails its"
	}
	return fmt.Sprintf("handrail: %s Examples: %s; run handrail check", count, strings.Join(failing, ", "))
}

// listedFiles is how many matched files a rule's message names before it
// counts the rest (docs/spec.md section 2, Several edits in one call).
const listedFiles = 10

// messages is the wire format the hook path delivers on event, one text per
// audience. The agent hears the standing notices, the matched messages, then
// handrail's own failures. The human hears the same
// order, but a rule's body only on a block or an ask, where the interruption is
// theirs. Where the agent hears only a block, as on Stop, every other message
// and every notice goes to the human alone, since any text would continue it.
// It stays in the CLI because it is the hook command's own output format.
func messages(a harness.Adapter, rs *rule.Ruleset, event string, matched []rule.Match, failures []string) (agent, human string) {
	sections := standingNotices(rs, event)
	heard := slices.Clone(sections)
	for _, m := range matched {
		label := fmt.Sprintf("handrail %s: %s (%s)", m.Action, m.Name, m.Tier)
		s := label + "\n" + m.Message
		if note := a.Note(m.Rule); note != "" {
			s += "\n" + note
		}
		if len(m.Files) > 0 {
			s += "\nMatched files:\n  " + strings.Join(m.Files[:min(len(m.Files), listedFiles)], "\n  ")
			if len(m.Files) > listedFiles {
				s += fmt.Sprintf("\n  and %d more", len(m.Files)-listedFiles)
			}
		}
		if a.Injects(event) || a.Action(m.Rule) == rule.Block {
			sections = append(sections, s)
		}
		switch {
		case m.AgentOnly:
			continue
		case m.Action == rule.Warn:
			s = label
		}
		heard = append(heard, s)
	}
	if a.Injects(event) {
		sections = append(sections, failures...)
	}
	heard = append(heard, failures...)
	return strings.Join(sections, "\n\n"), strings.Join(heard, "\n")
}
