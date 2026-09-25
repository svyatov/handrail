# Contributing to handrail

Contributions are welcome. This file says what you need installed, what to run
before you push, and what a change has to satisfy to be merged.

## What a change has to satisfy

Two documents decide whether a change is acceptable:

- [`docs/spec.md`](docs/spec.md) is the behavioural source of truth. A change
  that alters behaviour changes the spec in the same pull request, and a change
  the spec forbids is not merged whatever the code does.
- [`CLAUDE.md`](CLAUDE.md) states the two constraints the build enforces: one
  third-party runtime dependency, `mvdan.cc/sh/v3/syntax`, and `os.Exit` only in `main.go`. Both are
  checked by the linter, so breaking either fails CI rather than review.

[`GLOSSARY.md`](GLOSSARY.md) defines the vocabulary. Use its words for types and
identifiers rather than inventing synonyms.

A flag table entry in `internal/shell` names, in a comment beside it, the
upstream man page or `--help` text it was taken from, so review can check each
flag against its source.

## Setup

You need [Go](https://go.dev/dl/) at the version in `go.mod`, currently 1.27.1,
and [mise](https://mise.jdx.dev/getting-started.html). Then:

```bash
git clone https://github.com/svyatov/handrail.git
cd handrail
mise trust && mise install
mise run build
```

`mise install` fetches golangci-lint, GoReleaser, syft and lefthook at the
versions `mise.toml` pins. None of them is in `go.mod` on purpose: a tool
directive would put roughly 200 modules into a `go.sum` whose first promise is
one third-party runtime dependency.

## Before you push

```bash
mise run test    # go test -race -shuffle=on ./...
mise run cover   # the same run with coverage, fails under 95%
mise run lint    # go.mod tidy and verified, go fix -diff, golangci-lint run, then the formatter, doc-path, flag-source and dash checks
mise run vuln    # govulncheck, fetched by go run at a pinned version
mise run fuzz    # 30s on each parser fuzz target
```

A fuzz target that finds a crash writes the input under the package's
`testdata/fuzz/`. Commit it with the fix: every `mise run test` replays it.

CI runs these same tasks, so a local pass means what a green check means.

To run them without remembering to, run `lefthook install` once: every commit
then runs the formatter and `mise run lint`, and every push runs `mise run test`.

## Tests

A change that adds functionality arrives with a test.

testscript over the compiled binary is the primary seam. A new case is a file in
[`testdata/script/`](testdata/script/), not a new `_test.go`. Unit tests under
`internal/` are only for what a process boundary cannot produce.
[`docs/adr/0009-testing-strategy.md`](docs/adr/0009-testing-strategy.md) says why.

## Opening a pull request

Fork the repository, branch from `main`, and open the pull request against
`main`. Branch names follow the commit types: `feat/`, `fix/`, `docs/`,
`refactor/`, `chore/`, each with a kebab-case description, as in
`fix/sync-ignores-config-dir`.

Commits use [Conventional Commits](https://www.conventionalcommits.org/en/v1.0.0/):
`type(scope): description`. Pull requests are squash-merged, and the branch is
deleted on merge.

The pull request template asks for what CI cannot report. Delete any heading you
have nothing to put under.

## Reporting bugs and asking questions

Both go to [GitHub issues](https://github.com/svyatov/handrail/issues). The bug
template asks for the output of `handrail check` and `handrail doctor`; run both
before filing, because between them they answer most reports about a rule that
did not fire.

Security vulnerabilities do not go to the issue tracker. See
[SECURITY.md](SECURITY.md).

## Who decides

Leonid Svyatov ([@svyatov](https://github.com/svyatov)) is the sole maintainer.
He reviews and merges every change and cuts every release. There is no
succession arranged: if he stops, nobody else currently has commit or release
access, and the project would need a fork to continue.
