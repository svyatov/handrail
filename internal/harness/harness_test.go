// White box, because write and shellQuote are unexported and the adapters this
// file constructs are ones no table lists: everything reachable through the CLI
// is tested through the compiled binary instead (ADR 0009).
package harness

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/svyatov/handrail/internal/rule"
)

func TestAdviseRefusesWhatItCannotSpellExactly(t *testing.T) {
	translatable := []rule.Condition{{Term: &rule.Term{Field: "command", Op: "starts_with", Value: "git push --force"}}}
	claude, _ := Lookup("claude")

	cases := []struct {
		name       string
		adapter    Adapter
		conditions []rule.Condition
	}{
		// Every harness sync writes for has a native mechanism today, so this is
		// the shape of the next one added before its translation is written.
		{"a harness with no native mechanism", Adapter{Name: "gemini"}, translatable},
		// A condition that names no term spells no entry, and an advice with no
		// entry is a paste target with nothing to paste.
		{"a condition carrying no term", claude, []rule.Condition{{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := &rule.Rule{
				Name: "no-force-push", Event: "PreToolUse", Kind: "shell",
				Action: "block", Enabled: true, Conditions: c.conditions,
			}
			if adv, ok := c.adapter.Advise(r); ok {
				t.Errorf("Advise translated it anyway: %+v", adv)
			}
		})
	}
}

// A harness that has never run leaves no directory, and a machine with no home
// directory leaves nowhere to look for one. sync reports that as "no harness
// found" long before it gets here, so these are the guards behind that.
func TestAdapterWithoutAHomeDirectory(t *testing.T) {
	t.Setenv("HOME", "")
	a := Adapter{Name: "nowhere", dir: ".nowhere", file: "settings.json"}

	if got := a.ConfigPath(); got != "" {
		t.Errorf("ConfigPath() = %q, want empty", got)
	}
	if a.Installed() {
		t.Error("Installed() = true, want false")
	}
	if _, _, err := a.Install("/usr/local/bin/handrail"); err == nil {
		t.Error("Install() succeeded with nowhere to write")
	}
}

func TestWriteReportsAnUnusableParent(t *testing.T) {
	file := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(file, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	// The parent of the target is a regular file, so the directory it names
	// cannot be created and never could be.
	if err := write(filepath.Join(file, "settings.json"), []byte("{}\n")); err == nil {
		t.Error("write() succeeded through a regular file")
	}
}

func TestShellQuote(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"an ordinary path is its own first word", "/usr/local/bin/handrail", "/usr/local/bin/handrail"},
		{"a space is ordinary on macOS", "/Users/a b/bin/handrail", "'/Users/a b/bin/handrail'"},
		{"a quote closes the quoting and reopens it", "/tmp/o'brien/handrail", `'/tmp/o'\''brien/handrail'`},
		{"an empty word would otherwise disappear", "", "''"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := shellQuote(c.in); got != c.want {
				t.Errorf("shellQuote(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
