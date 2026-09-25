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

// ExcludeFile is a project's exclude file as read. Outside a git working tree
// there is no such file, and the empty Path says so: that is not the same as a
// file whose line went missing, which is the distinction doctor reports on.
type ExcludeFile struct {
	Path     string
	Excluded bool // the file holds the line
}

// readExclude reads root's exclude file, and returns it with its contents.
func readExclude(root string) (ExcludeFile, []byte, error) {
	none := ExcludeFile{Path: "", Excluded: false}
	// A linked worktree shares info/exclude with the main checkout, which is
	// where git reads it from, so this is the common directory.
	dirs, err := gitindex.Dirs(root)
	if err != nil || dirs.Common == "" {
		return none, nil, err
	}

	path := filepath.Join(dirs.Common, "info", "exclude")

	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return none, nil, err
	}

	return ExcludeFile{Path: path, Excluded: hasExcludeLine(data)}, data, nil
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

// LocalExcluded reads a project's exclude file: whether it holds the line, and
// the file it read. An empty Path means the project root is not a git working
// tree, where there is nothing to exclude and therefore nothing missing.
func LocalExcluded(root string) (ExcludeFile, error) {
	file, _, err := readExclude(root)

	return file, err
}

// ExcludeLocal adds the Project-personal tier to a project's own exclude file,
// reporting whether that was new. info/exclude rather than .gitignore: the tier
// is one user's, and the ignore rule for it is nobody else's business.
func ExcludeLocal(root string) (bool, error) {
	file, data, err := readExclude(root)
	if err != nil || file.Path == "" || file.Excluded {
		return false, err
	}

	err = os.MkdirAll(filepath.Dir(file.Path), dirMode)
	if err != nil {
		return false, err
	}

	out, err := os.OpenFile(file.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, fileMode)
	if err != nil {
		return false, err
	}
	// A file whose last line has no newline would otherwise glue our pattern
	// onto the end of the user's.
	prefix := ""
	if len(data) > 0 && !strings.HasSuffix(string(data), "\n") {
		prefix = "\n"
	}

	_, err = fmt.Fprint(out, prefix+excludeLine+"\n")
	if err != nil {
		_ = out.Close()

		return false, err
	}

	return true, out.Close()
}
