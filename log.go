package main

import (
	"bytes"
	"cmp"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"slices"
	"strings"
	"text/tabwriter"
	"time"
	"unicode/utf8"

	"github.com/svyatov/handrail/internal/harness"
	"github.com/svyatov/handrail/internal/rule"
)

// logLine is one Decision log line: one payload's evaluation (docs/spec.md
// section 5).
type logLine struct {
	Time       string                          `json:"time"`
	Version    string                          `json:"version"`
	Harness    string                          `json:"harness"`
	Event      string                          `json:"event"`
	SessionID  string                          `json:"session_id"`
	Root       string                          `json:"root"`
	Cwd        string                          `json:"cwd"`
	Kind       string                          `json:"kind"`
	Tool       string                          `json:"tool"`
	Outcome    string                          `json:"outcome"`
	Matched    []logEntry                      `json:"matched"`
	Payload    map[string][]rule.CandidateView `json:"payload"`
	Unreadable []string                        `json:"unreadable"`
	// raw is the line as stored, on a line read back.
	raw []byte
	// Truncated marks a line cut to fit logLineMax.
	Truncated bool `json:"truncated,omitempty"`
}

// logEntry is one rule that matched a line's payload.
type logEntry struct {
	Rule   string `json:"rule"`
	Tier   string `json:"tier"`
	Action string `json:"action"`
	// DegradedFrom is the action the rule file names where the harness
	// delivers another, else null.
	DegradedFrom *string `json:"degraded_from"`
	// Trial is the route that put the rule on trial, the rule's own trial:
	// true or the enforcement state, and absent for a rule that delivers.
	Trial string `json:"trial,omitempty"`
}

// logTime is a line's timestamp: fixed width, so lines sort by it as text.
const logTime = "2006-01-02T15:04:05.000Z07:00"

// logEvent is one hook event as the Decision log records it.
type logEvent struct {
	ruleset *rule.Ruleset
	event   string
	cwd     string
	session string
	adapter harness.Adapter
}

// record appends the Decision log's lines for one event: one per payload in
// which a rule matched or unreadable was set. The grant is read only once there
// is a line to write, so a no-match event performs no I/O. It returns the
// failure to report, none when the lines were written: the log never enforces.
func (rec logEvent) record(payloads []rule.Payload, matched []rule.Match) []string {
	var (
		lines   [][]byte
		granted *bool
	)

	stamp := time.Now().UTC().Format(logTime)

	for index, payload := range rec.ruleset.Yield(payloads) {
		hits := slices.DeleteFunc(slices.Clone(matched), func(m rule.Match) bool {
			return !slices.Contains(m.PayloadIndices, index)
		})
		if len(hits) == 0 && !payload.Has("unreadable") {
			continue
		}

		if granted == nil {
			granted = new(rule.Logging(rec.ruleset.Root))
		}

		outcome := rec.adapter.Delivered(hits)
		// Without a grant, only a trial match earns a line, and the line names
		// the trial entries alone: putting a rule on trial is the request the
		// grant otherwise stands in for.
		if !*granted {
			hits = slices.DeleteFunc(hits, func(m rule.Match) bool { return m.Delivers() })
			if len(hits) == 0 {
				continue
			}
		}

		lines = append(lines, encodeLine(rec.line(stamp, payload, outcome, hits)))
	}

	if lines == nil {
		return nil
	}

	err := rule.AppendLog(lines)
	if err != nil {
		return []string{fmt.Sprintf("handrail: could not write the Decision log: %v", err)}
	}

	return nil
}

// line is the Decision log line for one payload, the Outcome the harness
// delivers for it, and the rules it names.
func (rec logEvent) line(stamp string, payload rule.Payload, outcome rule.Outcome, hits []rule.Match) logLine {
	fields := payload.Fields()
	line := logLine{
		Time: stamp, Version: version, Harness: rec.adapter.Name, Event: rec.event, SessionID: clip(rec.session),
		Root: clip(rec.ruleset.Root), Cwd: clip(rec.cwd), Kind: payload.Kind, Tool: "",
		Outcome: outcome.String(), Matched: []logEntry{}, Payload: fields, Unreadable: []string{}, raw: nil,
		Truncated: false,
	}

	for _, c := range fields["unreadable"] {
		line.Unreadable = append(line.Unreadable, c.Spellings[0])
	}

	delete(fields, "unreadable")

	for _, candidates := range fields {
		for _, c := range candidates {
			for i := range c.Spellings {
				c.Spellings[i] = clip(c.Spellings[i])
			}
		}
	}

	if tool := fields["tool"]; len(tool) > 0 {
		line.Tool = tool[0].Spellings[0]
	}

	for _, hit := range hits {
		action := rec.adapter.Action(hit.Rule)
		entry := logEntry{Rule: clip(hit.Name), Tier: hit.Tier, Action: action.String(), DegradedFrom: nil, Trial: ""}

		if action != hit.Action {
			entry.DegradedFrom = new(hit.Action.String())
		}

		switch {
		case hit.Rule.Trial:
			entry.Trial = "rule"
		case hit.Trial:
			entry.Trial = "state"
		}

		line.Matched = append(line.Matched, entry)
	}

	return line
}

