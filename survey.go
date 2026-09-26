package main

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/svyatov/handrail/internal/gitindex"
	"github.com/svyatov/handrail/internal/harness"
	"github.com/svyatov/handrail/internal/rule"
)

// repoSignal is one row of docs/spec.md section 12: its probes, a stat of a
// named path at the Project root or a glob over the git index. besides are
// stats taken only once another probe found something.
type repoSignal struct {
	id                    string
	stats, globs, besides []string
}

const (
	// matchesPerGlob bounds what one glob reports: enough to name the
	// pattern, never a listing of the tree.
	matchesPerGlob = 3
	packageJSON    = "package.json"
	// claudeLocal is the one instruction file in the repository that is the
	// user's own, unless git tracks it.
	claudeLocal = "CLAUDE.local.md"
	// maxImportHops is how far survey follows Claude Code's @ imports.
	maxImportHops = 4
)

// repoSignals is docs/spec.md section 12's closed list, in its order.
func repoSignals() []repoSignal {
	workflows := []string{".github/workflows/*.yml", ".github/workflows/*.yaml"}

	return []repoSignal{
		{id: "lockfile", stats: []string{
			"package-lock.json", "npm-shrinkwrap.json", "yarn.lock", "pnpm-lock.yaml", "bun.lock", "bun.lockb",
			"Gemfile.lock", "Cargo.lock", "go.sum", "poetry.lock", "uv.lock", "Pipfile.lock", "composer.lock",
			"mix.lock", "pubspec.lock", "Podfile.lock",
		}},
		{id: "gitignored-tree", stats: []string{".gitignore"}},
		{id: "linter", besides: []string{packageJSON}, stats: []string{
			"eslint.config.js", "eslint.config.mjs", "eslint.config.cjs", "eslint.config.ts", ".eslintrc",
			".eslintrc.js", ".eslintrc.cjs", ".eslintrc.json", ".eslintrc.yml", ".eslintrc.yaml", "biome.json",
			"biome.jsonc", ".golangci.yml", ".golangci.yaml", ".rubocop.yml", "ruff.toml", ".ruff.toml",
		}},
		{id: "task-wrapper", stats: []string{"Taskfile.yml", "Taskfile.yaml", "Makefile", "justfile", packageJSON}},
		{id: "git-hooks", stats: []string{
			".husky", ".pre-commit-config.yaml", "lefthook.yml", "lefthook.yaml", ".lefthook.yml",
		}},
		{
			id:    "test-naming",
			globs: []string{"**/*.test.[jt]s", "**/*.test.[jt]sx", "**/*.spec.[jt]s", "**/*.spec.[jt]sx", "**/*_spec.rb"},
			stats: []string{packageJSON, "Gemfile"},
		},
		{id: "ci-workflows", globs: workflows},
		{id: "pinned-actions", globs: workflows},
		{id: "env-file", stats: []string{".env"}, globs: []string{"**/.env", "**/.env.*"}},
		{id: "key-material", globs: []string{"**/*.pem", "**/*.key", "**/id_rsa", "**/id_ed25519"}},
		{id: "tag-release", globs: workflows, stats: []string{packageJSON}},
		{id: "workspace", stats: []string{"pnpm-workspace.yaml"}},
		{id: "terraform-state", globs: []string{"**/*.tfstate"}},
		{id: "mcp-server", stats: []string{".mcp.json"}},
		{
			id:    "migrations",
			globs: []string{"db/migrate/*", "migrations/*", "**/migrations/*.sql", "prisma/migrations/*", "alembic/versions/*"},
		},
	}
}

type surveySignal struct {
	ID    string   `json:"id"`
	Paths []string `json:"paths"`
}

type instructionFile struct {
	Path  string `json:"path"`
	Class string `json:"class"`
}

type surveyOutput struct {
	Signals          []surveySignal    `json:"signals"`
	InstructionFiles []instructionFile `json:"instruction_files"`
}

