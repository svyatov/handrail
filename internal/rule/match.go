package rule

import "strings"

// Payload is the canonical event payload a matcher is evaluated against: the
// envelope fields a matcher can select on, plus the per-kind normalized fields.
// A field the event does not carry is absent from Fields, which is not the same
// as being present and empty.
type Payload struct {
	Event  string
	Kind   string
	Fields map[string]string
}

// Match returns the rules of the Effective ruleset that select the payload, in
// delivery order: tier order, then alphabetical within a tier. Liveness is
// checked inline rather than over rs.Effective(), because this is the hot path
// and the selector would allocate a second slice per event.
func (rs *Ruleset) Match(p Payload) []*Rule {
	var out []*Rule
	for _, r := range rs.Rules {
		if r.Live() && r.Matches(p) {
			out = append(out, r)
		}
	}
	return out
}

// Matches reports whether this rule's matcher selects the payload. Whether the
// rule can fire at all is Live's question, not this one's.
func (r *Rule) Matches(p Payload) bool {
	if r.Event != p.Event {
		return false
	}
	if r.Kind != "" && r.Kind != p.Kind {
		return false
	}
	for _, c := range r.Conditions {
		if c.Term != nil {
			if !c.Term.matches(p) {
				return false
			}
			continue
		}
		hit := false
		for i := range c.Any {
			if c.Any[i].matches(p) {
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
	v, ok := p.Fields[t.Field]
	if !ok {
		return false
	}
	op, negated := strings.CutPrefix(t.Op, "not_")
	var hit bool
	switch op {
	case "matches", "glob":
		hit = t.Re.MatchString(v)
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
