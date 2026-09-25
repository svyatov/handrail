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
	Root  string  // the project root: the repo root, or the cwd outside a repo
	Rules []*Rule // delivery order, shadowed and disabled included
	// Untrusted holds the rules of a tier trust skipped: none of them counts,
	// and check still runs their Examples.
	Untrusted []*Rule
	Tiers     []Tier
	Problems  []Problem
	// Demoted says why .handrail/local/ was read as Project-shared, and is ""
	// where it is the Project-personal tier.
	Demoted string
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
			out = append(out, Problem{Path: r.Path, Message: RefusedAgentOnly})
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

	rs := &Ruleset{Root: root}
	inRoot := func(dir func(string) string) string {
		if root == "" {
			return ""
		}

		return dir(root)
	}

	global := configDir()
	rs.gather(inRoot(LocalDir), Tier{Name: TierGlobal, Dir: global, Trusted: true}, global)
	// A Global file that does not parse still names a Global rule, so it keeps
	// a shared namesake out just as the parsed rule would. Every problem so far
	// is the Global tier's.
	byName := make(map[string]*Rule)

	for _, p := range rs.Problems {
		if name, ok := strings.CutSuffix(filepath.Base(p.Path), ".md"); ok {
			byName[name] = &Rule{Name: name, Path: p.Path, Tier: TierGlobal}
		}
	}
	// A user-level hook entry means any repo on the machine is enforced, so a
	// clone's committed rules wait for an explicit grant. The user's own two
	// tiers are never gated.
	// A tier's identity is its supply: a .handrail/local/ the repository
	// supplies is read as part of the shared tier, gated and add-only with it.
	shared, local := inRoot(sharedDir), inRoot(LocalDir)

	if root != "" {
		rs.Demoted = demotion(root)
	}

	if rs.Demoted == "" {
		rs.gather(local, Tier{Name: TierProjectShared, Dir: shared, Trusted: isTrusted(root)}, shared)
		rs.gather(local, Tier{Name: TierProjectPersonal, Dir: local, Trusted: true}, local)
	} else {
		rs.gather(local, Tier{Name: TierProjectShared, Dir: shared, Trusted: isTrusted(root)}, shared, local)

		for _, r := range slices.Concat(rs.Rules, rs.Untrusted) {
			if strings.HasPrefix(r.Path, local+string(filepath.Separator)) {
				r.DemotedFrom = TierProjectPersonal
			}
		}
	}

	rs.shadow(byName)

	return rs
}

// gather loads one tier into the ruleset, skip being the directory its walk
// does not enter. An untrusted tier is read and then dropped, not left unread:
// strict validation is what check promises for every tier, and .handrail/
// existing says nothing on its own, since the Project-personal tier lives
// inside it.
func (rs *Ruleset) gather(skip string, t Tier, dirs ...string) {
	if t.Dir != "" {
		rules, problems := load(skip, t.Name == TierProjectShared, dirs...)
		for i := range problems {
			problems[i].Untrusted = !t.Trusted
		}

		rs.Problems = append(rs.Problems, problems...)

		if t.Trusted {
			for _, r := range rules {
				r.Tier = t.Name
			}

			t.Count = len(rules)
			rs.Rules = append(rs.Rules, rules...)
		} else {
			t.Skipped = len(rules)+len(problems) > 0
			rs.Untrusted = append(rs.Untrusted, rules...)
		}
	}

	rs.Tiers = append(rs.Tiers, t)
}

// shadow resolves Shadowing across the loaded tiers. byName starts out
// holding the Global files that did not parse.
func (rs *Ruleset) shadow(byName map[string]*Rule) {
	// Identity is the basename, so the highest tier holding a name carries the
	// effective rule and every lower one is shadowed by it, wholesale. The one
	// exception: the shared tier is add-only against Global, so a shared file
	// naming a Global rule is dropped and takes no part in shadowing. That
	// leaves at most one rule for any shadow to replace.
	for _, r := range rs.Rules {
		if global := byName[r.Name]; global != nil && global.Tier == TierGlobal && r.Tier == TierProjectShared {
			r.DroppedBy = global

			continue
		}

		byName[r.Name] = r
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
	if _, err := os.Lstat(LocalDir(root)); err != nil {
		return ""
	}

	for _, dir := range []string{sharedDir(root), LocalDir(root)} {
		if fi, err := os.Lstat(dir); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
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
		err := fs.WalkDir(os.DirFS(dir), ".", func(rel string, d fs.DirEntry, err error) error {
			p := filepath.Join(dir, filepath.FromSlash(rel))
			if err != nil {
				if rel == "." && errors.Is(err, fs.ErrNotExist) {
					return nil
				}

				problems = append(problems, Problem{Path: p, Message: err.Error()})

				return nil
			}

			if d.IsDir() {
				if skips(d, rel, skipped) {
					return fs.SkipDir
				}

				return nil
			}

			if !strings.HasSuffix(d.Name(), ".md") {
				return nil
			}

			r, err := readRule(p, strings.TrimSuffix(d.Name(), ".md"), shared)
			if err != nil {
				problems = append(problems, Problem{Path: p, Message: err.Error()})

				return nil //nolint:nilerr // the walk reports bad rules, it does not abort on them
			}

			rules = append(rules, r)

			return nil
		})
		if err != nil {
			problems = append(problems, Problem{Path: dir, Message: err.Error()})
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

// readRule reads and parses the rule file at p, holding a Project-shared rule
// to what that tier allows.
func readRule(p, name string, shared bool) (*Rule, error) {
	data, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}

	r, err := Parse(name, data)
	if err != nil {
		return nil, err
	}
	// A repository rule that speaks to the agent behind the human's
	// back is refused, and the hook path keeps the rule, louder: a
	// block that set it still blocks.
	if shared && r.AgentOnly {
		r.AgentOnly, r.LostAgentOnly = false, true
	}

	if err := r.checkAgentOnly(); err != nil {
		return nil, err
	}

	r.Path = p

	return r, nil
}

// unique keeps the first of each name among rules sorted by name, and reports
// every repeat as a Problem.
func unique(rules []*Rule) ([]*Rule, []Problem) {
	var problems []Problem
	// Identity is the basename, so a repeat inside one tier is ambiguous.
	kept := make([]*Rule, 0, len(rules))
	for i, r := range rules {
		if i > 0 && rules[i-1].Name == r.Name {
			problems = append(problems, Problem{
				Path:    r.Path,
				Message: fmt.Sprintf("duplicate rule name %q (also at %s)", r.Name, rules[i-1].Path),
			})

			continue
		}

		kept = append(kept, r)
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
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}

	for d := dir; ; {
		if _, err := os.Lstat(filepath.Join(d, ".git")); err == nil {
			return d
		}

		parent := filepath.Dir(d)
		if parent == d {
			return dir
		}

		d = parent
	}
}
