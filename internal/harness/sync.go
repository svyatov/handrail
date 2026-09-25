package harness

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/svyatov/handrail/internal/rule"
)

// userDir is the harness's user-level directory, which is also how sync detects
// it: the CLI creates the directory on first run, so its absence means no
// install worth writing config for. A harness that lets a variable relocate
// that directory is followed there, since config written anywhere else is
// config it will never read.
func (a Adapter) userDir() string {
	if a.homeEnv != "" {
		if dir := os.Getenv(a.homeEnv); dir != "" {
			return dir
		}
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}

	return filepath.Join(home, a.dir)
}

// Installed reports whether the harness has run on this machine.
func (a Adapter) Installed() bool {
	dir := a.userDir()
	if dir == "" {
		return false
	}

	fi, err := os.Stat(dir)

	return err == nil && fi.IsDir()
}

// path locates a file inside the harness's user-level directory. A machine with
// no home directory has no such directory, and no path either: a relative one
// would point at whatever the caller's cwd happens to be. A file nothing named
// is the same answer, since joining it yields the directory: a paste target
// that is a directory is no more usable than one that is a bare name.
func (a Adapter) path(file string) string {
	dir := a.userDir()
	if dir == "" || file == "" {
		return ""
	}

	return filepath.Join(dir, file)
}

// ConfigPath is the one file sync writes: user-level, never project-level.
func (a Adapter) ConfigPath() string { return a.path(a.file) }

// Install puts exactly one hook entry per event of its table into the harness's
// user-level config, invoking bin. It reports how many entries it wrote and
// whether the file needed changing. Every other key in the file is left exactly
// as it was: these are the user's settings, and handrail is one tenant among
// several.
func (a Adapter) Install(bin string) (entries int, changed bool, err error) {
	path := a.ConfigPath()

	old, settings, err := a.read()
	if err != nil {
		return 0, false, err
	}

	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}

	for _, c := range a.events {
		event := c.name
		groups := a.prune(hooks[event], event)
		// No matcher: the matcher field is optional on every event that has one,
		// and handrail classifies the tool itself, so one shape fits all eight.
		hooks[event] = append(groups, map[string]any{
			"hooks": []any{map[string]any{
				"type":    "command",
				"command": a.command(bin, event),
			}},
		})
	}

	settings["hooks"] = hooks

	// Map keys marshal in sorted order, so the same ruleset yields the same
	// bytes: idempotence is what keeps a hash-trusting harness from re-prompting.
	next, err := json.MarshalIndent(settings, "", "  ")
	if err != nil {
		return 0, false, err
	}

	next = append(next, '\n')
	if string(next) == string(old) {
		return len(a.events), false, nil
	}

	return len(a.events), true, write(path, next)
}

// read parses the harness's config, returning the bytes it came from so Install
// can tell an unchanged file from a rewritten one. A file that is not there yet
// is an empty config, not a failure.
func (a Adapter) read() (raw []byte, settings map[string]any, err error) {
	path := a.ConfigPath()
	if path == "" {
		return nil, nil, errors.New("no home directory: set HOME")
	}

	raw, err = os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, nil, err
	}

	settings = map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &settings); err != nil {
			return nil, nil, fmt.Errorf("%s: %w", path, err)
		}
	}

	return raw, settings, nil
}

// command is the hook entry sync writes for one event, and therefore the one
// doctor compares against what it finds installed.
func (a Adapter) command(bin, event string) string {
	return shellQuote(bin) + " hook " + a.Name + " " + event
}

// entryBinary reads the binary invoked by a command line, when that command
// line is one a previous sync wrote for event. The binary's directory is not
// part of the test, so a moved binary replaces its old entry instead of stacking
// a second one; its name is, so nothing of the user's own can be deleted from
// their settings by resembling handrail's command line.
func (a Adapter) entryBinary(command, event string) (string, bool) {
	tail, found := strings.CutSuffix(command, " hook "+a.Name+" "+event)
	if !found {
		return "", false
	}

	bin := shellUnquote(tail)
	if !strings.HasPrefix(filepath.Base(bin), "handrail") {
		return "", false
	}

	return bin, true
}

