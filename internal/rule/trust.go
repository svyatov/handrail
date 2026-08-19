package rule

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The trust registry is one project root per line in the XDG state dir. It
// gates the Project-shared tier only: cloning a repo cannot put its committed
// rules in front of the agent until the user grants that path once.

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

// Trust records this ruleset's project root as trusted, reporting whether that
// was new. The load already knows the root, so a caller holding a ruleset never
// has to work out which path the grant is keyed by.
func (rs *Ruleset) Trust() (added bool, err error) {
	// One path per line, so a newline in a path would write a second line and
	// grant a path nobody asked for. A directory may legally hold one.
	if strings.Contains(rs.Root, "\n") {
		return false, fmt.Errorf("cannot trust a path containing a newline: %q", rs.Root)
	}
	if isTrusted(rs.Root) {
		return false, nil
	}
	file := trustFile()
	if file == "" {
		return false, errors.New("no state directory: set HOME or XDG_STATE_HOME")
	}
	if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
		return false, err
	}
	f, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return false, err
	}
	if _, err := fmt.Fprintln(f, rs.Root); err != nil {
		_ = f.Close()
		return false, err
	}
	return true, f.Close()
}
