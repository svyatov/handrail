package rule

import (
	"slices"
	"strings"

	"github.com/svyatov/handrail/internal/shell"
)

// Payload is the canonical event payload a matcher is evaluated against: the
// envelope fields a matcher can select on, plus the per-kind normalized fields.
// The zero value is a valid empty payload, so an Adapter that fails to
// normalize can return one.
//
// Fill one, then read it: Evaluate reads each Payload by value, and a copy shares
// the map its fields live in, so a SetField on the copy would write through to
// the original once that map exists and be dropped while it does not. Nothing
// does that today, and this is the sentence saying not to start.
type Payload struct {
	Event string
	Kind  string
	// fields is unexported so that SetField is the only way in. The rule it
	// enforces is a matcher's rule, so it belongs to this package rather than to
	// each Adapter that fills a payload in.
	fields map[string][]candidate
}

// candidate is one thing the call does, as one field sees it, written every
// way it can be: a Candidate and its Spellings (ADR 0024). One with no
// Spellings stands for a command line that runs no command: no positive term
// meets it and every not_ term does.
type candidate struct {
	spellings []string
	// whole marks the whole command line, which positive terms read and a not_
	// term never does: it always holds an allowed command's text.
	whole bool
}

// SetField writes a canonical field unless the value is empty, and reports
// whether it wrote. A field the event carries empty is no answer about that
// field rather than an answer of nothing, so it stays absent, and a condition
// against it reads absence: no match, in either polarity.
//
// content is deliberately not an exception, and it is the case that costs
// something: a Write carrying content "" or an Edit carrying new_string ""
// truncates a real file, and no content condition sees it, in either polarity.
// Presenting the empty string instead would fire every not_contains rule
// against text nobody wrote, which is the louder mistake and the one that
// blocks the wrong call.
//
// command is read here as a shell program, so the hook path and test read it
// alike, and unreadable gains an entry rather than being replaced, since each
// failure adds its own.
func (p *Payload) SetField(name, value string) bool {
	if value == "" {
		return false
	}
	if p.fields == nil {
		p.fields = make(map[string][]candidate)
	}
	switch name {
	case "command":
		p.setCommand(value)
	case "unreadable":
		if !slices.ContainsFunc(p.fields[name], func(c candidate) bool { return c.spellings[0] == value }) {
			p.fields[name] = append(p.fields[name], candidate{spellings: []string{value}})
		}
	default:
		p.fields[name] = []candidate{{spellings: []string{value}}}
	}
	return true
}

// setCommand fills command with the whole line and every Candidate the shell
// reader finds. A line that will not parse declares so in unreadable and keeps
// the whole line for positive terms.
func (p *Payload) setCommand(line string) {
	read, ok := shell.Read(line)
	cands := make([]candidate, 0, len(read)+2)
	cands = append(cands, candidate{spellings: []string{line}, whole: true})
	for _, spellings := range read {
		cands = append(cands, candidate{spellings: spellings})
	}
	// bash runs the lines before a syntax error, so a line handrail read no
	// command from is one an allowlist cannot vouch for.
	if len(read) == 0 {
		cands = append(cands, candidate{})
	}
	p.fields["command"] = cands
	if !ok {
		p.SetField("unreadable", "command")
	}
}

// Has reports whether the payload carries a canonical field. Not carrying it
// is the same answer SetField gives when it refuses an empty value.
func (p Payload) Has(name string) bool { return len(p.fields[name]) > 0 }

// Evaluate runs an event's payloads against the Effective ruleset and answers
// with both halves of what the event produces: the rules that matched any of
// its payloads, once each and in delivery order (tier order, then alphabetical
// within a tier), and the Outcome, the strongest Action among them, or allow
// when nothing matched. A caller deriving the Outcome for itself would be a
// second answer to the same question, free to disagree with this one, and test
// exists to say what hook will do.
//
// Liveness is checked inline rather than over rs.Effective(), because this is
// the hot path and the selector would allocate a second slice per event.
func (rs *Ruleset) Evaluate(payloads []Payload) (matched []*Rule, outcome Outcome) {
	for _, r := range rs.Rules {
		if !r.Live() || !slices.ContainsFunc(payloads, r.matches) {
			continue
		}
		matched = append(matched, r)
		outcome = max(outcome, r.Action)
	}
	return matched, outcome
}

// matches reports whether this rule's matcher selects the payload. Whether the
// rule can fire at all is Live's question, not this one's.
func (r *Rule) matches(p Payload) bool {
	if r.Event != p.Event {
		return false
	}
	if r.Kind != "" && r.Kind != p.Kind {
		return false
	}
	return r.choose(p, make([]*candidate, 0, len(r.fields)))
}

// choose picks one Candidate for each field the rule names, in turn, and
// matches when some choice satisfies every condition: terms on one field read
// the same Candidate, and terms on different fields choose independently (ADR
// 0024). A field the payload does not carry is chosen as nil, which no term
// meets, in either polarity: "path does not end with .env" says nothing about
// a shell command that has no path at all.
func (r *Rule) choose(p Payload, chosen []*candidate) bool {
	if len(chosen) == len(r.fields) {
		return r.satisfied(chosen)
	}
	cands := p.fields[r.fields[len(chosen)]]
	if len(cands) == 0 {
		return r.choose(p, append(chosen, nil))
	}
	for i := range cands {
		if r.choose(p, append(chosen, &cands[i])) {
			return true
		}
	}
	return false
}

// satisfied reports whether every condition has a term the chosen Candidates
// meet.
func (r *Rule) satisfied(chosen []*candidate) bool {
	for _, c := range r.Conditions {
		if !slices.ContainsFunc(c.Terms, func(t Term) bool { return t.meets(chosen[t.slot]) }) {
			return false
		}
	}
	return true
}

// meets reports whether one Candidate meets the term. A positive term is met
// when any Spelling matches; a not_ term when none does, which is the
// allowlist reading: not_starts_with: git fires on git status; rm -rf /,
// because rm -rf / is a Candidate with no Spelling starting with git.
func (t *Term) meets(c *candidate) bool {
	op, negated := strings.CutPrefix(t.Op, "not_")
	if c == nil || negated && c.whole {
		return false
	}
	for _, s := range c.spellings {
		if t.hit(op, s) {
			return !negated
		}
	}
	return negated
}

// hit applies the operator, without its polarity, to one value.
func (t *Term) hit(op, v string) bool {
	switch op {
	case "matches", "glob":
		return t.re.MatchString(v)
	case "contains":
		return strings.Contains(v, t.Value)
	case "equals":
		return v == t.Value
	case "starts_with":
		return strings.HasPrefix(v, t.Value)
	case "ends_with":
		return strings.HasSuffix(v, t.Value)
	}
	return false
}
