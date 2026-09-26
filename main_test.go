package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/rogpeppe/go-internal/testscript"

	"github.com/svyatov/handrail/internal/harness"
)

// main itself, not a closure around run: testscript hands the command its own
// os.Args, so main reads exactly what a real invocation reads, and the entry
// point gets covered by every script rather than by nothing. Claude Code's
// managed settings live outside any home directory, so the sandbox moves them
// under its own.
func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){"handrail": func() {
		harness.ManagedDir = filepath.Join(os.Getenv("HOME"), "managed")

		main()
	}})
}

func TestScripts(t *testing.T) {
	t.Parallel()
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

	return false, fmt.Errorf("%w %q", errUnknownCondition, cond)
}

var errUnknownCondition = errors.New("unknown condition")

// hookBudget is docs/spec.md section 10's acceptance bar. Cold means no daemon:
// the harness spawns a whole process before every tool call, so the budget
// covers process start too, which is why this execs a built binary rather than
// calling run directly.
const hookBudget = 20 * time.Millisecond

// hookRuns is how many times each invocation runs, odd so the median is one run.
const hookRuns = 21

//nolint:paralleltest // a timing budget measured beside other tests measures them
func TestHookColdStart(t *testing.T) {
	if testing.Short() {
		t.Skip("builds and execs the binary")
	}

	dir := t.TempDir()
	home, repo := filepath.Join(dir, "home"), filepath.Join(dir, "repo")
	run := hookFixture(t, buildBinary(t, dir), home, repo)

	// Trust first, so the shared tier is read rather than skipped. It also puts
	// the binary and the rules in the page cache, which is the state a session's
	// second and every later tool call finds them in. The grant makes a match
	// append its Decision log line.
	run("", "trust")
	run("", "log", "on")

	for _, invocation := range []struct {
		name, event, fields string
		heard               func(out string) bool
	}{
		{
			"no-match hook", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"echo hi"}`,
			func(out string) bool { return out == "" },
		},
		{
			"matching hook", "PreToolUse", `"tool_name":"Bash","tool_input":{"command":"echo matched"}`,
			func(out string) bool { return strings.Contains(out, "handrail warn: matched") },
		},
		{
			// One failing Example per tier, so the notice shows each tier's ran.
			"SessionStart", "SessionStart", `"source":"startup"`,
			func(out string) bool {
				return strings.Contains(out, "3 rules fail their Examples: drifted-global (global), "+
					"drifted-shared (project-shared), drifted-personal (project-personal);")
			},
		},
	} {
		stdin := `{"hook_event_name":"` + invocation.event + `","session_id":"s1","cwd":"` + repo + `",` +
			invocation.fields + `}`
		times := make([]time.Duration, 0, hookRuns)

		for range hookRuns {
			elapsed, out := run(stdin, "hook", "claude", invocation.event)
			if !invocation.heard(out) {
				t.Fatalf("%s said the unexpected: %q", invocation.name, out)
			}

			times = append(times, elapsed)
		}

		assertMedian(t, invocation.name, times)
	}

	// The matching hook appended one line per run, and nothing else did. Each
	// names the Project-personal tier: an index tracking nothing under
	// .handrail/local/ leaves it the user's own.
	logged, err := os.ReadFile(filepath.Join(home, ".local", "state", "handrail", "log.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	lines, personal := strings.Count(string(logged), "\n"), strings.Count(string(logged), `"tier":"project-personal"`)
	if lines != hookRuns || personal != hookRuns {
		t.Errorf("the Decision log holds %d lines, %d of them Project-personal, want one per matching run, %d:\n%s",
			lines, personal, hookRuns, logged)
	}
}

// hookFixture builds the rule tiers and the git repository under home and repo,
// and returns a function that runs bin in repo with stdin, reporting how long
// the process took and what it printed.
func hookFixture(t *testing.T, bin, home, repo string) func(stdin string, args ...string) (time.Duration, string) {
	t.Helper()

	// The same redirection sandbox gives the scripts, for the same reason: this
	// execs a real binary, so a missing variable would land in the real user's
	// directories. An inherited GIT_DIR or GIT_INDEX_FILE would send git's
	// index elsewhere and leave the fixture's untracked.
	env := slices.DeleteFunc(os.Environ(), func(kv string) bool { return strings.HasPrefix(kv, "GIT_") })
	env = append(env,
		"HOME="+home,
		"XDG_CONFIG_HOME="+filepath.Join(home, ".config"),
		"XDG_DATA_HOME="+filepath.Join(home, ".local", "share"),
		"XDG_STATE_HOME="+filepath.Join(home, ".local", "state"),
		"XDG_CACHE_HOME="+filepath.Join(home, ".cache"),
		"GIT_CONFIG_NOSYSTEM=1",
	)
	populateTiers(t, home, repo)
	gitIndex(t, repo, env)

	return func(stdin string, args ...string) (time.Duration, string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), bin, args...)
		cmd.Dir, cmd.Env, cmd.Stdin = repo, env, strings.NewReader(stdin)
		start := time.Now()
		out, err := cmd.CombinedOutput()
		elapsed := time.Since(start)

		if err != nil {
			t.Fatalf("handrail %s: %v\n%s", strings.Join(args, " "), err, out)
		}

		return elapsed, string(out)
	}
}

// assertMedian fails when the median of times crosses hookBudget.
func assertMedian(t *testing.T, name string, times []time.Duration) {
	t.Helper()
	slices.Sort(times)
	median := times[len(times)/2]
	t.Logf("%s over %d runs: median %v, best %v, worst %v", name, len(times), median, times[0], times[len(times)-1])

	if median > hookBudget {
		t.Errorf("%s took %v, over the %v budget (best %v, worst %v)",
			name, median, hookBudget, times[0], times[len(times)-1])
	}
}

// buildBinary builds handrail into dir, stripped and without cgo, and returns
// its path.
func buildBinary(t *testing.T, dir string) string {
	t.Helper()

	bin := filepath.Join(dir, "handrail")
	build := exec.CommandContext(t.Context(), "go", "build", "-ldflags", "-s -w", "-o", bin, ".")

	build.Env = append(os.Environ(), "CGO_ENABLED=0")

	out, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf("building handrail: %v\n%s", err, out)
	}

	return bin
}

// populateTiers is a realistic worst case for a no-match call: every tier
// populated, so the run walks three directories and parses every rule before
// deciding nothing applies, and every rule carries Examples for SessionStart to
// run. One Project-personal warn rule matches "echo matched", and in each tier
// one rule's Example fails, so SessionStart shows it ran that tier's.
func populateTiers(t *testing.T, home, repo string) {
	t.Helper()

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
					fmt.Sprintf("    matches: ^never-%d-\\w+$\n", i)+
					fmt.Sprintf("examples:\n  match:\n    - command: never-%d-x\n", i)+
					"  no_match:\n    - command: echo hi\n---\nA rule that does not match.\n")
		}

		writeFile(t, filepath.Join(tier.dir, "drifted-"+tier.name+".md"),
			"---\nevent: PreToolUse\nkind: shell\nconditions:\n  - field: command\n    starts_with: never-drifted\n"+
				"examples:\n  match:\n    - command: echo drifted\n---\nA rule whose Example fails.\n")
	}

	writeFile(t, filepath.Join(repo, ".handrail", "local", "matched.md"),
		"---\nevent: PreToolUse\nkind: shell\nconditions:\n  - field: command\n    starts_with: echo matched\n"+
			"examples:\n  match:\n    - command: cd /tmp && echo matched\n  no_match:\n    - command: echo hi\n"+
			"---\nA rule that matches.\n")
}

// gitIndex makes repo a git repository whose index git itself wrote, holding
// the shared tier and a file beside it and leaving .handrail/local/ untracked,
// so the supply check reads a real index and keeps the tier Project-personal.
func gitIndex(t *testing.T, repo string, env []string) {
	t.Helper()
	writeFile(t, filepath.Join(repo, "README.md"), "A repository.\n")

	for _, args := range [][]string{
		{"-c", "init.defaultBranch=main", "init", "-q"},
		{"add", "README.md", ".handrail", ":!.handrail/local"},
	} {
		cmd := exec.CommandContext(t.Context(), "git", args...)
		cmd.Dir, cmd.Env = repo, env

		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
	}

	_, err := os.Stat(filepath.Join(repo, ".git", "index"))
	if err != nil {
		t.Fatalf("git wrote no index into the fixture: %v", err)
	}
}

func mkdirs(t *testing.T, dirs ...string) {
	t.Helper()

	for _, dir := range dirs {
		err := os.MkdirAll(dir, 0o755)
		if err != nil {
			t.Fatal(err)
		}
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()

	err := os.WriteFile(path, []byte(content), 0o644)
	if err != nil {
		t.Fatal(err)
	}
}

// sandbox redirects everything handrail reads from the environment into the
// script's own work directory, so a test can never see or touch the real user.
func sandbox(env *testscript.Env) error {
	home := filepath.Join(env.WorkDir, "home")

	err := os.MkdirAll(home, 0o755)
	if err != nil {
		return fmt.Errorf("creating the sandbox home: %w", err)
	}

	env.Setenv("HOME", home)
	env.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	env.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	env.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	env.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))

	return nil
}
