package rule

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
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

// registry returns one registry file's lines, none when there is no file yet.
// The error is a file that is there and cannot be read, which a reader treats
// as no lines and a writer must not.
func registry(name string) ([]string, error) {
	file := registryFile(name)
	if file == "" {
		return nil, nil
	}

	data, err := os.ReadFile(file)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	return strings.FieldsFunc(string(data), func(r rune) bool { return r == '\n' }), nil
}

// appendRegistry adds one line to a registry file. A write only ever appends,
// in one write to a file opened for appending, so writers running at once never
// lose each other's lines.
func appendRegistry(name, line string) error {
	// One path per line, so a newline in a path would write a second line and
	// record a path nobody asked for. A directory may legally hold one.
	if strings.Contains(line, "\n") {
		return fmt.Errorf("%w: %q", errNewlinePath, line)
	}

	file := registryFile(name)
	if file == "" {
		return errNoStateDir
	}

	err := os.MkdirAll(filepath.Dir(file), registryDirMode)
	if err != nil {
		return err
	}

	out, err := os.OpenFile(file, os.O_APPEND|os.O_CREATE|os.O_WRONLY, registryMode)
	if err != nil {
		return err
	}

	_, err = io.WriteString(out, line+"\n")
	if closeErr := out.Close(); err == nil {
		err = closeErr
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
// a state alone. Setting a state appends, so the last line for a scope holds.
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
// machine-wide state alone. A registry that cannot be read reads as enforce,
// the state that guards.
func StateOf(root string) (State, Scope) {
	machine, scope := StateEnforce, ScopeDefault
	project, found := StateEnforce, false
	lines, _ := registry(stateFile)

	for _, line := range lines {
		name, path, _ := strings.Cut(line, " ")

		state, ok := ParseState(name)
		switch {
		case !ok:
		case path == "":
			machine, scope = state, ScopeMachine
		case path == root:
			project, found = state, true
		}
	}

	if found {
		return project, ScopeProject
	}

	return machine, scope
}

// StateNotice is what a project that is not enforcing owes the user at every
// SessionStart, and "" when it enforces. The state has no expiry, so this is
// what stands between a forgotten off or trial and a project that looks
// guarded.
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
	line := state.String()
	if root != "" {
		line += " " + root
	}

	return appendRegistry(stateFile, line)
}
