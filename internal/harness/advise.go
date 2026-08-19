package harness

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/svyatov/handrail/internal/rule"
)

// Advice is one rule promoted to a harness-native mechanism: what to paste,
// where, and what the native layer will not do that the rule still does.
// Recommend-only, always: handrail neither writes nor tracks the entry.
type Advice struct {
	Mechanism string
	Entry     string
	Location  string
	Caveats   []string
}

// The rule stays the message-bearing layer either way, so every promotion says
// so: a native deny states no reason, and an agent that hears none retries.
const silentCaveat = "the native entry denies without a message, so keep the rule: the hook path is what tells the agent why"

// native is one harness's translation knowledge: where its mechanism lives,
// what to call it, and how a condition is spelled there. One value per harness
// keeps the mechanism and the spelling from ever disagreeing about who is
// supported.
type native struct {
	mechanism string
	location  string
	caveats   []string // what every promotion to this harness carries
	// entries spells one condition, and names the caveat that spelling earns.
	entries func(*rule.Rule, rule.Term) (spelled []string, caveat string, ok bool)
}

func (a Adapter) native() (native, bool) {
	switch a.Name {
	case "claude":
		return native{
			mechanism: "permissions.deny",
			location:  a.ConfigPath(),
			entries:   claudeEntries,
		}, true
	case "codex":
		return native{
			mechanism: "execpolicy prefix_rule",
			location:  filepath.Join(a.userDir(), "rules", "default.rules"),
			caveats: []string{
				"codex exec --ignore-rules bypasses execpolicy, so the backstop is absent there while the rule still fires",
			},
			entries: codexEntries,
		}, true
	}
	return native{}, false
}

// Advise translates a rule into this harness's native mechanism, and reports
// whether it translates at all. Only provably-safe translations qualify: an
// entry that blocks more than its rule would be a surprise handrail authored
// into the user's own config, so anything the native pattern language cannot
// say exactly is left alone (ADR 0005).
func (a Adapter) Advise(r *rule.Rule) (Advice, bool) {
	n, ok := a.native()
	if !ok {
		return Advice{}, false
	}
	// A permission deny answers a tool call, so a block on any other event has
	// nothing to promote to. Conditions AND together, and a native entry is one
	// pattern: two conditions cannot be one entry, and either alone blocks more
	// than the pair does.
	if r.Action != rule.Block || r.Event != "PreToolUse" || len(r.Conditions) != 1 {
		return Advice{}, false
	}
	terms := r.Conditions[0].Any
	if t := r.Conditions[0].Term; t != nil {
		terms = []rule.Term{*t}
	}

	// A deny list is a disjunction, which is exactly what an any group is, so
	// each branch earns its own entry. One untranslatable branch sinks the rule:
	// the remaining entries would be a guardrail with a hole in it.
	adv := Advice{Mechanism: n.mechanism, Location: n.location}
	adv.Caveats = append(adv.Caveats, silentCaveat)
	adv.Caveats = append(adv.Caveats, n.caveats...)
	var entries []string
	for _, t := range terms {
		spelled, caveat, ok := n.entries(r, t)
		if !ok {
			return Advice{}, false
		}
		entries = append(entries, spelled...)
		if caveat != "" && !slices.Contains(adv.Caveats, caveat) {
			adv.Caveats = append(adv.Caveats, caveat)
		}
	}
	if len(entries) == 0 {
		return Advice{}, false
	}
	adv.Entry = strings.Join(entries, "\n")
	return adv, true
}

// Claude Code splits a compound command and matches each subcommand, so a Bash
// deny reaches further than the rule's own string comparison does.
const compoundCaveat = "Claude Code matches each subcommand of a compound command, so the entry also denies this prefix after && or |, which the rule's own string match does not reach"

