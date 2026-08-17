package rule

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// excludeLine keeps the Project-personal tier out of version control.
const excludeLine = ".handrail/local/"

// readExclude reads root's exclude file and names it. Outside a git working
// tree there is no such file, and the empty path says so: that is not the same
// as a file whose line went missing, which is the distinction doctor reports on.
func readExclude(root string) (path string, data []byte, err error) {
	git, err := gitDir(root)
	if err != nil || git == "" {
		return "", nil, err
	}
	path = filepath.Join(git, "info", "exclude")
	data, err = os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", nil, err
	}
	return path, data, nil
}

// hasExcludeLine reports whether an exclude file's contents already hold it.
func hasExcludeLine(data []byte) bool {
	for line := range strings.SplitSeq(string(data), "\n") {
		if strings.TrimSpace(line) == excludeLine {
			return true
		}
	}
	return false
}

// LocalExcluded reports whether root's exclude file holds the line, and names
// the file it read. An empty path means root is not a git working tree, where
// there is nothing to exclude and therefore nothing missing.
func LocalExcluded(root string) (excluded bool, path string, err error) {
	path, data, err := readExclude(root)
	if err != nil || path == "" {
		return false, "", err
	}
	return hasExcludeLine(data), path, nil
}

// ExcludeLocal adds the Project-personal tier to root's own exclude file,
// reporting whether that was new. info/exclude rather than .gitignore: the tier
// is one user's, and the ignore rule for it is nobody else's business.
func ExcludeLocal(root string) (added bool, err error) {
	path, data, err := readExclude(root)
	if err != nil || path == "" || hasExcludeLine(data) {
		return false, err
	}

	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return false, err
	}
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return false, err
	}
	// A file whose last line has no newline would otherwise glue our pattern
	// onto the end of the user's.
	prefix := ""
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		prefix = "\n"
	}
	if _, err := fmt.Fprint(f, prefix+excludeLine+"\n"); err != nil {
		_ = f.Close()
		return false, err
	}
	return true, f.Close()
}

// gitDir returns the directory holding root's info/exclude, or "" when root is
// not a working tree. A worktree or submodule keeps its .git elsewhere and
// leaves a pointer file behind; a worktree then shares info/exclude with the
// main checkout, which is where git reads it from, so the commondir hop is not
// optional. Read directly rather than shelled out to git: this is the same walk
// RepoRoot already does, and handrail runs where git may not be installed.
func gitDir(root string) (string, error) {
	git := filepath.Join(root, ".git")
	fi, err := os.Stat(git)
	if err != nil {
		return "", nil //nolint:nilerr // no .git is not a failure, it is "not a working tree"
	}
	if fi.IsDir() {
		return git, nil
	}

	data, err := os.ReadFile(git)
	if err != nil {
		return "", err
	}
	pointer := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(string(data)), "gitdir:"))
	if pointer == "" {
		return "", fmt.Errorf("%s names no git directory", git)
	}
	if !filepath.IsAbs(pointer) {
		pointer = filepath.Join(root, pointer)
	}

	common, err := os.ReadFile(filepath.Join(pointer, "commondir"))
	if errors.Is(err, fs.ErrNotExist) {
		return pointer, nil // a submodule: its own git dir is the whole story
	}
	if err != nil {
		return "", err
	}
	shared := strings.TrimSpace(string(common))
	if !filepath.IsAbs(shared) {
		shared = filepath.Join(pointer, shared)
	}
	return filepath.Clean(shared), nil
}
