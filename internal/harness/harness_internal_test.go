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

// A harness that has never run leaves no directory, and a machine with no home
// directory leaves nowhere to look for one. sync reports that as "no harness
// found" long before it gets here, so these are the guards behind that.
func TestAdapterWithoutAHomeDirectory(t *testing.T) {
	t.Setenv("HOME", "")

	adapter := Adapter{
		Name: "nowhere", quirks: nil, title: "", dir: ".nowhere", homeEnv: "", file: "settings.json", sessionEnv: "",
		aliases: nil, agentTypeKey: "", agentPromptKey: "", events: nil, patchInShell: false, bypass: nil,
	}

	if got := adapter.ConfigPath(); got != "" {
		t.Errorf("ConfigPath() = %q, want empty", got)
	}

	if adapter.Installed() {
		t.Error("Installed() = true, want false")
	}

	_, err := adapter.Install("/usr/local/bin/handrail")
	if err == nil {
		t.Error("Install() succeeded with nowhere to write")
	}
}

// Every harness handrail syncs for lets a variable relocate its config
// directory, and config written anywhere else is config it never reads. The
// field is opt-in per adapter, so an adapter added without one fails silently:
// detection and sync go to ~/<dir> while the harness reads elsewhere. Only a
// walk of the table can catch that, which the compiled binary cannot do.
func TestEveryAdapterFollowsItsRelocationVariable(t *testing.T) {
	for _, adapter := range Adapters() {
		t.Run(adapter.Name, func(t *testing.T) {
			if adapter.homeEnv == "" {
				t.Fatalf("%s has no homeEnv, so its relocation variable is ignored", adapter.Name)
			}

			dir := t.TempDir()
			t.Setenv(adapter.homeEnv, dir)

			if got, want := adapter.ConfigPath(), filepath.Join(dir, adapter.file); got != want {
				t.Errorf("ConfigPath() = %q, want %q", got, want)
			}

			if !adapter.Installed() {
				t.Error("Installed() = false for the directory the variable names")
			}
		})
	}
}

func TestWriteReportsAnUnusableParent(t *testing.T) {
	t.Parallel()

	file := filepath.Join(t.TempDir(), "settings.json")

	err := os.WriteFile(file, []byte("{}\n"), 0o600)
	if err != nil {
		t.Fatal(err)
	}
	// The parent of the target is a regular file, so the directory it names
	// cannot be created and never could be.
	err = write(filepath.Join(file, "settings.json"), []byte("{}\n"))
	if err == nil {
		t.Error("write() succeeded through a regular file")
	}
}

func TestShellQuote(t *testing.T) {
	t.Parallel()

	cases := []struct{ name, in, want string }{
		{"an ordinary path is its own first word", "/usr/local/bin/handrail", "/usr/local/bin/handrail"},
		{"a space is ordinary on macOS", "/Users/a b/bin/handrail", "'/Users/a b/bin/handrail'"},
		{"a quote closes the quoting and reopens it", "/tmp/o'brien/handrail", `'/tmp/o'\''brien/handrail'`},
		{"an empty word would otherwise disappear", "", "''"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			if got := shellQuote(c.in); got != c.want {
				t.Errorf("shellQuote(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// Both harnesses have all eight events today, so a harness lacking one exists
// only here: a rule on an event it lacks degrades to skip, reported rather
// than delivered as whatever its missing row would read as.
func TestAMissingEventDegradesToSkip(t *testing.T) {
	t.Parallel()

	adapter := Adapter{
		Name: "partial", quirks: nil, title: "Partial", dir: "", homeEnv: "", file: "", sessionEnv: "",
		aliases: nil, agentTypeKey: "", agentPromptKey: "", patchInShell: false, bypass: nil,
		events: []eventCaps{{name: "PreToolUse", deny: permissionDeny, inject: true, ask: false, silent: false}},
	}
	notDone := &rule.Rule{
		Name: "not-done", Path: "", Tier: "", ShadowedBy: nil, Replaces: nil, DroppedBy: nil, DemotedFrom: "",
		Event: "Stop", Kind: "", Action: rule.Block, Enabled: false, AgentOnly: false, LostAgentOnly: false,
		Conditions: nil, Examples: nil, Message: "", Trial: false,
	}

	if got := adapter.Action(notDone); got != rule.Allow {
		t.Errorf("Action() = %s, want allow", got)
	}

	want := "block degraded to skip for not-done: Partial has no Stop event"
	if got := adapter.Report([]*rule.Rule{notDone}); len(got) != 1 || got[0] != want {
		t.Errorf("Report() = %q, want [%q]", got, want)
	}
}
