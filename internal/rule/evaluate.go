package rule

import (
	"net/netip"
	"net/url"
	"path"
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
	// files are the files command names, which a shell call yields as payloads
	// of their own.
	files []shell.File
	// StopHookActive is set when a stop hook already continued the agent, so a
	// block, which would continue it again, is not evaluated. It is not a
	// field: the engine applies the loop guard once, so no rule has to.
	StopHookActive bool
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
// alike. tool gains a Spelling and unreadable an entry rather than being
// replaced, since a call answers to several names and each failure adds its
// own.
func (p *Payload) SetField(name, value string) bool {
	// The agent's last message is read without the whitespace around it, so
	// starts_with and ends_with meet its first and last words.
	if name == fieldResponse {
		value = strings.TrimSpace(value)
	}

	if value == "" {
		return false
	}

	if p.fields == nil {
		p.fields = make(map[string][]candidate)
	}

	return p.set(name, value)
}

// SetRename fills path with both files a rename names, source then
// destination, each its own Candidate, so a not_ term on path fires when either
// one fails it. An empty one is left out, as SetField leaves it, and an edit
// that stays put names its one file once.
func (p *Payload) SetRename(from, to string) {
	p.SetField(fieldPath, from)

	src := p.fields[fieldPath]
	if p.SetField(fieldPath, to) && (len(src) == 0 || src[0].spellings[0] != p.fields[fieldPath][0].spellings[0]) {
		p.fields[fieldPath] = append(src, p.fields[fieldPath]...)
	}
}

// Unset drops a field and the unreadable entry for it, and a url's domain
// with them, so the next SetField replaces what it held rather than adding to
// it. A command's files need no dropping: setting a command replaces them.
func (p *Payload) Unset(name string) {
	if p.fields == nil {
		return
	}

	delete(p.fields, name)

	gone := []string{name}
	if name == fieldURL {
		delete(p.fields, "domain")

		gone = append(gone, "domain")
	}

	p.fields[fieldUnreadable] = slices.DeleteFunc(p.fields[fieldUnreadable], func(c candidate) bool {
		return slices.Contains(gone, c.spellings[0])
	})
}

// Has reports whether the payload carries a canonical field. Not carrying it
// is the same answer SetField gives when it refuses an empty value.
func (p *Payload) Has(name string) bool { return len(p.fields[name]) > 0 }

// CandidateView is one Candidate as a report shows it: its Spellings, and
// whether it is the whole command line, which only positive terms read. One
// with no Spellings stands for a command line that runs no command.
type CandidateView struct {
	Spellings []string `json:"spellings"`
	Whole     bool     `json:"whole,omitempty"`
}

// Fields returns every field the payload carries, each as its Candidates.
func (p *Payload) Fields() map[string][]CandidateView {
	out := make(map[string][]CandidateView, len(p.fields))
	for name, cands := range p.fields {
		for _, c := range cands {
			out[name] = append(out[name], CandidateView{Spellings: append([]string{}, c.spellings...), Whole: c.whole})
		}
	}

	return out
}

// set writes a value SetField has accepted, and reports whether it wrote.
func (p *Payload) set(name, value string) bool {
	switch name {
	case fieldCommand:
		p.setCommand(value)
	case fieldPath:
		// Cleaned where it is set, never joined to cwd, so src/../.env is .env
		// to a rule however the call spelled it.
		p.fields[name] = []candidate{spelled(path.Clean(value))}
	case fieldTool:
		p.setTool(value)
	case fieldURL:
		p.setURL(value)
	case fieldNetworkGrant:
		// Each grant is its own Candidate.
		host := grant(value)
		if host == "" {
			return false
		}

		p.fields[name] = append(p.fields[name], spelled(host))
	case fieldUnreadable:
		if !slices.ContainsFunc(p.fields[name], func(c candidate) bool { return c.spellings[0] == value }) {
			p.fields[name] = append(p.fields[name], spelled(value))
		}
	default:
		p.fields[name] = []candidate{spelled(value)}
	}

	return true
}

// spelled is a Candidate with the one Spelling, not the whole command line.
func spelled(spelling string) candidate {
	return candidate{spellings: []string{spelling}, whole: false}
}

// setTool adds a name the call answers to.
func (p *Payload) setTool(value string) {
	// One call, however many names it answers to: each is a Spelling of
	// the one Candidate, so a not_ term fires only when no name matches.
	if len(p.fields[fieldTool]) == 0 {
		p.fields[fieldTool] = []candidate{{spellings: nil, whole: false}}
	}

	if c := &p.fields[fieldTool][0]; !slices.Contains(c.spellings, value) {
		c.spellings = append(c.spellings, value)
	}
}

// setURL adds a url and the domain read from it, or declares the domain
// unreadable.
func (p *Payload) setURL(value string) {
	// Each url is its own Candidate, and so is the domain read from it.
	p.fields[fieldURL] = append(p.fields[fieldURL], spelled(value))
	if host, ok := domainOf(value); ok {
		p.fields["domain"] = append(p.fields["domain"], spelled(host))
	} else {
		p.SetField(fieldUnreadable, "domain")
	}
}

// grant normalizes a network_grant to the host alone: lowercased, without its
// port or IPv6 brackets. A leading *. stays, since it is what widens the grant.
func grant(value string) string {
	host := strings.ToLower(value)
	if rest, ok := strings.CutPrefix(host, "["); ok {
		host, _, _ = strings.Cut(rest, "]")
	} else {
		host, _, _ = strings.Cut(host, ":")
	}

	return host
}

// domainOf reads the host a url names, never resolving it: lowercased, with
// port, userinfo, IPv6 brackets and one trailing dot removed. A url that will
// not parse, names no host, or leaves a host character outside [a-z0-9.-] (an
// IP literal excepted) has no domain handrail can vouch for, because a host it
// reads differently from the fetcher is one a rule can be steered past.
func domainOf(raw string) (string, bool) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", false
	}

	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")

	_, err = netip.ParseAddr(host)
	if err == nil {
		return host, true
	}
	// A fetcher reads a host whose last label is a number as an IPv4 address,
	// however it is spelled (2852039166, 0xa9fea9fe, 169.254.43518), and
	// netip reads only the dotted-decimal form.
	last := host[strings.LastIndex(host, ".")+1:]

	hex, isHex := strings.CutPrefix(last, "0x")
	if strings.Trim(last, "0123456789") == "" || isHex && strings.Trim(hex, "0123456789abcdef") == "" {
		return "", false
	}

	if strings.ContainsFunc(host, outsideHost) {
		return "", false
	}

	return host, true
}

