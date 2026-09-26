package rule

import (
	"cmp"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/svyatov/handrail/internal/gitindex"
)

// Problem is one rule file that could not be used, and why.
type Problem struct {
	Path    string
	Message string
	// Untrusted marks a problem in a tier trust skipped, whose rules would
	// count for nothing had they parsed: check reports it, the hook path does
	// not.
	Untrusted bool
}

// The three tiers, in precedence order: most specific wins. Tiers are
// convenience layering, not a security boundary.
const (
	TierGlobal          = "global"
	TierProjectShared   = "project-shared"
	TierProjectPersonal = "project-personal"
)

// Tier is one tier as the load found it: where it was read from, how many rules
// it contributed, and whether trust let it contribute at all.
type Tier struct {
	Name    string
	Dir     string // "" when nothing names one: no home for Global, no working directory for the Project tiers
	Count   int    // rules this tier contributed
	Trusted bool   // trust gates the Project-shared tier; the user's own two carry it
	Skipped bool   // this tier held something and went untrusted, so none of it counts
}

// Ruleset is every rule that applies to one working directory, plus what the
// load had to say about producing it. Shadowing is resolved on the rules rather
// than by dropping them, so check can report what a higher tier replaced.
// Problems are reported, never judged: the hook path is loud fail-open and the
// authoring commands are strict.
type Ruleset struct {
	Root string // the project root: the repo root, or the cwd outside a repo
	// Demoted says why .handrail/local/ was read as Project-shared, and is ""
	// where it is the Project-personal tier.
	Demoted string
	Rules   []*Rule // delivery order, shadowed and disabled included
	// Untrusted holds the rules of a tier trust skipped: none of them counts,
	// and check still runs their Examples.
	Untrusted []*Rule
	Tiers     []Tier
	Problems  []Problem
	// State is the Enforcement state for Root, and StateScope where it was set.
	State      State
	StateScope Scope
}

// Effective returns the Effective ruleset: the rules that can fire, in
// delivery order. Callers that ask what is enforced want this; Rules is the
// full set, shadowed and disabled included, for reporting on the load itself.
func (rs *Ruleset) Effective() []*Rule {
	var out []*Rule

	for _, r := range rs.Rules {
		if r.Live() {
			out = append(out, r)
		}
	}

	return out
}

// RefusedAgentOnly is why a Project-shared rule that set agent_only lost it.
const RefusedAgentOnly = "agent_only is refused in the Project-shared tier"

// Invalid is every problem the authoring commands refuse: the files the load
// skipped, then each Project-shared rule that set agent_only, trusted or not,
// which the hook path keeps rather than skips.
func (rs *Ruleset) Invalid() []Problem {
	out := slices.Clone(rs.Problems)
	for _, r := range slices.Concat(rs.Rules, rs.Untrusted) {
		if r.LostAgentOnly {
			out = append(out, Problem{Path: r.Path, Message: RefusedAgentOnly, Untrusted: false})
		}
	}

	return out
}

// Unreadable reports whether the load lost rules an event should have been
// evaluated against: a tier with no directory to read, or a rule file skipped
// in a tier whose rules count.
func (rs *Ruleset) Unreadable() bool {
	return slices.ContainsFunc(rs.Tiers, func(t Tier) bool { return t.Dir == "" }) ||
		slices.ContainsFunc(rs.Problems, func(p Problem) bool { return !p.Untrusted })
}

// Load parses every tier that applies to cwd: Global from the XDG config dir,
// Project-shared at the project root, Project-personal under it. Rules come
// back in delivery order, tier by tier and alphabetical within a tier, each
// tagged with its tier and with the higher-tier rule that shadows it. An empty
// cwd is no working directory, where only the Global tier can be located.
func Load(cwd string) *Ruleset {
	var root string
	if cwd != "" {
		root = RepoRoot(cwd)
	}

	ruleset := &Ruleset{
		Root: root, Demoted: "", Rules: nil, Untrusted: nil, Tiers: nil, Problems: nil,
		State: StateEnforce, StateScope: ScopeDefault,
	}
	ruleset.State, ruleset.StateScope = StateOf(root)
	inRoot := func(dir func(string) string) string {
		if root == "" {
			return ""
		}

		return dir(root)
	}

	global := configDir()
	ruleset.gather(inRoot(LocalDir), Tier{Name: TierGlobal, Dir: global, Count: 0, Trusted: true, Skipped: false}, global)
	// A Global file that does not parse still names a Global rule, so it keeps
	// a shared namesake out just as the parsed rule would. Every problem so far
	// is the Global tier's.
	byName := make(map[string]*Rule)

	for _, p := range ruleset.Problems {
		if name, ok := strings.CutSuffix(filepath.Base(p.Path), ".md"); ok {
			byName[name] = &Rule{
				Name: name, Path: p.Path, Tier: TierGlobal, ShadowedBy: nil, Replaces: nil, DroppedBy: nil,
				DemotedFrom: "", Event: "", Kind: "", Message: "", Conditions: nil, Examples: nil, fields: nil,
				Action: Allow, agentOnlyLine: 0, Enabled: false, AgentOnly: false, LostAgentOnly: false, Trial: false, trialLine: 0,
			}
		}
	}
	// A user-level hook entry means any repo on the machine is enforced, so a
	// clone's committed rules wait for an explicit grant. The user's own two
	// tiers are never gated.
	// A tier's identity is its supply: a .handrail/local/ the repository
	// supplies is read as part of the shared tier, gated and add-only with it.
	shared, local := inRoot(sharedDir), inRoot(LocalDir)

	if root != "" {
		ruleset.Demoted = demotion(root)
	}

	sharedTier := Tier{Name: TierProjectShared, Dir: shared, Count: 0, Trusted: isTrusted(root), Skipped: false}
	if ruleset.Demoted == "" {
		ruleset.gather(local, sharedTier, shared)
		ruleset.gather(local, Tier{Name: TierProjectPersonal, Dir: local, Count: 0, Trusted: true, Skipped: false}, local)
	} else {
		ruleset.gather(local, sharedTier, shared, local)

		for _, r := range slices.Concat(ruleset.Rules, ruleset.Untrusted) {
			if strings.HasPrefix(r.Path, local+string(filepath.Separator)) {
				r.DemotedFrom = TierProjectPersonal
			}
		}
	}

	ruleset.shadow(byName)

	return ruleset
}

