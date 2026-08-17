package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/testscript"
)

// main itself, not a closure around run: testscript hands the command its own
// os.Args, so main reads exactly what a real invocation reads, and the entry
// point gets covered by every script rather than by nothing.
func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){"handrail": main})
}

func TestScripts(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir:                 "testdata/script",
		RequireExplicitExec: true,
		Setup:               sandbox,
		Condition:           condition,
	})
}

// condition adds [root], which several scripts negate: chmod cannot deny root
// anything, so a permission-failure case has to skip when the tests run as one.
func condition(cond string) (bool, error) {
	if cond == "root" {
		return os.Geteuid() == 0, nil
	}
	return false, fmt.Errorf("unknown condition %q", cond)
}

// hookBudget is docs/spec.md section 10's acceptance bar. Cold means no daemon:
// the harness spawns a whole process before every tool call, so the budget
// covers process start too, which is why this execs a built binary rather than
// calling run directly.
const hookBudget = 50 * time.Millisecond

func TestHookColdStart(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and execs the binary")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "handrail")
	build := exec.Command("go", "build", "-ldflags", "-s -w", "-o", bin, ".")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building handrail: %v\n%s", err, out)
	}

	// A realistic worst case for a no-match call: every tier populated, so the
	// run walks three directories and parses every rule before deciding nothing
	// applies.
	home, repo := filepath.Join(dir, "home"), filepath.Join(dir, "repo")
	mkdirs(t, filepath.Join(repo, ".git"))
	for _, tier := range []struct {
		dir   string
		name  string
		rules int
	}{
		{filepath.Join(home, ".config", "handrail"), "global", 10},
		{filepath.Join(repo, ".handrail"), "shared", 10},
		{filepath.Join(repo, ".handrail", "local"), "personal", 5},
	} {
		mkdirs(t, tier.dir)
		for i := range tier.rules {
			writeFile(t, filepath.Join(tier.dir, fmt.Sprintf("%s-%d.md", tier.name, i)),
				"---\nevent: PreToolUse\nkind: shell\nconditions:\n  - field: command\n"+
					fmt.Sprintf("    matches: ^never-%d-\\w+$\n---\nA rule that does not match.\n", i))
		}
	}

	// The same redirection sandbox gives the scripts, for the same reason: this
	// execs a real binary, so a missing variable would land in the real user's
	// directories.
	env := append(os.Environ(),
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME="+filepath.Join(home, ".local", "state"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
	)
	run := func(args ...string) (time.Duration, string) {
		t.Helper()
		cmd := exec.Command(bin, args...)
		cmd.Dir, cmd.Env = repo, env
		cmd.Stdin = strings.NewReader(
			`{"hook_event_name":"PreToolUse","cwd":"` + repo + `","tool_name":"Bash","tool_input":{"command":"echo hi"}}`)
		start := time.Now()
		out, err := cmd.CombinedOutput()
		elapsed := time.Since(start)
		if err != nil {
			t.Fatalf("handrail %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return elapsed, string(out)
	}

	// Trust first, so the shared tier is read rather than skipped. It also puts
	// the binary and the rules in the page cache, which is the state a session's
	// second and every later tool call finds them in.
	run("trust")
	times := make([]time.Duration, 0, 21)
	for range cap(times) {
		elapsed, out := run("hook", "claude", "PreToolUse")
		if out != "" {
			t.Fatalf("no rule matches this payload, yet the hook said: %s", out)
		}
		times = append(times, elapsed)
	}
	slices.Sort(times)
	t.Logf("no-match hook over %d runs: median %v, best %v, worst %v",
		len(times), times[len(times)/2], times[0], times[len(times)-1])
	if median := times[len(times)/2]; median > hookBudget {
		t.Errorf("no-match hook took %v, over the %v budget (best %v, worst %v)",
			median, hookBudget, times[0], times[len(times)-1])
	}
}

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// sandbox redirects everything handrail reads from the environment into the
// script's own work directory, so a test can never see or touch the real user.
func sandbox(e *testscript.Env) error {
	home := filepath.Join(e.WorkDir, "home")
	if err := os.MkdirAll(home, 0o755); err != nil {
		return err
	}
	e.Setenv("HOME", home)
	e.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	e.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	e.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	e.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	return nil
}
