package main

import (
	"flag"
	"fmt"
	"io"
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

type foundSignal struct {
	ID    string   `json:"id"`
	Paths []string `json:"paths"`
}

type instructionFile struct {
	Path  string `json:"path"`
	Class string `json:"class"`
}

type surveyOutput struct {
	Signals          []foundSignal     `json:"signals"`
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
			globs[glob], _ = rule.CompileGlob(glob) // the table's globs are constants that compile
		}
	}

	tracked, err := trackedPaths(root, globs)
	if err != nil {
		fmt.Fprintf(stderr, "handrail: cannot read the git index: %v\n", err)

		return 1
	}

	found := []foundSignal{}

	for _, signal := range signals {
		paths := probe(root, signal, globs, tracked)
		if len(paths) > 0 {
			found = append(found, foundSignal{ID: signal.id, Paths: paths})
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

	for _, path := range ruleFiles(filepath.Join(root, ".claude", "rules")) {
		seeds = append(seeds, instructionSeed{path, true})
	}

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

// ruleFiles returns every .md file below dir, in name order. A directory
// reached through a symlink is read too, since that is how rules are shared,
// and each directory once, so a link back up ends the walk.
func ruleFiles(dir string) []string {
	var (
		files  []string
		walked = map[string]bool{}
		walk   func(dir string)
	)

	walk = func(dir string) {
		resolved, err := filepath.EvalSymlinks(dir)
		if err != nil || walked[resolved] {
			return
		}

		walked[resolved] = true
		entries, _ := os.ReadDir(dir)

		for _, entry := range entries {
			path := filepath.Join(dir, entry.Name())

			info, err := os.Stat(path)
			switch {
			case err == nil && info.IsDir():
				walk(path)
			case strings.HasSuffix(path, ".md"):
				files = append(files, path)
			}
		}
	}

	walk(dir)

	return files
}

// instructionFiles lists the instruction files present among root's seeds,
// following Claude Code's @ imports, each classed by where it lives: in the
// repository it is repo prose, outside it the user's own, and CLAUDE.local.md
// is the user's while git does not track it.
func instructionFiles(root string, localTracked bool) []instructionFile {
	home, _ := os.UserHomeDir()
	// The managed policy file is an administrator's words, never listed.
	policy, _ := resolveFile(filepath.Join(harness.ManagedDir, "CLAUDE.md"))
	list := &instructionList{
		repo: repository(root), home: home, policy: policy, localTracked: localTracked,
		files: []instructionFile{}, seen: map[string]bool{}, read: map[string]int{},
	}

	for _, seed := range instructionSeeds(root) {
		list.visit(seed.path, 0, seed.imports, false)
	}

	return list.files
}

// instructionList is the walk instructionFiles makes over the seeds and
// their imports.
type instructionList struct {
	repo, home, policy string
	localTracked       bool
	files              []instructionFile
	seen               map[string]bool
	// read holds the fewest hops at which a file's imports were read, since
	// a shorter path to it leaves more hops for them.
	read map[string]int
}

// visit lists the file at path and follows its imports. A file the
// repository supplies is a stranger's words, so fromRepo keeps its imports
// inside the repository: followed out, they could put the user's own files,
// secrets included, on the list as the user's.
func (l *instructionList) visit(path string, hops int, imports, fromRepo bool) {
	resolved, ok := resolveFile(path)
	if _, inside := within(l.repo, resolved); !ok || resolved == l.policy || fromRepo && !inside {
		return
	}

	file := classify(l.repo, path, resolved, l.localTracked)
	if !l.seen[resolved] {
		l.seen[resolved] = true
		l.files = append(l.files, file)
	}

	if !l.follows(resolved, hops, imports) {
		return
	}

	for _, ref := range importRefs(path) {
		l.visit(importPath(ref, path, l.home), hops+1, true, file.Class == "repo")
	}
}

// follows reports whether the imports of the file at resolved, reached at
// hops, are to be read, and records that they are.
func (l *instructionList) follows(resolved string, hops int, imports bool) bool {
	if fewest, done := l.read[resolved]; !imports || hops == maxImportHops || done && fewest <= hops {
		return false
	}

	l.read[resolved] = hops

	return true
}

// repository returns root where it is a git repository's, and "" where it is
// not, since then no file sits in a repository.
func repository(root string) string {
	_, err := os.Lstat(filepath.Join(root, ".git"))
	if err != nil {
		return ""
	}

	return root
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

// classify names an instruction file relative to repo, the repository's root,
// where it lives there, and by its absolute path where it does not. With no
// repository, repo is "", which no path is relative to.
func classify(repo, path, resolved string, localTracked bool) instructionFile {
	rel, inside := within(repo, resolved)

	switch {
	case !inside:
		return instructionFile{Path: path, Class: "user"}
	case rel == claudeLocal && !localTracked:
		return instructionFile{Path: rel, Class: "user"}
	default:
		return instructionFile{Path: rel, Class: "repo"}
	}
}

// within returns resolved relative to repo, slash-separated, and whether it
// lies there.
func within(repo, resolved string) (string, bool) {
	rel, err := filepath.Rel(repo, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", false
	}

	return filepath.ToSlash(rel), true
}

// importRefs returns the paths the file at path imports with @, outside code
// spans and fenced code blocks, where Claude Code does not read them. A file
// that cannot be read imports nothing.
func importRefs(path string) []string {
	var (
		refs  []string
		fence string
	)

	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}

	for line := range strings.Lines(string(data)) {
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
// of its prose that opens with @, so an address is none.
func lineRefs(line string) []string {
	var refs []string

	for word := range strings.FieldsSeq(prose(line)) {
		if ref, ok := strings.CutPrefix(word, "@"); ok && ref != "" {
			refs = append(refs, ref)
		}
	}

	return refs
}

// prose returns line with each code span replaced by a space. As in
// CommonMark, a run of backticks opens a span that the next run of the same
// length closes, and a run nothing closes is literal text.
func prose(line string) string {
	var out strings.Builder

	for {
		start := strings.IndexByte(line, '`')
		if start < 0 {
			out.WriteString(line)

			return out.String()
		}

		run := backticks(line[start:])

		end := closingRun(line[start+run:], run)
		if end < 0 {
			out.WriteString(line[:start+run])
			line = line[start+run:]

			continue
		}

		out.WriteString(line[:start] + " ")
		line = line[start+run+end+run:]
	}
}

// closingRun returns where in text the first run of exactly length backticks
// starts, or -1 where there is none.
func closingRun(text string, length int) int {
	for pos := 0; ; {
		next := strings.IndexByte(text[pos:], '`')
		if next < 0 {
			return -1
		}

		pos += next

		run := backticks(text[pos:])
		if run == length {
			return pos
		}

		pos += run
	}
}

// backticks is the length of the run of backticks s opens with.
func backticks(s string) int {
	return len(s) - len(strings.TrimLeft(s, "`"))
}