// Entries reports the binary each event's installed hook entry
// invokes, in the order sync wrote them. An event handrail has no entry for
// comes back empty, which is what doctor calls a missing entry.
func (a Adapter) Entries() ([]Entry, error) {
	_, settings, err := a.read()
	if err != nil {
		return nil, err
	}

	hooks, _ := settings["hooks"].(map[string]any)

	out := make([]Entry, 0, len(a.events))
	for _, c := range a.events {
		event := c.name
		e := Entry{Event: event}

		groups, _ := hooks[event].([]any)
		for _, g := range groups {
			group, _ := g.(map[string]any)

			inner, _ := group["hooks"].([]any)
			for _, h := range inner {
				hook, _ := h.(map[string]any)

				cmd, _ := hook["command"].(string)
				if bin, ok := a.entryBinary(cmd, event); ok {
					e.Binary = bin
				}
			}
		}

		out = append(out, e)
	}

	return out, nil
}

// Entry is one event's installed hook entry, as doctor found it.
type Entry struct {
	Event  string
	Binary string // the binary the entry invokes, empty when there is no entry
}

// prune drops the entries a previous sync wrote for event. An entry pointing at
// a binary that no longer exists is exactly the one that must go.
func (a Adapter) prune(groups any, event string) []any {
	list, _ := groups.([]any)

	kept := make([]any, 0, len(list))
	for _, g := range list {
		group, ok := g.(map[string]any)
		if !ok {
			kept = append(kept, g)

			continue
		}

		inner, ok := group["hooks"].([]any)
		if !ok {
			kept = append(kept, g)

			continue
		}

		keptInner := make([]any, 0, len(inner))
		for _, h := range inner {
			if hook, ok := h.(map[string]any); ok {
				cmd, _ := hook["command"].(string)
				if _, ours := a.entryBinary(cmd, event); ours {
					continue
				}
			}

			keptInner = append(keptInner, h)
		}
		// A group handrail emptied was handrail's own; one the user shares with
		// us keeps its remaining hooks.
		if len(keptInner) == 0 {
			continue
		}

		group["hooks"] = keptInner
		kept = append(kept, group)
	}

	return kept
}

// write replaces path atomically, so an interrupted sync cannot leave the user
// with half a settings file.
func write(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	// Replacing the file must not silently change who can read it: handrail is a
	// guest in this file, so an existing mode is the user's decision to keep.
	mode := os.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}

	tmp, err := os.CreateTemp(filepath.Dir(path), ".handrail-settings-*")
	if err != nil {
		return err
	}

	defer func() { _ = os.Remove(tmp.Name()) }()

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()

		return err
	}

	if err := tmp.Close(); err != nil {
		return err
	}

	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}

	return os.Rename(tmp.Name(), path)
}

// shellQuote makes s safe as the first word of a shell command line, which is
// how a harness runs a hook entry. A home directory with a space in it is
// ordinary on macOS.
func shellQuote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\n\"'\\$`&;|<>()*?[]{}~#!") {
		return s
	}

	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// shellUnquote undoes shellQuote, so doctor can stat the binary an entry names.
func shellUnquote(s string) string {
	if len(s) < 2 || !strings.HasPrefix(s, "'") || !strings.HasSuffix(s, "'") {
		return s
	}

	return strings.ReplaceAll(s[1:len(s)-1], `'\''`, "'")
}

// Action is the action the harness delivers for r: the rule's own, or the
// nearest one it can deliver where it cannot deliver that.
func (a Adapter) Action(r *rule.Rule) rule.Outcome { return a.degrade(r.Event, r.Action) }

// Delivered is the Outcome the harness delivers for the matched rules: the
// strongest action it delivers among them.
func (a Adapter) Delivered(matched []rule.Match) rule.Outcome {
	var o rule.Outcome
	for _, m := range matched {
		o = max(o, a.Action(m.Rule))
	}

	return o
}

// Report is what the harness cannot do, for sync to print and doctor to
// reprint: each rule it weakens, then each event whose hook output its user
// never sees, then its quirks. Pass the Effective ruleset, since a rule that
// cannot fire cannot be degraded. The hot path stays quiet.
func (a Adapter) Report(rules []*rule.Rule) []string {
	var out []string

	for _, r := range rules {
		to := a.Action(r)
		if to == r.Action {
			continue
		}
		// A skipped rule delivers nothing, which a report says as skip.
		name := to.String()
		if to == rule.Allow {
			name = "skip"
		}

		out = append(out, fmt.Sprintf("%s degraded to %s for %s: %s", r.Action, name, r.Name, a.reason(r.Event, to)))
	}
	// An audience is not an action: the rule still enforces, and only the
	// human loses the line.
	for _, c := range a.events {
		if c.silent {
			out = append(out, c.name+" cannot tell the human: "+a.title+" shows the user no hook output there")
		}
	}

	return append(out, a.quirks...)
}
