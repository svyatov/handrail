package rule

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/svyatov/handrail/internal/gitindex"
)

// excludeLine keeps the Project-personal tier out of version control. Git wants
// forward slashes here whatever the platform, so it is not filepath.Join.
const excludeLine = sharedName + "/" + localName + "/"

// readExclude reads root's exclude file and names it. Outside a git working
// tree there is no such file, and the empty path says so: that is not the same
// as a file whose line went missing, which is the distinction doctor reports on.
func readExclude(root string) (path string, data []byte, err error) {
	// A linked worktree shares info/exclude with the main checkout, which is
	// where git reads it from, so this is the common directory.
	_, git, err := gitindex.Dirs(root)
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

// LocalExcluded reports whether a project's exclude file holds the line, and
// names the file it read. An empty path means the project root is not a git
// working tree, where there is nothing to exclude and therefore nothing missing.
func LocalExcluded(root string) (excluded bool, path string, err error) {
	path, data, err := readExclude(root)
	if err != nil || path == "" {
		return false, "", err
	}

	return hasExcludeLine(data), path, nil
}

// ExcludeLocal adds the Project-personal tier to a project's own exclude file,
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
