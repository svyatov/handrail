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
	translatable := []rule.Condition{{Terms: []rule.Term{{Field: "command", Op: "starts_with", Value: "git push --force"}}}}
	claude, _ := Lookup("claude")

	cases := []struct {
		name       string
		adapter    Adapter
		conditions []rule.Condition
	}{
		// Every harness sync writes for has a native mechanism today, so this is
		// the shape of the next one added before its translation is written.
		{"a harness with no native mechanism", Adapter{Name: "gemini"}, translatable},
		// And this is the shape of one added with half of it: a mechanism named,
		// with nothing to spell an entry with.
		{
			"a harness naming a mechanism it cannot spell entries for",
			Adapter{Name: "gemini", promotion: promotion{mechanism: "policy.deny", file: "policy.json"}},
			translatable,
		},
		// A condition carrying no terms spells no entry, and an advice with no
		// entry is a paste target with nothing to paste. The parser never builds
		// one, so this is the shape a future caller could hand Advise directly.
		{"a condition carrying no terms", claude, []rule.Condition{{}}},
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

// Every harness handrail syncs for lets a variable relocate its config
// directory, and config written anywhere else is config it never reads. The
// field is opt-in per adapter, so an adapter added without one fails silently:
// detection and sync go to ~/<dir> while the harness reads elsewhere. Only a
// walk of the table can catch that, which the compiled binary cannot do.
func TestEveryAdapterFollowsItsRelocationVariable(t *testing.T) {
	for _, a := range Adapters() {
		t.Run(a.Name, func(t *testing.T) {
			if a.homeEnv == "" {
				t.Fatalf("%s has no homeEnv, so its relocation variable is ignored", a.Name)
			}
			dir := t.TempDir()
			t.Setenv(a.homeEnv, dir)

			if got, want := a.ConfigPath(), filepath.Join(dir, a.file); got != want {
				t.Errorf("ConfigPath() = %q, want %q", got, want)
			}
			if !a.Installed() {
				t.Error("Installed() = false for the directory the variable names")
			}
		})
	}
}

// A Promotion is four declarations that only mean something together: the
// mechanism names it, the file says where its entries are pasted, the scope
// says how far one reaches once pasted, and entries spells them. Part of one is
// a harness that advertises a mechanism it cannot write an entry for, writes
// entries with nowhere to put them, or leaves handrail to guess how far they
// carry. Declaring none is how a harness says it has no Promotion, so only the
// mixture is wrong, and only a walk of the table can see it.
func TestEveryAdapterDeclaresItsPromotionWhole(t *testing.T) {
	for _, a := range Adapters() {
		t.Run(a.Name, func(t *testing.T) {
			declared := 0
			for _, part := range []bool{
				a.promotion.mechanism != "",
				a.promotion.file != "",
				a.promotion.scope != "",
				a.promotion.entries != nil,
			} {
				if part {
					declared++
				}
			}
			if declared != 0 && declared != 4 {
				t.Errorf("%s declares %d of the 4 Promotion parts: %+v", a.Name, declared, a.promotion)
			}
		})
	}
}

// Wholeness is not enough for scope, because Advise compares it against the one
// scope handrail knows and reads anything else as narrower. A misspelling is
// therefore a declaration the table accepts and the Advisor answers backwards:
// the entry still reaches every repo, and the user is no longer told so before
// pasting it. On the harnesses sync writes for, advise.txtar and
// advise_json.txtar catch that; on the next harness added, nothing does until
// someone writes its cases, which is the gap only a walk of the table closes.
//
// Comparing against scopeUser rather than a set is what having one scope means.
// A harness whose mechanism reaches less has to add the constant and widen this
// test, which is the point where that harness's reach gets thought about.
func TestEveryPromotionDeclaresAScopeHandrailKnows(t *testing.T) {
	for _, a := range Adapters() {
		t.Run(a.Name, func(t *testing.T) {
			if a.promotion.scope != "" && a.promotion.scope != scopeUser {
				t.Errorf("%s declares scope %q, which is not a scope handrail knows: Advise reads it as narrower than %q and drops the widening warning",
					a.Name, a.promotion.scope, scopeUser)
			}
		})
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
