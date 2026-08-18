package rule

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Problem is one rule file that could not be used, and why.
type Problem struct {
	Path    string
	Message string
}

// The three tiers, in precedence order: most specific wins. Tiers are
// convenience layering, not a security boundary.
const (
	TierGlobal          = "global"
	TierProjectShared   = "project-shared"
	TierProjectPersonal = "project-personal"
)

// Discovery is what tier discovery found for one working directory.
type Discovery struct {
	Root    string // the project root: the repo root, or the cwd outside a repo
	Skipped bool   // whether a Project-shared tier held rules and was skipped for want of trust
}

// LoadTiers parses every tier that applies to cwd: Global from the XDG config
// dir, Project-shared at the project root, Project-personal under it. Rules
// come back in delivery order, tier by tier and alphabetical within a tier,
// each tagged with its tier and with the higher-tier rule that shadows it.
func LoadTiers(cwd string) ([]*Rule, []Problem, Discovery) {
	root := RepoRoot(cwd)
	d := Discovery{Root: root}
	shared := SharedDir(root)

	var rules []*Rule
	var problems []Problem
	add := func(tier, dir string, skipLocal bool) {
		if dir == "" {
			return
		}
		rs, ps := load(dir, skipLocal)
		for _, r := range rs {
			r.Tier = tier
		}
		rules = append(rules, rs...)
		problems = append(problems, ps...)
	}

	add(TierGlobal, ConfigDir(), false)
	// A user-level hook entry means any repo on the machine is enforced, so a
	// clone's committed rules wait for an explicit grant. The user's own two
	// tiers are never gated.
	if IsTrusted(root) {
		add(TierProjectShared, shared, true)
	} else if sharedRules, ps := load(shared, true); len(sharedRules)+len(ps) > 0 {
		// The tier is skipped, not unread: strict validation is what check
		// promises for every tier, and .handrail/ existing says nothing on its
		// own, since the Project-personal tier lives inside it.
		d.Skipped = true
		problems = append(problems, ps...)
	}
	add(TierProjectPersonal, LocalDir(root), false)

	// Identity is the basename, so the highest tier holding a name carries the
	// effective rule and every lower one is shadowed by it, wholesale.
	byName := make(map[string]*Rule, len(rules))
	for _, r := range rules {
		byName[r.Name] = r
	}
	for _, r := range rules {
		if effective := byName[r.Name]; effective != r {
			r.ShadowedBy = effective
		}
	}
	return rules, problems, d
}

// SharedDir is the Project-shared tier's directory, at the project root.
func SharedDir(root string) string { return filepath.Join(root, ".handrail") }

// LocalDir is the Project-personal tier's directory, inside the shared one so a
// single exclude line keeps it out of version control.
func LocalDir(root string) string { return filepath.Join(SharedDir(root), "local") }

// ConfigDir is the Global tier's directory: XDG on every Unix platform,
// including macOS, following the git and gh dotfiles precedent rather than
// os.UserConfigDir's ~/Library/Application Support.
func ConfigDir() string {
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

// load parses every rule file under dir, recursively. Rules come back sorted by
// name; every file that cannot be used comes back as a Problem instead. A
// missing dir is not a problem: a repo without rules is a valid repo. skipLocal
// holds back dir/local, which is the Project-personal tier's own scan.
func load(dir string, skipLocal bool) ([]*Rule, []Problem) {
	var rules []*Rule
	var problems []Problem

	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			if p == dir && errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			problems = append(problems, Problem{Path: p, Message: err.Error()})
			return nil
		}
		if d.IsDir() {
			if skipLocal && d.Name() == "local" && filepath.Dir(p) == dir {
				return fs.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".md") {
			return nil
		}
		data, err := os.ReadFile(p)
		if err != nil {
			problems = append(problems, Problem{Path: p, Message: err.Error()})
			return nil //nolint:nilerr // the walk reports bad rules, it does not abort on them
		}
		r, err := Parse(strings.TrimSuffix(d.Name(), ".md"), data)
		if err != nil {
			problems = append(problems, Problem{Path: p, Message: err.Error()})
			return nil //nolint:nilerr // the walk reports bad rules, it does not abort on them
		}
		r.Path = p
		rules = append(rules, r)
		return nil
	})
	if err != nil {
		problems = append(problems, Problem{Path: dir, Message: err.Error()})
	}

	slices.SortFunc(rules, func(a, b *Rule) int {
		if a.Name != b.Name {
			return strings.Compare(a.Name, b.Name)
		}
		return strings.Compare(a.Path, b.Path)
	})

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
