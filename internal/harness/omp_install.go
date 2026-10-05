package harness

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

//go:embed omp-extension.js
var ompExtensionSource []byte

const ompOwnership = "// handrail-managed: omp-v1"

var (
	ompProfileName     = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,63}$`)
	ompReservedProfile = regexp.MustCompile(`(?i)^(CON|PRN|AUX|NUL|COM\d|LPT\d)(\..*)?$`)
	errOMPTarget       = errors.New("no omp installation target: check HOME and omp profile variables")
	errOMPConflict     = errors.New("extension conflict")
	errOMPIntegrity    = errors.New("managed extension is missing ownership, modified, or outdated; run handrail sync")
)

// ompProfile follows omp's profile validation, including platform-reserved names
// even on Unix. An invalid profile must never fall back to the default profile.
func ompProfile(value string) (string, bool) {
	name := strings.TrimSpace(value)
	if name == "" || name == "default" {
		return "", true
	}

	if !ompProfileName.MatchString(name) || strings.HasSuffix(name, ".") || ompReservedProfile.MatchString(name) {
		return "", false
	}

	return name, true
}

// ompUserDir follows the config-root rules, not omp's XDG data/state roots.
func ompUserDir() string {
	value, explicit := os.LookupEnv("OMP_PROFILE")
	if !explicit {
		value = os.Getenv("PI_PROFILE")
	}

	profile, valid := ompProfile(value)
	if !valid {
		return ""
	}

	home, homeErr := os.UserHomeDir()

	config := os.Getenv("PI_CONFIG_DIR")
	if config == "" {
		config = ".omp"
	}

	root := filepath.Join(home, config)

	override := ompAgentOverride(profile, root, explicit, homeErr == nil)
	if override != "" {
		resolved, err := filepath.Abs(override)
		if err != nil {
			return ""
		}

		return resolved
	}

	if homeErr != nil {
		return ""
	}

	if profile != "" {
		root = filepath.Join(root, "profiles", profile)
	}

	resolved, err := filepath.Abs(filepath.Join(root, "agent"))
	if err != nil {
		return ""
	}

	return resolved
}

// ompAgentOverride rejects overrides inherited from a legacy named profile
// when OMP_PROFILE explicitly selected the default profile instead.
func ompAgentOverride(profile, root string, explicit, hasHome bool) string {
	override := os.Getenv("PI_CODING_AGENT_DIR")
	if profile != "" || override == "" {
		return ""
	}

	legacy, valid := ompProfile(os.Getenv("PI_PROFILE"))

	inherited := explicit && valid && legacy != "" && hasHome &&
		override == filepath.Join(root, "profiles", legacy, "agent")
	if inherited {
		return ""
	}

	return override
}

// ompExtension's first two lines are the ownership and executable metadata.
// JSON quoting produces a JavaScript literal without involving a shell.
func ompExtension(bin string) []byte {
	const (
		prefix = ompOwnership + "\nconst handrailBinary = "
		suffix = ";\n"
	)

	literal, _ := json.Marshal(bin) //nolint:errchkjson // A string always has a JSON representation.
	out := make([]byte, 0, len(prefix)+len(literal)+len(suffix)+len(ompExtensionSource))
	out = append(out, prefix...)
	out = append(out, literal...)
	out = append(out, suffix...)

	return append(out, ompExtensionSource...)
}

func ompRecordedBinary(data []byte) (string, bool) {
	marker, rest, found := bytes.Cut(data, []byte{'\n'})
	if !found || string(marker) != ompOwnership {
		return "", false
	}

	line, _, found := bytes.Cut(rest, []byte{'\n'})
	if !found {
		return "", false
	}

	literal, prefix := strings.CutPrefix(string(line), "const handrailBinary = ")

	literal, suffix := strings.CutSuffix(literal, ";")
	if !prefix || !suffix {
		return "", false
	}

	var bin string
	if json.Unmarshal([]byte(literal), &bin) != nil || bin == "" || strings.ContainsRune(bin, 0) {
		return "", false
	}

	return bin, true
}

// ompModule never follows a target symlink, including a dangling one. A missing
// module is nil; every existing non-regular target is a conflict, not writable.
func ompModule(path string) ([]byte, error) {
	if path == "" {
		return nil, errOMPTarget
	}

	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}

	if err != nil {
		return nil, err
	}

	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w at %s: target is not a regular file", errOMPConflict, path)
	}

	return os.ReadFile(path)
}

func (a Adapter) installOMP(bin string) (Installation, error) {
	path := a.ConfigPath()

	current, err := ompModule(path)
	if err != nil {
		return Installation{}, err
	}

	if current != nil {
		if _, managed := ompRecordedBinary(current); !managed {
			return Installation{}, fmt.Errorf("%w at %s: missing or invalid handrail ownership metadata", errOMPConflict, path)
		}
	}

	next := ompExtension(bin)
	if bytes.Equal(current, next) {
		return Installation{Entries: len(a.events), Changed: false}, nil
	}

	return Installation{Entries: len(a.events), Changed: true}, write(path, next)
}

func (a Adapter) ompEntries() ([]Entry, error) {
	data, err := ompModule(a.ConfigPath())
	if err != nil {
		return nil, err
	}

	var bin string

	if data != nil {
		var managed bool

		bin, managed = ompRecordedBinary(data)
		if !managed || !bytes.Equal(data, ompExtension(bin)) {
			return nil, errOMPIntegrity
		}
	}

	out := make([]Entry, 0, len(a.events))
	for _, event := range a.events {
		out = append(out, Entry{Event: event.name, Binary: bin, Narrowed: false})
	}

	return out, nil
}

func ompBypass(_ Adapter, _, cwd string) (Bypass, error) {
	var out Bypass

	for _, name := range []string{"handrail.js", "handrail.ts", "handrail"} {
		path := filepath.Join(cwd, ".omp", "extensions", name)

		_, err := os.Lstat(path)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}

		if err != nil {
			return out, err
		}

		out.Disabled = append(out.Disabled, "project extension at "+path+" can shadow the user-level handrail module")
	}

	return out, nil
}
