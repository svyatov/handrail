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

// ConfigPath is the one file sync writes: user-level, never project-level.
func (a Adapter) ConfigPath() string {
	dir := a.userDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, a.file)
}

// Install puts exactly one hook entry per canonical event into the harness's
// user-level config, invoking bin. It reports how many entries it wrote and
// whether the file needed changing. Every other key in the file is left exactly
// as it was: these are the user's settings, and handrail is one tenant among
// several.
func (a Adapter) Install(bin string) (entries int, changed bool, err error) {
	path := a.ConfigPath()
	if path == "" {
		return 0, false, errors.New("no home directory: set HOME")
	}
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return 0, false, err
	}
	settings := map[string]any{}
	if len(old) > 0 {
		if err := json.Unmarshal(old, &settings); err != nil {
			return 0, false, fmt.Errorf("%s: %w", path, err)
		}
	}

	hooks, _ := settings["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
	}
	events := rule.Events()
	for _, event := range events {
		groups := a.prune(hooks[event], event)
		// No matcher: the matcher field is optional on every event that has one,
		// and handrail classifies the tool itself, so one shape fits all six.
		hooks[event] = append(groups, map[string]any{
			"hooks": []any{map[string]any{
				"type":    "command",
				"command": shellQuote(bin) + " hook " + a.Name + " " + event,
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
		return len(events), false, nil
	}
	return len(events), true, write(path, next)
}

// ours reports whether command is an entry a previous sync wrote for event. The
// binary's directory is not part of the test, so a moved binary replaces its old
// entry instead of stacking a second one; its name is, so nothing of the user's
// own can be deleted from their settings by resembling handrail's command line.
func (a Adapter) ours(command, event string) bool {
	tail, found := strings.CutSuffix(command, " hook "+a.Name+" "+event)
	if !found {
		return false
	}
	return strings.HasPrefix(filepath.Base(strings.Trim(tail, "'")), "handrail")
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
			hook, ok := h.(map[string]any)
			if ok {
				if cmd, _ := hook["command"].(string); a.ours(cmd, event) {
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

// Degradation is one rule this harness cannot enforce at full strength.
type Degradation struct {
	Rule, From, To, Reason string
}

func (d Degradation) String() string {
	return fmt.Sprintf("%s degraded to %s for %s: %s", d.From, d.To, d.Rule, d.Reason)
}

// Degradations reports where the harness weakens a rule's action. Sync and
// doctor print this; the runtime hot path stays quiet about it.
func (a Adapter) Degradations(rules []*rule.Rule) []Degradation {
	var out []Degradation
	for _, r := range rules {
		if !r.Enabled || r.ShadowedBy != "" {
			continue
		}
		if r.Action == "block" && !a.canBlock(r.Event) {
			out = append(out, Degradation{
				Rule: r.Name, From: "block", To: "warn", Reason: a.blockReason(r.Event),
			})
		}
		// The message still reaches the user, on stderr, but the agent is gone by
		// then: an injected warning it can act on is what was lost.
		if !canInject(r.Event) {
			out = append(out, Degradation{
				Rule: r.Name, From: "warn", To: "notice",
				Reason: a.title + " discards hook output on " + r.Event + ", so the message goes to the user, not the agent",
			})
		}
	}
	return out
}
