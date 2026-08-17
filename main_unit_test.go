package main

import (
	"errors"
	"io"
	"path/filepath"
	"testing"
)

// The CLI is tested through the compiled binary (ADR 0009); what is left here is
// what a process boundary cannot produce. testscript always hands the command a
// real file for stdout, so the paths that answer a write failure need the
// command funcs called directly.

// failWriter is what a closed pipe looks like from inside: every write fails,
// and nothing the caller does makes the next one succeed.
type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, errors.New("no space left on device") }

func TestCommandsReportAnUnwritableStdout(t *testing.T) {
	cases := []struct {
		name string
		run  func(stdout, stderr io.Writer) int
	}{
		{"check", func(stdout, stderr io.Writer) int { return cmdCheck(nil, stdout, stderr) }},
		{"check --json", func(stdout, stderr io.Writer) int { return cmdCheck([]string{"--json"}, stdout, stderr) }},
		{"test --json", func(stdout, stderr io.Writer) int {
			return cmdTest([]string{"PreToolUse", "--json"}, nil, stdout, stderr)
		}},
		{"sync", func(stdout, stderr io.Writer) int { return cmdSync(nil, stdout, stderr) }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sandboxHome(t)
			var stderr writerSpy
			if code := c.run(failWriter{}, &stderr); code != 1 {
				t.Errorf("exit code = %d, want 1", code)
			}
			if stderr.n == 0 {
				t.Error("the failure was not reported on stderr")
			}
		})
	}
}

// writerSpy counts what reached it, which is the whole assertion: the message
// itself is the error's, and the error is the operating system's.
type writerSpy struct{ n int }

func (w *writerSpy) Write(p []byte) (int, error) {
	w.n += len(p)
	return len(p), nil
}

// sandboxHome puts the test in a repo with one Project-personal rule and an
// installed harness, with every directory handrail reads from the environment
// redirected into a temporary one. Project-personal because it is the tier no
// trust grant gates: the ruleset has to be non-empty for anything to be printed.
func sandboxHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	home, repo := filepath.Join(dir, "home"), filepath.Join(dir, "repo")
	mkdirs(t,
		filepath.Join(home, ".claude"),
		filepath.Join(repo, ".git"),
		filepath.Join(repo, ".handrail", "local"),
	)
	writeFile(t, filepath.Join(repo, ".git", "HEAD"), "ref: refs/heads/main\n")
	writeFile(t, filepath.Join(repo, ".handrail", "local", "rule.md"),
		"---\nevent: PreToolUse\n---\nA rule that is always in the ruleset.\n")
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(home, ".local", "share"))
	t.Setenv("XDG_STATE_HOME", filepath.Join(home, ".local", "state"))
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, ".cache"))
	t.Chdir(repo)
	return repo
}
