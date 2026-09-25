# handrail

`docs/spec.md` is the behavioural source of truth, about 100 KB, one bullet per line and
up to 4 KB each, so a 30-line range runs 15-40 KB. Each bullet opens with its field or
bold term: find it with ``grep -n '^- [`*]*<term>' docs/spec.md`` and read those lines
alone. `GLOSSARY.md` defines the vocabulary.

## Commands

```bash
mise run test    # go test -race -shuffle=on ./...
mise run cover   # the same run with coverage, fails under 95%
mise run lint    # go mod tidy -diff and verify, golangci-lint run, then fmt --diff; with mise run test, past 2 minutes: raise the Bash timeout
mise run vuln    # govulncheck through go run, so it stays out of go.mod
mise run fuzz    # 30s per parser fuzz target; a crash input lands in testdata/fuzz/, commit it
mise run build   # the release build
```

CI calls these same tasks, so a local pass means what a green check means.

## Two constraints the build enforces

- One third-party runtime dependency, `mvdan.cc/sh/v3` (ADR 0011). `depguard` allows
  only the stdlib, this module and `mvdan.cc/sh/v3/syntax` outside `_test.go`, and denies
  `os/exec`; `github.com/rogpeppe/go-internal` is the sole test-only one.
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

The seven canonical roles (two category, five state), each label string equal to its name. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` + `docs/adr/` at the repo root. See `docs/agents/domain.md`.

### Semantic code navigation

Serena's LSP symbol tools, optional, needs `gopls`. See `docs/agents/serena.md`.