// gather loads one tier into the ruleset, skip being the directory its walk
// does not enter. An untrusted tier is read and then dropped, not left unread:
// strict validation is what check promises for every tier, and .handrail/
// existing says nothing on its own, since the Project-personal tier lives
// inside it.
func (rs *Ruleset) gather(skip string, tier Tier, dirs ...string) {
	if tier.Dir != "" {
		rules, problems := load(skip, tier.Name == TierProjectShared, dirs...)
		for i := range problems {
			problems[i].Untrusted = !tier.Trusted
		}

		rs.Problems = append(rs.Problems, problems...)

		if tier.Trusted {
			for _, r := range rules {
				r.Tier = tier.Name
			}

			tier.Count = len(rules)
			rs.Rules = append(rs.Rules, rules...)
		} else {
			tier.Skipped = len(rules)+len(problems) > 0
			rs.Untrusted = append(rs.Untrusted, rules...)
		}
	}

	rs.Tiers = append(rs.Tiers, tier)
}

// shadow resolves Shadowing across the loaded tiers. byName starts out
// holding the Global files that did not parse.
func (rs *Ruleset) shadow(byName map[string]*Rule) {
	// Identity is the basename, so the highest tier holding a name carries the
	// effective rule and every lower one is shadowed by it, wholesale. The one
	// exception: the shared tier is add-only against Global, so a shared file
	// naming a Global rule is dropped and takes no part in shadowing. That
	// leaves at most one rule for any shadow to replace.
	for _, loaded := range rs.Rules {
		if global := byName[loaded.Name]; global != nil && global.Tier == TierGlobal && loaded.Tier == TierProjectShared {
			loaded.DroppedBy = global

			continue
		}

		byName[loaded.Name] = loaded
	}

	for _, r := range rs.Rules {
		if effective := byName[r.Name]; effective != r && r.DroppedBy == nil {
			r.ShadowedBy = effective
			effective.Replaces = r
		}
	}
}

// The two directory names every path in this package is built from: excludeLine
// and the walk's skip are spelled from them rather than repeating the literals.
// The CLI's own messages name the same paths in prose, so a rename is still a
// grep, not a one-line edit.
const (
	sharedName = ".handrail"
	localName  = "local"
)

// sharedDir is the Project-shared tier's directory, at the project root.
func sharedDir(root string) string { return filepath.Join(root, sharedName) }

// LocalDir is the Project-personal tier's directory, inside the shared one so a
// single exclude line keeps it out of version control. Exported for import,
// which writes into the tier without loading it.
func LocalDir(root string) string { return filepath.Join(sharedDir(root), localName) }

// configDir is the Global tier's directory: XDG on every Unix platform,
// including macOS, following the git and gh dotfiles precedent rather than
// os.UserConfigDir's ~/Library/Application Support.
func configDir() string {
	return xdgSubdir("XDG_CONFIG_HOME", ".config")
}

// xdgSubdir returns handrail's directory under an XDG base, or "" when neither
// the variable nor a home directory says where that is. A relative value is
// ignored, as the XDG basedir spec requires.
func xdgSubdir(env, fallback string) string {
	base := os.Getenv(env)
	if !filepath.IsAbs(base) {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}

		base = filepath.Join(home, fallback)
	}

	return filepath.Join(base, "handrail")
}

// demotion says why root's .handrail/local/ is supplied by the repository, and
// "" when it is genuinely local or absent. An index that cannot be read cannot
// vouch for the tier, so it is read as the repository's too.
func demotion(root string) string {
	_, err := os.Lstat(LocalDir(root))
	if err != nil {
		return ""
	}

	for _, dir := range []string{sharedDir(root), LocalDir(root)} {
		info, statErr := os.Lstat(dir)
		if statErr == nil && info.Mode()&fs.ModeSymlink != 0 {
			return dir + " is a symlink"
		}
	}

	tracked, err := gitindex.Under(root, sharedName+"/"+localName)
	switch {
	case err != nil:
		return "cannot read the git index: " + err.Error()
	case tracked:
		return excludeLine + " is in the git index"
	}

	return ""
}

