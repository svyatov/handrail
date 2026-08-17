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

// Load parses every rule file under dir, recursively, skipping dir/local (the
// Project-personal tier is its own scan). Rules come back sorted by name; every
// file that cannot be used comes back as a Problem instead. A missing dir is
// not a problem: a repo without rules is a valid repo.
func Load(dir string) ([]*Rule, []Problem) {
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
			if d.Name() == "local" && filepath.Dir(p) == dir {
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
			return nil
		}
		r, err := Parse(strings.TrimSuffix(d.Name(), ".md"), data)
		if err != nil {
			problems = append(problems, Problem{Path: p, Message: err.Error()})
			return nil
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
// there is none. It walks up for .git rather than shelling out to git: this
// runs on the hook hot path, where a process spawn is most of the budget.
func RepoRoot(dir string) string {
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
