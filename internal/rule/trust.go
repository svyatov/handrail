package rule

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Trust end to end: the registry that records it, and what the user is told
// when a tier goes untrusted. The registry is one project root per line in the
// XDG state dir, and it gates the Project-shared tier only: cloning a repo
// cannot put its committed rules in front of the agent until the user grants
// that path once.

var (
	errNewlinePath = errors.New("cannot trust a path containing a newline")
	errNoStateDir  = errors.New("no state directory: set HOME or XDG_STATE_HOME")
)

// The registry is the user's alone, so nobody else may read or list it.
const (
	registryDirMode = 0o700
	registryMode    = 0o600
)

func trustFile() string {
	dir := xdgSubdir("XDG_STATE_HOME", filepath.Join(".local", "state"))
	if dir == "" {
		return ""
	}

	return filepath.Join(dir, "trusted")
}

// isTrusted reports whether root's Project-shared tier has been granted. An
// unreadable registry means untrusted: the gate fails closed.
func isTrusted(root string) bool {
	file := trustFile()
	if file == "" {
		return false
	}

	data, err := os.ReadFile(file)
	if err != nil {
		return false
	}

	return slices.Contains(strings.Split(string(data), "\n"), root)
}

// TrustNotice is what a skipped Project-shared tier owes the user, and "" when
// nothing was skipped. The tiers and the root are the ruleset's own state, so
// the sentence is written here rather than assembled by every caller that has
// to print it.
func (rs *Ruleset) TrustNotice() string {
	for _, t := range rs.Tiers {
		if t.Skipped {
			return fmt.Sprintf(
				"handrail: skipping the untrusted Project-shared rules in %s; run handrail trust to enable them",
				rs.Root)
		}
	}

	return ""
}

// Trust records a project root as trusted, reporting whether that was new. The
// grant is keyed by the root alone, so granting one needs no ruleset: reading
// the rules is what the grant gates, not a prerequisite for making it.
func Trust(root string) (bool, error) {
	// One path per line, so a newline in a path would write a second line and
	// grant a path nobody asked for. A directory may legally hold one.
	if strings.Contains(root, "\n") {
		return false, fmt.Errorf("%w: %q", errNewlinePath, root)
	}

	if isTrusted(root) {
		return false, nil
	}

	file := trustFile()
	if file == "" {
		return false, errNoStateDir
	}

	err := os.MkdirAll(filepath.Dir(file), registryDirMode)
	if err != nil {
		return false, err
	}

	out, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, registryMode)
	if err != nil {
		return false, err
	}

	_, err = fmt.Fprintln(out, root)
	if err != nil {
		_ = out.Close()

		return false, err
	}

	return true, out.Close()
}