// cmdSurvey prints the Repo signals of the current Project root and the
// instruction files the Surveyor reads prose from. It opens no file but the
// git index and those instruction files, and spawns nothing.
func cmdSurvey(args []string, _ io.Reader, stdout, stderr io.Writer) int {
	flags := flag.NewFlagSet("survey", flag.ContinueOnError)
	flags.SetOutput(stderr)

	if !parseFlags(flags, args, stderr) {
		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)

		return 1
	}

	root := rule.RepoRoot(cwd)
	signals := repoSignals()
	globs := map[string]*regexp.Regexp{}

	for _, signal := range signals {
		for _, glob := range signal.globs {
			globs[glob], _ = rule.Glob(glob) // the table's globs are constants that compile
		}
	}

	tracked, err := trackedPaths(root, globs)
	if err != nil {
		fmt.Fprintf(stderr, "handrail: cannot read the git index: %v\n", err)

		return 1
	}

	found := []surveySignal{}

	for _, signal := range signals {
		paths := probe(root, signal, globs, tracked)
		if len(paths) > 0 {
			found = append(found, surveySignal{ID: signal.id, Paths: paths})
		}
	}

	return writeJSON(stdout, stderr, surveyOutput{
		Signals:          found,
		InstructionFiles: instructionFiles(root, slices.Contains(tracked, claudeLocal)),
	})
}

// trackedPaths reads the git index of root once for every glob, and for
// whether git tracks CLAUDE.local.md.
func trackedPaths(root string, globs map[string]*regexp.Regexp) ([]string, error) {
	return gitindex.Paths(root, func(path string) bool {
		if path == claudeLocal {
			return true
		}

		for _, re := range globs {
			if re.MatchString(path) {
				return true
			}
		}

		return false
	})
}

// probe runs one signal's probes against root, given the paths from the git
// index that match a glob, and returns every path they found, each once.
func probe(root string, signal repoSignal, globs map[string]*regexp.Regexp, tracked []string) []string {
	var paths []string

	add := func(path string) {
		if !slices.Contains(paths, path) {
			paths = append(paths, path)
		}
	}

	stat := func(names []string) {
		for _, name := range names {
			_, err := os.Lstat(filepath.Join(root, name))
			if err == nil {
				add(name)
			}
		}
	}

	stat(signal.stats)

	for _, glob := range signal.globs {
		found := 0

		for _, path := range tracked {
			if found < matchesPerGlob && globs[glob].MatchString(path) {
				add(path)

				found++
			}
		}
	}

	if len(paths) > 0 {
		stat(signal.besides)
	}

	return paths
}

// instructionSeed is a file an instruction file list starts from.
type instructionSeed struct {
	path    string
	imports bool // Claude Code reads the file, so its @ imports load too
}

// instructionSeeds lists every instruction file either harness loads at the
// Project root and in the user's harness directories, present or not.
func instructionSeeds(root string) []instructionSeed {
	seeds := []instructionSeed{
		{filepath.Join(root, "CLAUDE.md"), true},
		{filepath.Join(root, claudeLocal), true},
		{filepath.Join(root, ".claude", "CLAUDE.md"), true},
	}

	_ = filepath.WalkDir(filepath.Join(root, ".claude", "rules"), func(path string, _ fs.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(path, ".md") {
			seeds = append(seeds, instructionSeed{path, true})
		}

		return nil
	})

	for _, name := range []string{"AGENTS.override.md", "AGENTS.md", "CONTRIBUTING.md"} {
		seeds = append(seeds, instructionSeed{filepath.Join(root, name), false})
	}

	claude, _ := harness.Lookup("claude")
	if dir := claude.UserDir(); dir != "" {
		seeds = append(seeds, instructionSeed{filepath.Join(dir, "CLAUDE.md"), true})

		entries, _ := os.ReadDir(filepath.Join(dir, "rules"))
		for _, entry := range entries {
			if strings.HasSuffix(entry.Name(), ".md") {
				seeds = append(seeds, instructionSeed{filepath.Join(dir, "rules", entry.Name()), true})
			}
		}
	}

	codex, _ := harness.Lookup("codex")
	if dir := codex.UserDir(); dir != "" {
		// Codex reads its override in place of AGENTS.md unless it is empty.
		global := filepath.Join(dir, "AGENTS.override.md")

		info, err := os.Stat(global)
		if err != nil || info.Size() == 0 {
			global = filepath.Join(dir, "AGENTS.md")
		}

		seeds = append(seeds, instructionSeed{global, false})
	}

	return seeds
}

