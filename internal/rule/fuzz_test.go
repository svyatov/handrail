package rule_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/txtar"

	"github.com/svyatov/handrail/internal/rule"
)

// FuzzParse feeds Parse rule files nobody wrote by hand. A rule file can come
// from a cloned repository's .handrail/local/, so whatever it holds, Parse
// answers with a rule or an error, never a panic. The seeds are every rule
// file the script tests hold.
func FuzzParse(f *testing.F) {
	scripts, err := filepath.Glob("../../testdata/script/*.txtar")
	if err != nil {
		f.Fatal(err)
	}
	for _, path := range scripts {
		a, err := txtar.ParseFile(path)
		if err != nil {
			f.Fatal(err)
		}
		for _, file := range a.Files {
			if strings.HasSuffix(file.Name, ".md") {
				f.Add(file.Data)
			}
		}
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		r, err := rule.Parse("fuzz", data)
		if (r == nil) == (err == nil) {
			t.Fatalf("Parse() = %v, %v: want exactly one of a rule and an error", r, err)
		}
	})
}