// The Decision log's bounds (docs/spec.md section 5), and the mark a value
// cut at the first one carries.
const (
	logValueMax = 512
	logLineMax  = 4 << 10
	clipMark    = "[truncated]"
)

// clip cuts a value at logValueMax bytes, on a character boundary, and marks
// the cut. The harness transcript holds the whole value.
func clip(value string) string {
	if len(value) <= logValueMax {
		return value
	}

	end := logValueMax
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}

	return value[:end] + clipMark
}

// encodeLine is one line of JSON. A line over logLineMax drops its payload,
// then as many of its last entries as it must, and says it was truncated. A
// line is strings, lists and maps of them alone, so encoding it cannot fail.
func encodeLine(line logLine) []byte {
	for {
		data, _ := json.Marshal(line) //nolint:errchkjson // strings, lists and maps of them cannot fail to encode
		if len(data) < logLineMax || line.Truncated && len(line.Matched) == 0 {
			return append(data, '\n')
		}

		if line.Truncated {
			line.Matched = line.Matched[:len(line.Matched)-1]
		}

		line.Payload, line.Truncated = nil, true
	}
}

// defaultLogCount is how many lines log prints without -n.
const defaultLogCount = 20

// cmdLog grants, revokes or reads the Decision log.
func cmdLog(args []string, _ io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("log", flag.ContinueOnError)
	flags.SetOutput(stderr)
	count := flags.Int("n", defaultLogCount, "print at most this many lines")
	all := flags.Bool("all", false, "every project in the log, not only this one")
	name := flags.String("rule", "", "only the lines where this rule matched")
	asJSON := flags.Bool("json", false, "print the stored lines verbatim")

	verb := leadingArg(args)
	if verb != "" {
		args = args[1:]
	}

	if !parseFlags(flags, args, stderr) {
		return 1
	}

	if *count < 1 {
		fmt.Fprintln(stderr, "handrail log: -n must be at least 1")

		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)

		return 1
	}

	root := rule.RepoRoot(cwd)

	switch verb {
	case "":
		return printLog(logQuery{root: root, all: *all, rule: *name, count: *count, json: *asJSON}, stdout, stderr)
	case "on", "off":
		err = rule.SetLogging(root, verb == "on")
	default:
		fmt.Fprintf(stderr, "handrail log: unknown argument %q; known: on, off\n", verb)

		return 1
	}

	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)

		return 1
	}

	file := rule.LogPaths().Current
	if verb == "on" {
		fmt.Fprintf(stdout, "logging decisions for %s to %s\n", root, file)
	} else {
		fmt.Fprintf(stdout, "stopped logging decisions for %s; its lines stay in %s, and trial matches still write there\n",
			root, file)
	}

	return 0
}

// logQuery is what log reads: whose lines, which rule's, and how many.
type logQuery struct {
	root  string
	rule  string
	count int
	all   bool
	json  bool
}

// readLog is every line of the Decision log, oldest first, across both
// generations. A line that does not parse, which a crash mid-write can leave,
// is skipped.
func readLog() []logLine {
	var lines []logLine

	files := rule.LogPaths()
	for _, file := range []string{files.Older, files.Current} {
		data, _ := os.ReadFile(file)
		for raw := range bytes.Lines(data) {
			var line logLine

			if json.Unmarshal(raw, &line) == nil {
				line.raw = raw
				lines = append(lines, line)
			}
		}
	}

	return lines
}

// noteGrant says on stderr when root holds no Decision log grant, since then
// only trial matches were recorded.
func noteGrant(root string, stderr io.Writer) {
	if !rule.Logging(root) {
		fmt.Fprintln(stderr, "handrail: the Decision log is off for this project, so only trial matches are recorded; "+
			"run handrail log on to record every decision")
	}
}

// projectLines is the Decision log's lines for root, or every project's with
// all, oldest first.
func projectLines(root string, all bool) []logLine {
	lines := readLog()
	if all {
		return lines
	}

	return slices.DeleteFunc(lines, func(l logLine) bool { return l.Root != root })
}