// instructionFiles lists the instruction files present among root's seeds,
// following Claude Code's @ imports, each classed by where it lives: in the
// repository it is repo prose, outside it the user's own, and CLAUDE.local.md
// is the user's while git does not track it.
func instructionFiles(root string, localTracked bool) []instructionFile {
	home, _ := os.UserHomeDir()
	files := []instructionFile{}
	seen := map[string]bool{}

	var visit func(path string, hops int, imports bool)

	visit = func(path string, hops int, imports bool) {
		resolved, ok := resolveFile(path)
		if !ok || seen[resolved] {
			return
		}

		seen[resolved] = true
		files = append(files, classify(root, path, resolved, localTracked))

		if !imports || hops == maxImportHops {
			return
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return
		}

		for _, ref := range importRefs(string(data)) {
			visit(importPath(ref, path, home), hops+1, true)
		}
	}

	for _, seed := range instructionSeeds(root) {
		visit(seed.path, 0, seed.imports)
	}

	return files
}

// importPath is the file an @ import in the file at from names: ~/ is the
// home directory, and a relative path is relative to from. It is "" where ~/
// names nothing, with no home directory.
func importPath(ref, from, home string) string {
	switch {
	case strings.HasPrefix(ref, "~/"):
		if home == "" {
			return ""
		}

		return filepath.Join(home, ref[2:])
	case filepath.IsAbs(ref):
		return ref
	default:
		return filepath.Join(filepath.Dir(from), ref)
	}
}

// resolveFile returns path with its directory resolved and its name not, so
// /tmp and /private/tmp are one place and a tracked symlink stays the
// repository's file, and whether path is a regular file.
func resolveFile(path string) (string, bool) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() {
		return "", false
	}

	dir, err := filepath.EvalSymlinks(filepath.Dir(path))
	if err != nil {
		return "", false
	}

	return filepath.Join(dir, filepath.Base(path)), true
}

// classify names an instruction file relative to root where it lives in the
// repository, and by its absolute path where it does not.
func classify(root, path, resolved string, localTracked bool) instructionFile {
	rel, err := filepath.Rel(root, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return instructionFile{Path: path, Class: "user"}
	}

	rel = filepath.ToSlash(rel)
	if rel == claudeLocal && !localTracked {
		return instructionFile{Path: rel, Class: "user"}
	}

	return instructionFile{Path: rel, Class: "repo"}
}

// importRefs returns the paths text imports with @, outside code spans and
// fenced code blocks, where Claude Code does not read them.
func importRefs(text string) []string {
	var (
		refs  []string
		fence string
	)

	for line := range strings.Lines(text) {
		trimmed := strings.TrimLeft(line, " ")

		if fence != "" {
			if strings.HasPrefix(trimmed, fence) {
				fence = ""
			}

			continue
		}

		for _, marker := range []string{"```", "~~~"} {
			if strings.HasPrefix(trimmed, marker) {
				fence = marker
			}
		}

		if fence == "" {
			refs = append(refs, lineRefs(line)...)
		}
	}

	return refs
}

// lineRefs returns the paths one line outside a code block imports: each word
// that opens with @, so an address is none, outside the code spans, which are
// every other backtick-separated part.
func lineRefs(line string) []string {
	var refs []string

	for i, part := range strings.Split(line, "`") {
		if i%2 == 1 {
			continue
		}

		for word := range strings.FieldsSeq(part) {
			if ref, ok := strings.CutPrefix(word, "@"); ok && ref != "" {
				refs = append(refs, ref)
			}
		}
	}

	return refs
}
