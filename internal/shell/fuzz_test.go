package shell_test

import (
	"testing"

	"github.com/svyatov/handrail/internal/shell"
)

// The command line comes from the agent, so whatever it holds, Read and Patch
// answer, never panic. The seeds are one of each construct the readers follow.
var lines = []string{
	"echo hi",
	"rm -rf build && git add -A",
	`bash -c 'cat secrets.env' > /dev/null 2>&1`,
	`env FOO=1 sudo -u root sh -c "echo $(ls ~)"`,
	`find . -name '*.go' -exec rm {} +`,
	"sed -i.bak s/a/b/ config.yml < input",
	"mise exec -- xargs -0 grep -l token",
	"cat <<'EOF' | sh\nrm -rf /\nEOF\n",
	`echo $'\x41\n' ${HOME:-/root} "$(eval "$cmd")"`,
	"cd sub && apply_patch <<'EOF'\n*** Begin Patch\n*** Add File: a.txt\n+hi\n*** End Patch\nEOF\n",
}

func FuzzRead(f *testing.F) {
	for _, line := range lines {
		f.Add(line)
	}
	f.Fuzz(func(_ *testing.T, line string) {
		shell.Read(line)
	})
}

func FuzzPatch(f *testing.F) {
	for _, line := range lines {
		f.Add(line)
	}
	f.Fuzz(func(t *testing.T, line string) {
		if _, body, ok := shell.Patch(line); !ok && body != "" {
			t.Fatalf("Patch(%q) returned a body without ok", line)
		}
	})
}
