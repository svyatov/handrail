package harness

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// ManagedDir is where Claude Code reads its managed settings files. A variable
// so the tests can point it into their sandbox: the real one is the machine's.
var ManagedDir = func() string {
	if runtime.GOOS == "darwin" {
		return "/Library/Application Support/ClaudeCode"
	}

	return "/etc/claude-code"
}()

// Bypass is what a harness's settings do to handrail's installed hooks for one
// Project root: the reasons they do not run at all, and the files holding a
// running hook that can answer a handrail ask without the human.
type Bypass struct {
	Disabled  []string
	Approvers []string
}

// Bypass reads what keeps handrail's hooks from running for the project at
// root, from the files on this machine alone: a policy the harness fetches
// when it starts is not here to read.
func (a Adapter) Bypass(root string) (Bypass, error) { return a.bypass(a, root) }

// layer is one settings file, highest precedence first in a list of them.
type layer struct {
	path     string
	settings map[string]any
}

// claudeBypass reads disableAllHooks after Claude Code's settings precedence,
// allowManagedHooksOnly from managed settings alone, and every PermissionRequest
// hook those two leave running.
func claudeBypass(a Adapter, root string) (Bypass, error) {
	var out Bypass

	layers, managed, err := claudeLayers(a, root)
	if err != nil {
		return out, err
	}
	// Hooks merge across the files rather than replacing each other, and only
	// managed settings can switch off a managed hook. The managed files merge
	// into one policy, so switched off in one, it is off in all of them.
	running := layers

	if i, on := first(layers, "disableAllHooks"); on {
		out.Disabled = append(out.Disabled, "hooks are disabled: disableAllHooks is true in "+layers[i].path)

		running = layers[:managed]
		if i < managed {
			running = nil
		}
	}

	if i, on := first(layers[:managed], "allowManagedHooksOnly"); on {
		out.Disabled = append(out.Disabled, "only managed hooks run: allowManagedHooksOnly is true in "+layers[i].path)
		running = running[:min(len(running), managed)]
	}

	for _, l := range running {
		if approves(l.settings) {
			out.Approvers = append(out.Approvers, l.path)
		}
	}

	return out, nil
}

// claudeLayers reads Claude Code's settings files, highest precedence first:
// managed, then local, then project, then user. It also counts the managed
// ones, which lead.
func claudeLayers(adapter Adapter, root string) ([]layer, int, error) {
	paths := managedFiles()

	managed := len(paths)
	if root != "" {
		paths = append(paths,
			filepath.Join(root, ".claude", "settings.local.json"), filepath.Join(root, ".claude", "settings.json"))
	}

	layers := make([]layer, 0, len(paths)+1)

	for _, path := range append(paths, adapter.ConfigPath()) {
		cfg, err := readSettings(path)
		if err != nil {
			return nil, 0, err
		}

		layers = append(layers, layer{path: path, settings: cfg.settings})
	}

	return layers, managed, nil
}

// approves reports whether settings hold a PermissionRequest hook, which can
// answer an ask in the human's place.
func approves(settings map[string]any) bool {
	hooks, _ := settings["hooks"].(map[string]any)
	groups, _ := hooks["PermissionRequest"].([]any)

	return slices.ContainsFunc(groups, func(g any) bool {
		group, _ := g.(map[string]any)
		inner, _ := group["hooks"].([]any)

		return len(inner) > 0
	})
}

// codexBypass reads Codex CLI's hooks feature: the project's config.toml, then
// the user's, the first that sets it winning.
func codexBypass(a Adapter, root string) (Bypass, error) {
	var out Bypass

	paths := []string{a.path("config.toml")}
	if root != "" {
		paths = slices.Insert(paths, 0, filepath.Join(root, ".codex", "config.toml"))
	}

	for _, path := range paths {
		data, err := os.ReadFile(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return out, err
		}

		if f := hooksFeature(string(data)); f.set {
			if !f.on {
				out.Disabled = append(out.Disabled, "hooks are disabled: [features] "+f.key+" is false in "+path)
			}

			return out, nil
		}
	}

	return out, nil
}

// feature is one boolean feature flag as a config.toml sets it.
type feature struct {
	key     string
	on, set bool
}

// hooksFeature reads the hooks feature, or its deprecated codex_hooks name,
// from a config.toml. It reads that one key and no more TOML: table headers,
// and a key under [features] or dotted under features.
// ponytail: a line inside a multi-line string reads as a line; parse TOML
// fully if a config ever hides the switch that way.
func hooksFeature(toml string) feature {
	var (
		hooks feature
		table string
	)

	bare := strings.NewReplacer(" ", "", "\t", "", `"`, "", "'", "")

	for line := range strings.Lines(toml) {
		line, _, _ = strings.Cut(line, "#")
		line = strings.TrimSpace(line)

		if strings.HasPrefix(line, "[") {
			table = bare.Replace(strings.Trim(line, "[]"))

			continue
		}

		name, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}

		name = bare.Replace(name)
		if table != "" {
			name = table + "." + name
		}

		if key, ok := strings.CutPrefix(name, "features."); ok && (key == "hooks" || key == "codex_hooks") {
			hooks = feature{key: key, on: strings.TrimSpace(value) == "true", set: true}
		}
	}

	return hooks
}

// managedFiles are Claude Code's managed settings files, highest precedence
// first: it merges managed-settings.json, then each visible *.json drop-in in
// alphabetical order, so the last drop-in wins.
func managedFiles() []string {
	dropins := filepath.Join(ManagedDir, "managed-settings.d")
	entries, _ := os.ReadDir(dropins) // sorted by name; none when it is absent

	var files []string

	for _, e := range slices.Backward(entries) {
		name := e.Name()
		if !e.IsDir() && !strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".json") {
			files = append(files, filepath.Join(dropins, name))
		}
	}

	return append(files, filepath.Join(ManagedDir, "managed-settings.json"))
}

// first is the boolean value key takes after precedence, with the layer it
// came from: the first layer that sets it wins.
func first(layers []layer, key string) (int, bool) {
	for i, l := range layers {
		if v, ok := l.settings[key].(bool); ok {
			return i, v
		}
	}

	return -1, false
}
