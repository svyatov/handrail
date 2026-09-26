package harness_test

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/rogpeppe/go-internal/txtar"

	"github.com/svyatov/handrail/internal/harness"
)

// FuzzNormalize feeds every Adapter hook payloads no harness sent. The payload
// arrives on stdin from whatever runs the hook, so Normalize answers with
// payloads or an error, never a panic. The seeds are every JSON file the
// script tests hold, under each event they could arrive as.
func FuzzNormalize(f *testing.F) {
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
			if strings.HasSuffix(file.Name, ".json") {
				for _, event := range []string{"PreToolUse", "PostToolUse", "UserPromptSubmit", "Stop"} {
					f.Add(event, file.Data)
				}
			}
		}
	}

	f.Fuzz(func(_ *testing.T, event string, data []byte) {
		for _, a := range harness.Adapters() {
			_, _ = a.Normalize(event, data)
		}
	})
}