// claudeEntries spells a condition as Claude Code permission rules: Bash(...)
// for a command prefix, Read(...) and Edit(...) for a path pattern.
func claudeEntries(r *rule.Rule, t rule.Term) ([]string, string, bool) {
	switch t.Field {
	case "command":
		if !commandCondition(r, t) {
			return nil, "", false
		}
		// Claude Code globs a Bash pattern, so a bare value is an exact match and
		// a trailing star is the prefix: the two operators, verbatim.
		if t.Op == "equals" {
			return []string{"Bash(" + t.Value + ")"}, compoundCaveat, true
		}
		return []string{"Bash(" + t.Value + "*)"}, compoundCaveat, true
	case "path":
		if !pathCondition(t) {
			return nil, "", false
		}
		// A leading / means the filesystem root in a rule and the settings file's
		// own directory in Claude Code, whose spelling for the root is //.
		pattern := t.Value
		if strings.HasPrefix(pattern, "/") {
			pattern = "/" + pattern
		}
		// Read rules cover reading, Edit rules cover every built-in editing tool.
		// A rule that names no kind reaches both, so its entry must too.
		switch r.Kind {
		case "file_read":
			return []string{"Read(" + pattern + ")"}, "", true
		case "file_edit":
			return []string{"Edit(" + pattern + ")"}, "", true
		case "":
			return []string{"Read(" + pattern + ")", "Edit(" + pattern + ")"}, "", true
		}
	}
	return nil, "", false
}

// An execpolicy pattern is a list of argv tokens, where a rule compares a
// string, so the two agree on whole arguments and nowhere finer.
const tokenCaveat = "execpolicy matches whole argv tokens, so the entry forbids this prefix at argument boundaries rather than as a raw string"

// codexEntries spells a command condition as an execpolicy prefix rule. Codex
// has no path mechanism at this grain: execpolicy governs what runs, and a rule
// about files earns no Codex advice rather than a coarse sandbox setting.
func codexEntries(r *rule.Rule, t rule.Term) ([]string, string, bool) {
	// A prefix rule forbids every command that extends the pattern, which is
	// what starts_with says and is strictly more than equals says. There is no
	// exact-match form to fall back on, so an equals condition earns nothing.
	if t.Field != "command" || t.Op != "starts_with" || !commandCondition(r, t) {
		return nil, "", false
	}
	fields := strings.Fields(t.Value)
	if len(fields) == 0 {
		return nil, "", false
	}
	tokens := make([]string, len(fields))
	for i, f := range fields {
		tokens[i] = strconv.Quote(f)
	}
	return []string{fmt.Sprintf("prefix_rule(\n    pattern = [%s],\n    decision = \"forbidden\",\n    justification = %s,\n)",
		strings.Join(tokens, ", "), strconv.Quote("handrail rule: "+r.Name))}, tokenCaveat, true
}

// commandCondition reports whether a condition is a command prefix a native
// mechanism can state exactly. Negations are excluded because no native
// mechanism denies everything except a pattern.
func commandCondition(r *rule.Rule, t rule.Term) bool {
	if t.Op != "equals" && t.Op != "starts_with" {
		return false
	}
	// The command field exists only on a shell call, so a rule that names no kind
	// still reaches nothing else; one that names another kind never matches at all.
	if r.Kind != "" && r.Kind != "shell" {
		return false
	}
	// A rule compares strings; a native pattern is globbed, split into
	// subcommands, or tokenized. Punctuation that any of that reads as syntax
	// would promote to an entry blocking something the rule does not.
	return t.Value != "" && !strings.ContainsAny(t.Value, "*?[]\\\"'`$&|;<>()\n\t")
}

// pathCondition reports whether a path condition is a pattern Claude Code's
// gitignore-syntax path rules state exactly.
func pathCondition(t rule.Term) bool {
	if t.Op != "equals" && t.Op != "glob" {
		return false
	}
	// Character classes and escapes differ between the two dialects, and a
	// difference here is silently the wrong set of files.
	if t.Value == "" || strings.ContainsAny(t.Value, "[]\\\n") {
		return false
	}
	// A rule's glob is anchored to the whole path, and harnesses report absolute
	// paths, so only an absolute pattern or a **/ prefix ever matches one. A
	// relative pattern would translate into an entry matching at any depth: live
	// where the rule is dead.
	//
	// An exact path is absolute for the same reason. A gitignore pattern naming a
	// directory covers what is under it, which equals does not, but a path a
	// reading or editing tool passes is a file, so the difference has nothing to
	// match on.
	if t.Op == "equals" {
		return strings.HasPrefix(t.Value, "/")
	}
	return strings.HasPrefix(t.Value, "/") || strings.HasPrefix(t.Value, "**/")
}
