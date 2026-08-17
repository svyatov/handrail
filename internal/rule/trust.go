package rule

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
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

// IsTrusted reports whether root's Project-shared tier has been granted. An
// unreadable registry means untrusted: the gate fails closed.
func IsTrusted(root string) bool {
	file := trustFile()
	if file == "" {
		return false
	}
	data, err := os.ReadFile(file)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(data), "\n") {
		if line == root {
			return true
		}
	}
	return false
}

// Trust records root as trusted, reporting whether that was new.
func Trust(root string) (added bool, err error) {
	// One path per line, so a newline in a path would write a second line and
	// grant a path nobody asked for. A directory may legally hold one.
	if strings.Contains(root, "\n") {
		return false, fmt.Errorf("cannot trust a path containing a newline: %q", root)
	}
	if IsTrusted(root) {
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
	if _, err := fmt.Fprintln(f, root); err != nil {
		f.Close()
		return false, err
	}
	return true, f.Close()
}