// outsideHost reports whether r falls outside [a-z0-9.-].
func outsideHost(r rune) bool {
	return (r < 'a' || r > 'z') && (r < '0' || r > '9') && r != '.' && r != '-'
}

// setCommand fills command with the whole line and every Candidate the shell
// reader finds. A line that will not parse declares so in unreadable and keeps
// the whole line for positive terms.
func (p *Payload) setCommand(line string) {
	read, files, parsed := shell.Read(line)
	p.files = files
	// The whole line, the Candidates read, and the empty one when there are none.
	cands := make([]candidate, 0, 1+len(read)+1)

	cands = append(cands, candidate{spellings: []string{line}, whole: true})
	for _, spellings := range read {
		cands = append(cands, candidate{spellings: spellings, whole: false})
	}
	// bash runs the lines before a syntax error, so a line handrail read no
	// command from is one an allowlist cannot vouch for.
	if len(read) == 0 {
		cands = append(cands, candidate{spellings: nil, whole: false})
	}

	p.fields[fieldCommand] = cands
	if !parsed {
		p.SetField(fieldUnreadable, fieldCommand)
	}
}

// withFiles returns the payloads with every file a shell call names after
// them, each its own file_edit or file_read payload carrying that one path
// and the call's own event and tool. Only a shell call yields them: a tool
// handrail does not classify contributes its command, not its files.
func withFiles(payloads []Payload) []Payload {
	all := slices.Clip(payloads) // appending never writes into the caller's array
	for _, call := range payloads {
		if call.Kind != kindShell {
			continue
		}

		for _, file := range call.files {
			named := Payload{
				Event: call.Event, Kind: kindFileRead,
				fields: map[string][]candidate{fieldTool: call.fields[fieldTool]}, files: nil, StopHookActive: false,
			}
			if file.Write {
				named.Kind = kindFileEdit
			}

			if file.Unreadable {
				// As written: an expansion in it may hold a /, so cleaning it
				// could only invent a path.
				named.fields[fieldPath] = []candidate{spelled(file.Path)}
				named.SetField(fieldUnreadable, fieldPath)
			} else {
				named.SetField(fieldPath, file.Path)
			}

			all = append(all, named)
		}
	}

	return all
}

// Yield returns every payload an event's payloads yield, the ones Evaluate
// reads: each of them, then every file a shell call among them names. A load
// that lost rules declares unreadable: rules on every payload, the ones passed
// in included, so a rule that is left can fail closed on the loss.
func (rs *Ruleset) Yield(payloads []Payload) []Payload {
	payloads = withFiles(payloads)
	if rs.Unreadable() {
		for i := range payloads {
			payloads[i].SetField(fieldUnreadable, "rules")
		}
	}

	return payloads
}

