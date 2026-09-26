package main

import (
	"errors"
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

// hookArgs is hook's positional arguments: the harness and the event.
const hookArgs = 2

// errNotDir is eventDir's reason for a path that names something else.
var errNotDir = errors.New("is not a directory")

// cmdHook is the entrypoint sync installs into each harness. Everything it can
// get wrong past argument parsing fails open and says so: a guardrail manager
// that wedges the harness is worse than the harness without it.
func cmdHook(args []string, stdin io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("hook", flag.ContinueOnError)
	flags.SetOutput(stderr)

	err := flags.Parse(args)
	if err != nil {
		return 1
	}

	if flags.NArg() != hookArgs {
		fmt.Fprint(stderr, hookUsage)

		return 1
	}

	name, event := flags.Arg(0), flags.Arg(1)
	// The hook entry is handrail's own writing, so a wrong harness or event is a
	// bug in the installed config rather than something to soldier through.
	adapter, ok := harness.Lookup(name)
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
	call, err := readCall(adapter, event, stdin)
	payloads := call.Payloads

	var failures []string
	if err != nil {
		failures = append(failures, err.Error())
	}
	// A directory that is not there names no project, whoever named it, but the
	// call was still read, so it keeps its kind and meets the Global tier.
	cwd, err := eventDir(call.Cwd)
	if err != nil {
		failures = append(failures, fmt.Sprintf("handrail: no working directory, so no project rule was evaluated: %v", err))
	}

	if failures != nil {
		for i := range payloads {
			payloads[i].SetField("unreadable", "payload")
		}
	}

	ruleset := rule.Load(cwd)
	if ruleset.State == rule.StateOff {
		return deliverOff(adapter, ruleset, event, stdout, stderr)
	}

	matched, outcome := ruleset.Evaluate(payloads)
	failures = append(failures, loadNotices(ruleset)...)
	// A line the log could not write is reported like any failure of
	// handrail's own, and the Outcome stands.
	logged := logEvent{ruleset: ruleset, event: event, cwd: cwd, session: call.Session, adapter: adapter}
	failures = append(failures, logged.record(payloads, matched)...)

	text := messages(adapter, ruleset, event, matched, failures)

	return adapter.Deliver(event, text.agent, text.human, outcome, stdout, stderr)
}

// deliverOff is what hook delivers under the off state: the state notice at
// SessionStart, to both audiences, and nothing anywhere else.
func deliverOff(adapter harness.Adapter, ruleset *rule.Ruleset, event string, stdout, stderr io.Writer) int {
	notice := ""
	if event == "SessionStart" {
		notice = ruleset.StateNotice()
	}

	return adapter.Deliver(event, notice, notice, rule.Allow, stdout, stderr)
}

// eventDir is the directory the event happened in, and "" with the reason when
// that is no directory. The payload names it; the process's own is the
// fallback for a harness that leaves it out. That is process state rather than
// payload, so it is answered here and not in Normalize.
func eventDir(cwd string) (string, error) {
	if !filepath.IsAbs(cwd) {
		cwd, _ = os.Getwd()
	}

	fi, err := os.Stat(cwd)
	if err == nil && !fi.IsDir() {
		err = fmt.Errorf("%s %w", cwd, errNotDir)
	}

	if err != nil {
		return "", err
	}

	return cwd, nil
}

// readCall reads the harness's payload from stdin and normalizes it. A
// payload handrail could not take whole is one kind-less payload declaring
// unreadable: payload, which only a rule naming no kind reaches, returned with
// the failure that names it. Two different faults, so two different messages:
// stdin never arrived, or it arrived and was not the payload. Reporting the
// second as the first sends the reader to look at the pipe when the harness's
// JSON is what to fix.
func readCall(adapter harness.Adapter, event string, stdin io.Reader) (harness.Call, error) {
	data, err := io.ReadAll(stdin)
	if err != nil {
		return harness.Call{Cwd: "", Session: "", Payloads: unreadableCall(event)},
			fmt.Errorf("handrail: could not read the %s payload: %w", event, err)
	}

	call, err := adapter.Normalize(event, data)
	if err != nil {
		call.Payloads = unreadableCall(event)

		return call, fmt.Errorf("handrail: could not parse the %s payload: %w", event, err)
	}

	return call, nil
}

// unreadableCall is the one payload a call handrail could not read stands in
// as. A stop it could not read may already be a continuation, so it is read as
// one: no block rule may continue the agent on it.
func unreadableCall(event string) []rule.Payload {
	payloads := []rule.Payload{{Event: event, Kind: "", StopHookActive: rule.StopEvent(event)}}
	payloads[0].SetField("unreadable", "payload")

	return payloads
}

// loadNotices is what the load itself tells both audiences on every event. Loud
// fail-open: a rule that cannot be parsed is skipped, and the skipping is
// named. A guardrail that guards nothing must never look like one that did. A
// tier trust skipped loses nothing, so its broken files stay quiet.
func loadNotices(ruleset *rule.Ruleset) []string {
	var notices []string

	for _, t := range ruleset.Tiers {
		if t.Name == rule.TierGlobal && t.Dir == "" {
			notices = append(notices, "handrail: no Global tier to read: set HOME or XDG_CONFIG_HOME")
		}
	}

	for _, p := range ruleset.Problems {
		if !p.Untrusted {
			notices = append(notices, fmt.Sprintf("handrail: skipped the broken rule %s: %s", p.Path, p.Message))
		}
	}

	return notices
}

// standingNotices are the conditions that hold for the whole session rather
// than fail at one event, so they go out at every SessionStart and nowhere
// else, ahead of any rule's message.
func standingNotices(ruleset *rule.Ruleset, event string) []string {
	if event != "SessionStart" {
		return nil
	}

	var notices []string

	for _, notice := range []string{
		ruleset.StateNotice(), droppedNotice(ruleset.Rules), ruleset.TrustNotice(),
		agentOnlyNotice(ruleset.Rules), examplesNotice(ruleset.Rules),
	} {
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
func messages(
	adapter harness.Adapter, ruleset *rule.Ruleset, event string, matched []rule.Match, failures []string,
) audiences {
	sections := standingNotices(ruleset, event)
	heard := slices.Clone(sections)

	// A match on trial delivers nothing, so it has no section.
	for _, match := range slices.DeleteFunc(slices.Clone(matched), func(m rule.Match) bool { return !m.Delivers() }) {
		label := fmt.Sprintf("handrail %s: %s (%s)", match.Action, match.Name, match.Tier)

		section := label + "\n" + match.Message
		if note := adapter.Note(match.Rule); note != "" {
			section += "\n" + note
		}

		if len(match.Files) > 0 {
			section += "\nMatched files:\n  " + strings.Join(match.Files[:min(len(match.Files), listedFiles)], "\n  ")
			if len(match.Files) > listedFiles {
				section += fmt.Sprintf("\n  and %d more", len(match.Files)-listedFiles)
			}
		}

		if adapter.Injects(event) || adapter.Action(match.Rule) == rule.Block {
			sections = append(sections, section)
		}

		switch {
		case match.AgentOnly:
			continue
		case match.Action == rule.Warn:
			section = label
		}

		heard = append(heard, section)
	}

	if adapter.Injects(event) {
		sections = append(sections, failures...)
	}

	heard = append(heard, failures...)

	return audiences{agent: strings.Join(sections, "\n\n"), human: strings.Join(heard, "\n")}
}

// audiences is one text per audience of an event, "" where one hears nothing.
type audiences struct {
	agent, human string
}
