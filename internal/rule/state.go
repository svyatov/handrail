package rule

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// The state registry and the Enforcement state it holds. The registry is
// handrail's own record of what the user granted or set, one file per record
// in the XDG state dir, one Project root per line: Trust, and the Enforcement
// state beside it.

var (
	errNewlinePath = errors.New("cannot record a path containing a newline")
	errNoStateDir  = errors.New("no state directory: set HOME or XDG_STATE_HOME")
)

// The registry is the user's alone, so nobody else may read or list it.
const (
	registryDirMode = 0o700
	registryMode    = 0o600
)

// registryFile is the path of one registry file, "" when there is no state dir.
func registryFile(name string) string {
	dir := xdgSubdir("XDG_STATE_HOME", filepath.Join(".local", "state"))
	if dir == "" {
		return ""
	}

	return filepath.Join(dir, name)
}

// registry returns one registry file's lines, none when it cannot be read.
func registry(name string) []string {
	file := registryFile(name)
	if file == "" {
		return nil
	}

	data, err := os.ReadFile(file)
	if err != nil {
		return nil
	}

	return strings.FieldsFunc(string(data), func(r rune) bool { return r == '\n' })
}

// writeRegistry replaces one registry file's lines. The new file is written
// beside the old and renamed over it, so a reader sees one or the other.
func writeRegistry(name string, lines []string) error {
	// One path per line, so a newline in a path would write a second line and
	// record a path nobody asked for. A directory may legally hold one.
	for _, line := range lines {
		if strings.Contains(line, "\n") {
			return fmt.Errorf("%w: %q", errNewlinePath, line)
		}
	}

	file := registryFile(name)
	if file == "" {
		return errNoStateDir
	}

	err := os.MkdirAll(filepath.Dir(file), registryDirMode)
	if err != nil {
		return err
	}

	out, err := os.CreateTemp(filepath.Dir(file), name+".*")
	if err != nil {
		return err
	}

	err = out.Chmod(registryMode)
	if err == nil {
		_, err = fmt.Fprint(out, strings.Join(lines, "\n")+"\n")
	}

	if closeErr := out.Close(); err == nil {
		err = closeErr
	}

	if err == nil {
		err = os.Rename(out.Name(), file)
	}

	if err != nil {
		_ = os.Remove(out.Name())
	}

	return err
}

// State is the Enforcement state: whether handrail enforces at all. The zero
// value enforces.
type State int

// The three Enforcement states, strongest first.
const (
	StateEnforce State = iota
	StateTrial
	StateOff
)

// String is the state as mode takes and prints it.
func (s State) String() string { return [...]string{"enforce", "trial", "off"}[s] }

// ParseState reads a state as mode takes it.
func ParseState(name string) (State, bool) {
	for s := StateEnforce; s <= StateOff; s++ {
		if s.String() == name {
			return s, true
		}
	}

	return StateEnforce, false
}

// stateFile is the registry file of the Enforcement state. Each line is a
// state, a space, and the Project root it holds for; the machine-wide state is
// a state alone.
const stateFile = "enforcement"

// Scope is where the Enforcement state that applies was set.
type Scope int

// The three places a state can come from.
const (
	ScopeDefault Scope = iota // set nowhere, so enforce
	ScopeProject
	ScopeMachine
)

// StateOf is the Enforcement state for root and where it was set: its own,
// else the machine-wide one, else enforce. A root of "" asks for the
// machine-wide state alone.
func StateOf(root string) (State, Scope) {
	machine, scope := StateEnforce, ScopeDefault

	for _, line := range registry(stateFile) {
		name, path, _ := strings.Cut(line, " ")

		state, ok := ParseState(name)
		switch {
		case !ok:
		case path == "":
			machine, scope = state, ScopeMachine
		case path == root:
			return state, ScopeProject
		}
	}

	return machine, scope
}

// StateNotice is what a project that is not enforcing owes the user at every
// SessionStart, and "" when it enforces. The state has no expiry, so this is
// what stands between a suspension and a project that looks guarded.
func (rs *Ruleset) StateNotice() string {
	if rs.State == StateEnforce {
		return ""
	}

	what := "every rule is on trial and delivers nothing"
	if rs.State == StateOff {
		what = "no rule is evaluated"
	}

	scope, resume := fmt.Sprintf("for this project (%s)", rs.Root), "handrail mode enforce"
	if rs.StateScope == ScopeMachine {
		scope, resume = "machine-wide", "handrail mode enforce --global"
	}

	return fmt.Sprintf("handrail: the enforcement state is %s %s: %s; run %s in your own terminal to resume",
		rs.State, scope, what, resume)
}

// SetState records state for root, or machine-wide for a root of "",
// replacing whatever that scope held.
func SetState(root string, state State) error {
	lines := slices.DeleteFunc(registry(stateFile), func(line string) bool {
		_, path, _ := strings.Cut(line, " ")

		return path == root
	})

	line := state.String()
	if root != "" {
		line += " " + root
	}

	return writeRegistry(stateFile, append(lines, line))
}
