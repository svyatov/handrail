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

// Bypass reads what keeps handrail's hooks from running for a session started
// in cwd, inside the project at root, from the files on this machine alone: a
// policy the harness fetches when it starts is not here to read.
func (a Adapter) Bypass(root, cwd string) (Bypass, error) { return a.bypass(a, root, cwd) }

// layer is one settings file, highest precedence first in a list of them.
type layer struct {
	path     string
	settings map[string]any
}

// claudeBypass reads disableAllHooks after Claude Code's settings precedence,
// allowManagedHooksOnly from managed settings alone, and every PermissionRequest
// hook those two leave running.
func claudeBypass(a Adapter, root, cwd string) (Bypass, error) {
	var out Bypass

	layers, managed, err := claudeLayers(a, root, cwd)
	if err != nil {
		return out, err
	}
	// Hooks merge across the files rather than replacing each other, and only
	// managed settings can switch off a managed hook. The managed files merge
	// into one policy, so switched off in one, it is off in all of them.
	running := layers

	if i, on := winning(layers, "disableAllHooks"); on {
		out.Disabled = append(out.Disabled, "hooks are disabled: disableAllHooks is true in "+layers[i].path)

		running = layers[:managed]
		if i < managed {
			running = nil
		}
	}

	if i, on := winning(layers[:managed], "allowManagedHooksOnly"); on {
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
// ones, which lead. The local file sits at the repository root, below it the
// one an older Claude Code kept in the starting directory; the project file
// is the starting directory's alone.
func claudeLayers(adapter Adapter, root, cwd string) ([]layer, int, error) {
	paths, err := managedFiles()
	if err != nil {
		return nil, 0, err
	}

	managed := len(paths)
	paths = append(paths,
		filepath.Join(root, ".claude", "settings.local.json"), filepath.Join(cwd, ".claude", "settings.local.json"),
		filepath.Join(cwd, ".claude", "settings.json"))

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
func codexBypass(a Adapter, root, _ string) (Bypass, error) {
	var out Bypass

	for _, path := range []string{filepath.Join(root, ".codex", "config.toml"), a.path("config.toml")} {
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
// and a key under [features], dotted under features, or in an inline features
// table.
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

		for _, kv := range inline(name, value, bare) {
			if key, ok := strings.CutPrefix(kv[0], "features."); ok && (key == "hooks" || key == "codex_hooks") {
				hooks = feature{key: key, on: strings.TrimSpace(kv[1]) == "true", set: true}
			}
		}
	}

	return hooks
}

// inline spreads an inline table's entries into dotted keys under name, and
// leaves any other assignment as it is.
func inline(name, value string, bare *strings.Replacer) [][2]string {
	body, ok := strings.CutPrefix(strings.TrimSpace(value), "{")
	if !ok {
		return [][2]string{{name, value}}
	}

	var out [][2]string

	for entry := range strings.SplitSeq(strings.TrimSuffix(strings.TrimSpace(body), "}"), ",") {
		key, v, _ := strings.Cut(entry, "=")
		out = append(out, [2]string{name + "." + bare.Replace(key), v})
	}

	return out
}

// managedFiles are Claude Code's managed settings files, highest precedence
// first: it merges managed-settings.json, then each visible *.json drop-in in
// alphabetical order, so the last drop-in wins.
func managedFiles() ([]string, error) {
	dropins := filepath.Join(ManagedDir, "managed-settings.d")

	entries, err := os.ReadDir(dropins) // sorted by name
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}

	var files []string

	for _, e := range slices.Backward(entries) {
		name := e.Name()
		if !e.IsDir() && !strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".json") {
			files = append(files, filepath.Join(dropins, name))
		}
	}

	return append(files, filepath.Join(ManagedDir, "managed-settings.json")), nil
}

// winning is the index of the layer whose value for key holds after
// precedence, the first that sets it, and that value. The index is -1 where no
// layer sets it.
func winning(layers []layer, key string) (int, bool) {
	for i, l := range layers {
		if v, ok := l.settings[key].(bool); ok {
			return i, v
		}
	}

	return -1, false
}
