package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/svyatov/handrail/internal/rule"
)

const importUsage = `Usage: handrail import hookify [path]

Converts upstream hookify's .claude/hookify.*.local.md rules. Anything the rule
format cannot express is skipped and reported, never written.
`

// cmdImport is the one-shot converter from another tool's rules. It writes into
// the Project-personal tier, which is where somebody else's guardrails belong
// until their new owner has read them: nothing lands in a committed tier, and
// nothing that cannot be expressed lands at all.
func cmdImport(args []string, _ io.Reader, stdout, stderr io.Writer) int {
	// The format leads, so a flag in its place is a request for the usage rather
	// than the name of something to convert.
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		fmt.Fprint(stderr, importUsage)

		return 1
	}

	if args[0] != "hookify" {
		fmt.Fprintf(stderr, "handrail import: unknown format %q; known: hookify\n", args[0])

		return 1
	}

	flags := flag.NewFlagSet("import", flag.ContinueOnError)
	flags.SetOutput(stderr)

	err := flags.Parse(args[1:])
	if err != nil {
		return 1
	}

	if flags.NArg() > 1 {
		fmt.Fprintf(stderr, "handrail import: unexpected argument %q\n", flags.Arg(1))

		return 1
	}

	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintf(stderr, "handrail: %v\n", err)

		return 1
	}

	root := rule.RepoRoot(cwd)

	// Upstream reads .claude/ from the repo root, so that is where the import
	// looks unless told otherwise.
	src := filepath.Join(root, ".claude")
	if flags.NArg() == 1 {
		if src = flags.Arg(0); !filepath.IsAbs(src) {
			src = filepath.Join(cwd, src)
		}
	}

	results, err := rule.ImportHookify(src, rule.LocalDir(root))
	if err != nil {
		fmt.Fprintf(stderr, "handrail import: %v\n", err)

		return 1
	}

	reportImport(stdout, root, results)

	return 0
}

// reportImport names each converted rule's source and target, and each skipped
// one's reason, then counts both.
func reportImport(stdout io.Writer, root string, results []rule.Imported) {
	imported, skipped := 0, 0

	for _, result := range results {
		if result.Reason != "" {
			skipped++

			fmt.Fprintf(stdout, "skipped  %s: %s\n", relTo(root, result.Source), result.Reason)

			continue
		}

		imported++

		fmt.Fprintf(stdout, "imported %s -> %s\n", relTo(root, result.Source), relTo(root, result.Target))
	}

	fmt.Fprintf(stdout, "%d imported, %d skipped\n", imported, skipped)
}

// relTo shortens a path for the report, and leaves it alone when it lies
// outside the project.
func relTo(root, path string) string {
	rel, err := filepath.Rel(root, path)
	if err != nil || strings.HasPrefix(rel, "..") {
		return path
	}

	return rel
}
