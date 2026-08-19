# handrail

`docs/spec.md` is the behavioural source of truth. `CONTEXT.md` defines the vocabulary.

## Commands

```bash
task test    # go test -race -shuffle=on ./...
task cover   # the same run with coverage, fails under 95%
task lint    # golangci-lint run, then fmt --diff
task build   # the release build
```

CI calls these same tasks, so a local pass means what a green check means.

## Two constraints the build enforces

- Zero third-party runtime dependencies. `depguard` allows only the stdlib and this
  module outside `_test.go`; `github.com/rogpeppe/go-internal` is the sole test-only one.
- `os.Exit` only in `main.go`, so `run()` returns an exit code and stays testable.
  `forbidigo` enforces it.

## Tests

testscript over the compiled binary is the primary seam: a new case goes in
`testdata/script/*.txtar`, not a new `_test.go`. Unit tests under `internal/` are only
for what a process boundary cannot produce. See `docs/adr/0009-testing-strategy.md`.

## Agent skills

### Issue tracker

Issues live as GitHub issues in `svyatov/handrail`, managed via the `gh` CLI. See `docs/agents/issue-tracker.md`.

### Triage labels

The five canonical roles, each label string equal to its name. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `CONTEXT.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.

### Semantic code navigation

Serena's LSP symbol tools, optional, needs `gopls`. See `docs/agents/serena.md`.
