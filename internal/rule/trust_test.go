package rule

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// White box: the CLI covers the trust registry end to end (ADR 0009), and what
// is left is the one path no shell can hand the binary, because an argument
// cannot carry a newline through argv on every platform the tests run on. A
// ruleset built here is the only way to put such a root in front of Trust.
func TestTrustRefusesAPathThatWouldWriteTwoLines(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	registry := filepath.Join(state, "handrail", "trusted")

	// A directory may legally hold a newline, and the registry is one path per
	// line: granting this one would grant "/tmp/evil" as well.
	rs := &Ruleset{Root: "/tmp/a\n/tmp/evil"}
	added, err := rs.Trust()
	if err == nil {
		t.Fatal("Trust() accepted a path containing a newline")
	}
	if added {
		t.Error("added = true on a refusal")
	}
	if !strings.Contains(err.Error(), "newline") {
		t.Errorf("error = %v, want it to name the newline", err)
	}
	if isTrusted("/tmp/evil") {
		t.Error("the second line was granted")
	}
	// A refusal writes nothing at all, so there is no registry to inspect.
	if _, err := os.Stat(registry); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("stat %s: %v, want it never created", registry, err)
	}
}
