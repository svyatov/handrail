package rule

import "strings"

// Payload is the canonical event payload a matcher is evaluated against: the
// envelope fields a matcher can select on, plus the per-kind normalized fields.
// The zero value is a valid empty payload, so an Adapter that fails to
// normalize can return one.
//
// Fill one, then read it: Evaluate takes a Payload by value, and a copy shares
// the map its fields live in, so a SetField on the copy would write through to
// the original once that map exists and be dropped while it does not. Nothing
// does that today, and this is the sentence saying not to start.
type Payload struct {
	Event string
	Kind  string
	// fields is unexported so that SetField is the only way in. The rule it
	// enforces is a matcher's rule, so it belongs to this package rather than to
	// each Adapter that fills a payload in.
	fields map[string]string
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
func (p *Payload) SetField(name, value string) bool {
	if value == "" {
		return false
	}
	if p.fields == nil {
		p.fields = make(map[string]string)
	}
	p.fields[name] = value
	return true
}

// Field reads a canonical field. Empty means the payload does not carry it,
// which is the same answer SetField refuses to write.
func (p Payload) Field(name string) string { return p.fields[name] }

// Evaluate runs the payload against the Effective ruleset and answers with both
// halves of what an event produces: the rules that matched, in delivery order
// (tier order, then alphabetical within a tier), and the Outcome, allow, warn
// or block, where one block among several matches makes it block. A caller
// deriving the Outcome for itself would be a second answer to the same
// question, free to disagree with this one, and test exists to say what hook
// will do.
//
// Liveness is checked inline rather than over rs.Effective(), because this is
// the hot path and the selector would allocate a second slice per event.
func (rs *Ruleset) Evaluate(p Payload) (matched []*Rule, outcome string) {
	outcome = Allow
	for _, r := range rs.Rules {
		if !r.Live() || !r.matches(p) {
			continue
		}
		matched = append(matched, r)
		if r.Action == Block {
			outcome = Block
		} else if outcome == Allow {
			outcome = Warn
		}
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
	for _, c := range r.Conditions {
		hit := false
		for i := range c.Terms {
			if c.Terms[i].matches(p) {
				hit = true
				break
			}
		}
		if !hit {
			return false
		}
	}
	return true
}

// matches applies one operator to one field. A condition against a field the
// payload does not carry never matches, in either polarity: "path does not end
// with .env" says nothing about a shell command that has no path at all.
func (t *Term) matches(p Payload) bool {
	v, ok := p.fields[t.Field]
	if !ok {
		return false
	}
	op, negated := strings.CutPrefix(t.Op, "not_")
	var hit bool
	switch op {
	case "matches", "glob":
		hit = t.re.MatchString(v)
	case "contains":
		hit = strings.Contains(v, t.Value)
	case "equals":
		hit = v == t.Value
	case "starts_with":
		hit = strings.HasPrefix(v, t.Value)
	case "ends_with":
		hit = strings.HasSuffix(v, t.Value)
	}
	return hit != negated
}