// printLog prints the lines the query asks for, newest first.
func printLog(query logQuery, stdout, stderr io.Writer) int {
	noteGrant(query.root, stderr)

	lines := projectLines(query.root, query.all)
	if query.rule != "" {
		lines = slices.DeleteFunc(lines, func(l logLine) bool {
			return !slices.ContainsFunc(l.Matched, func(e logEntry) bool { return e.Rule == query.rule })
		})
	}

	slices.Reverse(lines)

	for _, line := range lines[:min(query.count, len(lines))] {
		if query.json {
			_, _ = stdout.Write(line.raw)

			continue
		}

		fmt.Fprint(stdout, line.Time)

		if query.all {
			fmt.Fprintf(stdout, "  %s", line.Root)
		}

		fmt.Fprintf(stdout, "  %s  %s  %s", line.Outcome, line.Event, cmp.Or(line.Tool, "-"))

		entries := make([]string, 0, len(line.Matched))
		for _, e := range line.Matched {
			entries = append(entries, e.String())
		}

		if len(entries) > 0 {
			fmt.Fprintf(stdout, "  %s", strings.Join(entries, ", "))
		}

		if len(line.Unreadable) > 0 {
			fmt.Fprintf(stdout, "  unreadable: %s", strings.Join(line.Unreadable, ", "))
		}

		fmt.Fprintln(stdout)
	}

	return 0
}

// ruleStats is one rule's Decision log history in check --stats.
type ruleStats struct {
	FirstSeen *string `json:"first_seen"`
	LastSeen  *string `json:"last_seen"`
	Matches   int     `json:"matches"`
	Sessions  int     `json:"sessions"`
}

// unreadableCount is how many lines declared one field of one tool unreadable.
type unreadableCount struct {
	Tool  string `json:"tool"`
	Field string `json:"field"`
	Count int    `json:"count"`
}

// logStats is the history check --stats adds beside the rules: the unreadable
// counts and the time of the oldest line. No rate: the log records no
// non-matches.
type logStats struct {
	Oldest     *string           `json:"oldest"`
	Unreadable []unreadableCount `json:"unreadable"`
}

// history is one Project root's Decision log lines, oldest first.
type history []logLine

// of is the named rule's history.
func (past history) of(name string) ruleStats {
	stats := ruleStats{FirstSeen: nil, LastSeen: nil, Matches: 0, Sessions: 0}
	sessions := map[string]bool{}

	for _, line := range past {
		for _, e := range line.Matched {
			if e.Rule != name {
				continue
			}

			stats.Matches++
			sessions[line.SessionID] = true

			if stats.FirstSeen == nil {
				stats.FirstSeen = &line.Time
			}

			stats.LastSeen = &line.Time
		}
	}

	stats.Sessions = len(sessions)

	return stats
}

// stats is the history's unreadable counts, in the order each first appears,
// and its oldest line's time.
func (past history) stats() logStats {
	out := logStats{Oldest: nil, Unreadable: []unreadableCount{}}
	if len(past) > 0 {
		out.Oldest = &past[0].Time
	}

	for _, line := range past {
		for _, field := range line.Unreadable {
			found := slices.IndexFunc(out.Unreadable, func(u unreadableCount) bool {
				return u.Tool == line.Tool && u.Field == field
			})
			if found < 0 {
				found = len(out.Unreadable)
				out.Unreadable = append(out.Unreadable, unreadableCount{Tool: line.Tool, Field: field, Count: 0})
			}

			out.Unreadable[found].Count++
		}
	}

	return out
}

// printStats writes check --stats' tables: each effective rule's history, then
// the unreadable counts and the oldest line.
func printStats(w io.Writer, rules []*rule.Rule, past history) error {
	table := tabwriter.NewWriter(w, 0, 0, columnGap, ' ', 0)
	fmt.Fprintln(table, "\nRULE\tTIER\tMATCHES\tSESSIONS\tFIRST SEEN\tLAST SEEN")

	for _, r := range rules {
		s := past.of(r.Name)
		fmt.Fprintf(table, "%s\t%s\t%d\t%d\t%s\t%s\n",
			r.Name, r.Tier, s.Matches, s.Sessions, orDash(s.FirstSeen), orDash(s.LastSeen))
	}

	stats := past.stats()
	if len(stats.Unreadable) > 0 {
		fmt.Fprintln(table, "\nTOOL\tFIELD\tUNREADABLE")

		for _, u := range stats.Unreadable {
			fmt.Fprintf(table, "%s\t%s\t%d\n", cmp.Or(u.Tool, "-"), u.Field, u.Count)
		}
	}

	fmt.Fprintf(table, "\noldest line: %s\n", orDash(stats.Oldest))

	return table.Flush()
}

// orDash is a time as a table prints it, - for none.
func orDash(s *string) string {
	if s == nil {
		return "-"
	}

	return *s
}

// String is the entry as log prints it: the rule, its tier and action, and
// what degraded or put it on trial.
func (e logEntry) String() string {
	about := []string{e.Tier, e.Action}
	if e.DegradedFrom != nil {
		about = append(about, "degraded from "+*e.DegradedFrom)
	}

	if e.Trial != "" {
		about = append(about, "trial")
	}

	return fmt.Sprintf("%s (%s)", e.Rule, strings.Join(about, ", "))
}
