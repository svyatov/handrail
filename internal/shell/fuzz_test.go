package shell_test

import (
	"testing"

	"github.com/svyatov/handrail/internal/shell"
)

// seed adds one of each construct the readers follow. The command line comes
// from the agent, so whatever it holds, Read and Patch answer, never panic.
func seed(f *testing.F) {
	f.Helper()

	for _, line := range []string{
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
	} {
		f.Add(line)
	}
}

func FuzzRead(f *testing.F) {
	seed(f)

	f.Fuzz(func(_ *testing.T, line string) {
		shell.Read(line)
	})
}

func FuzzPatch(f *testing.F) {
	seed(f)

	f.Fuzz(func(_ *testing.T, line string) {
		shell.Patch(line)
	})
}