// load parses every rule file under dirs, recursively, as one tier. Rules come
// back sorted by name; every file that cannot be used comes back as a Problem
// instead. A missing dir is not a problem: a repo without rules is a valid
// repo. skip is a directory no walk enters, since the Project-personal tier
// sits inside the shared one; it is still walked when it is one of dirs. It is
// matched by identity, not spelling, because a case-insensitive filesystem
// gives it more than one. A dir that is a symlink is followed, one inside a
// dir is not. shared marks the Project-shared tier, which refuses agent_only.
func load(skip string, shared bool, dirs ...string) ([]*Rule, []Problem) {
	var (
		rules    []*Rule
		problems []Problem
	)

	skipped, _ := os.Stat(skip) // nil where there is nothing to skip

	for _, dir := range dirs {
		err := fs.WalkDir(os.DirFS(dir), ".", func(rel string, entry fs.DirEntry, err error) error {
			file := filepath.Join(dir, filepath.FromSlash(rel))
			if err != nil {
				if rel == "." && errors.Is(err, fs.ErrNotExist) {
					return nil
				}

				problems = append(problems, Problem{Path: file, Message: err.Error(), Untrusted: false})

				return nil
			}

			if entry.IsDir() {
				if skips(entry, rel, skipped) {
					return fs.SkipDir
				}

				return nil
			}

			if !strings.HasSuffix(entry.Name(), ".md") {
				return nil
			}

			parsed, err := readRule(file, strings.TrimSuffix(entry.Name(), ".md"), shared)
			if err != nil {
				problems = append(problems, Problem{Path: file, Message: err.Error(), Untrusted: false})

				return nil //nolint:nilerr // the walk reports bad rules, it does not abort on them
			}

			rules = append(rules, parsed)

			return nil
		})
		if err != nil {
			problems = append(problems, Problem{Path: dir, Message: err.Error(), Untrusted: false})
		}
	}

	slices.SortFunc(rules, func(a, b *Rule) int {
		return cmp.Or(strings.Compare(a.Name, b.Name), strings.Compare(a.Path, b.Path))
	})
	kept, duplicates := unique(rules)

	return kept, append(problems, duplicates...)
}

// skips reports whether the walk is at skip's directory, other than the one it
// started from.
func skips(d fs.DirEntry, rel string, skipped fs.FileInfo) bool {
	info, err := d.Info()

	return err == nil && rel != "." && os.SameFile(info, skipped)
}

// readRule reads and parses the rule file at file, holding a Project-shared
// rule to what that tier allows.
func readRule(file, name string, shared bool) (*Rule, error) {
	data, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}

	parsed, err := Parse(name, data)
	if err != nil {
		return nil, err
	}
	// A repository rule that speaks to the agent behind the human's
	// back is refused, and the hook path keeps the rule, louder: a
	// block that set it still blocks.
	if shared && parsed.AgentOnly {
		parsed.AgentOnly, parsed.LostAgentOnly = false, true
	}

	err = parsed.checkAgentOnly()
	if err != nil {
		return nil, err
	}

	parsed.Path = file

	return parsed, nil
}

// unique keeps the first of each name among rules sorted by name, and reports
// every repeat as a Problem.
func unique(rules []*Rule) ([]*Rule, []Problem) {
	var problems []Problem
	// Identity is the basename, so a repeat inside one tier is ambiguous.
	kept := make([]*Rule, 0, len(rules))
	for i, sorted := range rules {
		if i > 0 && rules[i-1].Name == sorted.Name {
			problems = append(problems, Problem{
				Path:      sorted.Path,
				Message:   fmt.Sprintf("duplicate rule name %q (also at %s)", sorted.Name, rules[i-1].Path),
				Untrusted: false,
			})

			continue
		}

		kept = append(kept, sorted)
	}

	return kept, problems
}

// RepoRoot returns the git repository root containing dir, or dir itself when
// there is none, always as a symlink-free path. It walks up for .git rather
// than shelling out to git: this runs on the hook hot path, where a process
// spawn is most of the budget.
//
// It is also the Project root every path-keyed operation takes: Trust,
// ExcludeLocal, LocalExcluded. Those are keyed by the root and nothing else, so
// Load is not their prerequisite. A caller that already holds a ruleset passes
// rs.Root; a caller that only needs the path calls this and reads no rules.
func RepoRoot(dir string) string {
	// Trust is keyed by path, and the cwd a harness reports need not be spelled
	// the same way as the one handrail trust saw: /tmp and /private/tmp are one
	// repo, and a grant given through one must not read as absent through the
	// other. Resolving here settles it for every caller at once.
	resolved, err := filepath.EvalSymlinks(dir)
	if err == nil {
		dir = resolved
	}

	for current := dir; ; {
		_, err := os.Lstat(filepath.Join(current, ".git"))
		if err == nil {
			return current
		}

		parent := filepath.Dir(current)
		if parent == current {
			return dir
		}

		current = parent
	}
}
