package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/rogpeppe/go-internal/testscript"
)

func TestMain(m *testing.M) {
	testscript.Main(m, map[string]func(){
		"handrail": func() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr)) },
	})
}

func TestScripts(t *testing.T) {
	testscript.Run(t, testscript.Params{
		Dir:                 "testdata/script",
		RequireExplicitExec: true,
		Setup:               sandbox,
	})
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