// Evaluate runs an event's payloads against the Effective ruleset and answers
// with both halves of what the event produces: the rules that matched any of
// its payloads, once each and in delivery order (tier order, then alphabetical
// within a tier), and the Outcome, the strongest Action among those not on
// trial, or allow when none matched. A caller deriving the Outcome for itself
// would be a second answer to the same question, free to disagree with this
// one, and test exists to say what hook will do. What a harness delivers of it
// is the Adapter's answer, not a second one to this.
//
// Liveness is checked inline rather than over rs.Effective(), because this is
// the hot path and the selector would allocate a second slice per event.
func (rs *Ruleset) Evaluate(payloads []Payload) ([]Match, Outcome) {
	var (
		matched []Match
		outcome Outcome
	)

	continued := slices.ContainsFunc(payloads, func(p Payload) bool { return p.StopHookActive })

	payloads = rs.Yield(payloads)
	for _, live := range rs.Rules {
		if !live.Live() || continued && live.Action == Block {
			continue
		}

		match, hit := live.matchAll(payloads)
		if !hit {
			continue
		}
		// The weaker of the rule's own state and the Enforcement state applies.
		match.Trial = live.Trial || rs.State == StateTrial

		matched = append(matched, match)
		if match.Delivers() {
			outcome = max(outcome, live.Action)
		}
	}

	return matched, outcome
}

// matchAll is this rule's Match over an event's payloads, and whether it
// matched any of them.
func (r *Rule) matchAll(payloads []Payload) (Match, bool) {
	match, hit := Match{Rule: r, Files: nil, Trial: false}, false
	for _, payload := range payloads {
		if !r.matches(payload) {
			continue
		}

		hit = true

		match.addFiles(payload)
	}
	// One file is the one the message is about, with nothing to list.
	if len(match.Files) == 1 {
		match.Files = nil
	}

	return match, hit
}

// Match is a rule that matched an event. A rule is delivered once however
// many of the event's payloads it matched, and when they name several files,
// Files lists them.
type Match struct {
	*Rule

	Files []string
	// Trial marks a match that delivers nothing: no Outcome, no message, no
	// ordering slot.
	Trial bool
}

// Delivers reports whether the match reaches the harness at all, which a
// match on trial never does.
func (m *Match) Delivers() bool { return !m.Trial }

// addFiles adds every path a matched payload names that Files lacks.
func (m *Match) addFiles(p Payload) {
	for _, c := range p.fields[fieldPath] {
		if !slices.Contains(m.Files, c.spellings[0]) {
			m.Files = append(m.Files, c.spellings[0])
		}
	}
}

// matches reports whether this rule's matcher selects the payload. Whether the
// rule can fire at all is Live's question, not this one's.
func (r *Rule) matches(payload Payload) bool {
	if r.Event != payload.Event {
		return false
	}

	if r.Kind != "" && r.Kind != payload.Kind {
		return false
	}

	return r.choose(payload, make([]*candidate, 0, len(r.fields)))
}

// choose picks one Candidate for each field the rule names, in turn, and
// matches when some choice satisfies every condition: terms on one field read
// the same Candidate, and terms on different fields choose independently (ADR
// 0024). A field the payload does not carry is chosen as nil, which no term
// meets, in either polarity: "path does not end with .env" says nothing about
// a shell command that has no path at all.
func (r *Rule) choose(payload Payload, chosen []*candidate) bool {
	if len(chosen) == len(r.fields) {
		return r.satisfied(chosen)
	}

	cands := payload.fields[r.fields[len(chosen)]]
	if len(cands) == 0 {
		return r.choose(payload, append(chosen, nil))
	}

	for i := range cands {
		if r.choose(payload, append(chosen, &cands[i])) {
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
func (t *Term) meets(chosen *candidate) bool {
	operator, negated := strings.CutPrefix(t.Op, "not_")
	if chosen == nil || negated && chosen.whole {
		return false
	}

	for _, s := range chosen.spellings {
		if t.hit(operator, s) {
			return !negated
		}
	}

	return negated
}

// hit applies the operator, without its polarity, to one value.
func (t *Term) hit(operator, value string) bool {
	switch operator {
	case opMatches, opGlob:
		return t.re.MatchString(value)
	case opContains:
		return strings.Contains(value, t.Value)
	case opEquals:
		return value == t.Value
	case opStartsWith:
		return strings.HasPrefix(value, t.Value)
	case opEndsWith:
		return strings.HasSuffix(value, t.Value)
	}

	return false
}
