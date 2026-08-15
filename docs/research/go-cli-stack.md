# Research: modern Go CLI stack and distribution tooling

Resolves [#5](https://github.com/svyatov/hookify/issues/5). Researched 2026-08-15 against primary sources only (go.dev, pkg.go.dev, goreleaser.com, golangci-lint.run, upstream repos). Every recommendation is biased toward fast cold start and minimal dependencies, since hookify runs on every git hook invocation.

## Recommendations at a glance

| Area | Recommendation |
| --- | --- |
| Layout | `main.go` at repo root, logic in `internal/`; no `cmd/`, no `pkg/` |
| Flags/subcommands | stdlib `flag` with one `FlagSet` per subcommand |
| Cold start | Small import graph, no work in `init()`, CGO_ENABLED=0, `-ldflags "-s -w"` |
| Release | GoReleaser v2 on tag push via GitHub Actions |
| Distribution | GitHub Releases + `go install` + Homebrew cask in a tap; install script later if demand appears |
| Testing | Table-driven `t.Run` subtests, `testdata/`, `go test -race`; testscript for end-to-end |
| Lint | golangci-lint v2, `linters.default: standard`, binary install (never `go install`) |
| Go version | `go 1.25` in go.mod (oldest supported release), track the trailing supported major |

## 1. Project layout

The official layout doc, "Organizing a Go module" ([go.dev/doc/modules/layout](https://go.dev/doc/modules/layout)), starts from the simplest case: a basic command keeps its `package main` files in the repo root (`go.mod`, `main.go`), installable with `go install github.com/svyatov/hookify@latest`. A `cmd/` directory is called "a common convention" but the doc says it "isn't strictly necessary in a repository that consists only of commands"; it earns its keep only in mixed repos that also export importable packages. `internal/` is the recommended home for packages you do not want to expose, and the toolchain enforces the boundary ([cmd/go, Internal Directories](https://pkg.go.dev/cmd/go#hdr-Internal_Directories)), leaving you free to refactor without breaking external users. `pkg/` appears nowhere in official guidance.

**Recommendation**: `main.go` at the root (thin: parse args, dispatch, exit codes), everything else under `internal/`. Add `cmd/` only if hookify ever ships a second binary.

## 2. Flags and subcommands: stdlib flag vs cobra vs alternatives

What the primary sources establish:

- **stdlib `flag`**: "The FlagSet type allows one to define independent sets of flags, such as to implement subcommands in a command-line interface" ([pkg.go.dev/flag](https://pkg.go.dev/flag)). Single and double dashes are both accepted. No nested-command help or shell completions; subcommand dispatch is a manual switch on `os.Args[1]` with one `FlagSet` per command.
- **cobra**: nested subcommands, POSIX short/long flags, automatic help, bash/zsh/fish/powershell completions; used by Kubernetes, Hugo, and GitHub CLI ([spf13/cobra README](https://github.com/spf13/cobra)). Since v1.4.0 the viper dependency is gone ("Cobra's dependency tree has been drastically thinned!", [v1.4.0 release notes](https://github.com/spf13/cobra/releases/tag/v1.4.0)). What actually links into your binary is one third-party package, `spf13/pflag`, plus heavier stdlib imports like `text/template` and `regexp` ([cobra imports](https://pkg.go.dev/github.com/spf13/cobra?tab=imports)); `mousetrap` is Windows-only, and md2man/yaml are only pulled by the docs-generation subpackage.
- **urfave/cli v3**: root package imports stdlib only ([imports](https://pkg.go.dev/github.com/urfave/cli/v3?tab=imports)); testify in go.mod is test-only.
- **alecthomas/kong**: struct-tag/reflection based, root package imports are 100% stdlib ([imports](https://pkg.go.dev/github.com/alecthomas/kong?tab=imports)).
- **peterbourgon/ff v4**: "flags-first" layer over stdlib `flag`, positions `ff.Command` as "a declarative and lightweight alternative to more common frameworks like spf13/cobra, urfave/cli, or alecthomas/kingpin" ([peterbourgon/ff](https://github.com/peterbourgon/ff)); root package imports only stdlib plus its own `ffval`.

**Startup-cost evidence, stated honestly**: no primary source publishes cold-start benchmark numbers for any of these libraries; the research found no cobra issue or Go doc quantifying init-time cost. What is documented: Go compiles ahead of time with no VM to warm up ([go.dev/doc/faq](https://go.dev/doc/faq)), and the language spec guarantees that every transitively imported package's package-level variables and `init()` functions run before `main` starts ([spec, Package initialization](https://go.dev/ref/spec#Package_initialization)). So import-graph weight is the startup cost, and the measurable difference between these libraries is dependency and import footprint, where all five are stdlib-only or one small package.

**Recommendation**: stdlib `flag` with a manual subcommand switch. hookify has a small, known command surface; the cost is a page of dispatch code, and it keeps the binary at zero third-party runtime dependencies. If subcommand ergonomics ever genuinely hurt, kong or urfave/cli v3 add full subcommand support with zero third-party runtime imports; cobra is the choice only if generated shell completions become a requirement.

## 3. Cold-start latency techniques

All grounded in official docs:

- **Keep the import graph small.** Every imported package initializes before `main` ([spec](https://go.dev/ref/spec#Package_initialization)). Rejecting heavyweight config/framework deps (viper and friends) is the single highest-leverage lever.
- **No work in `init()` or package-level variables.** Same spec rule: that code runs on every invocation, wanted or not. Initialize lazily inside the command that needs it.
- **`CGO_ENABLED=0`.** Pure-Go static binaries, no dynamic linker involvement, and it is what makes cross-compilation trivial ([go.dev/doc/install/source](https://go.dev/doc/install/source)).
- **`-ldflags "-s -w"`.** Linker `-s` omits the symbol table and debug info and implies `-w` (omit DWARF) ([cmd/link](https://pkg.go.dev/cmd/link)); smaller binaries, faster to page in. `-trimpath` removes filesystem paths for reproducibility ([cmd/go](https://pkg.go.dev/cmd/go)), not speed.
- **Ignore GC tuning.** The runtime's minimum total heap is 4 MiB ([GC guide](https://tip.golang.org/doc/gc-guide)); a short-lived, low-allocation hook process effectively never collects, so GOGC knobs are moot.

## 4. Cross-compilation and GoReleaser

Cross-compiling is `GOOS`/`GOARCH` plus `CGO_ENABLED=0` ([go.dev/doc/install/source](https://go.dev/doc/install/source)); GoReleaser itself documents that CGO cross-builds "won't work out of the box" ([limitations/cgo](https://goreleaser.com/resources/limitations/cgo/)), one more reason to stay pure Go.

GoReleaser v2 ([goreleaser.com](https://goreleaser.com), which serves a working `llms.txt`):

- Config must declare `version: 2` ([errors/version](https://goreleaser.com/resources/errors/version/)). `goreleaser init` scaffolds, `goreleaser check` validates, `goreleaser release --snapshot --clean` dry-runs ([quick start](https://goreleaser.com/getting-started/quick-start/)).
- Go builder defaults already match this project's bias: ldflags `-s -w -X main.version={{.Version}} -X main.commit={{.Commit}} -X main.date={{.Date}} ...`, goos `[darwin, linux, windows]`, goarch `[386, amd64, arm64]`, main package `.` ([builders/go](https://goreleaser.com/customization/builds/builders/go/)). Declare `var (version = "dev"; commit = "none"; date = "unknown")` in main and let it inject ([using-main.version cookbook](https://goreleaser.com/resources/cookbooks/using-main.version/)). Trim goarch to `[amd64, arm64]`; nobody needs 386 for a new tool.
- Releases run in GitHub Actions on tag push: `permissions: contents: write`, checkout with `fetch-depth: 0`, setup-go, then `goreleaser/goreleaser-action@v7` with `args: release --clean` ([ci/actions](https://goreleaser.com/customization/ci/actions/)).
- A `checksums.txt` (sha256) is generated and uploaded with every release ([checksum](https://goreleaser.com/customization/checksum/)).
- Homebrew: the old `brews` (formula) pipe is deprecated since v2.10 and enforced at v2.16; the replacement is `homebrew_casks` ([deprecations](https://goreleaser.com/resources/deprecations/), [homebrew_casks](https://goreleaser.com/customization/homebrew_casks/)). Minimal config is a name plus a tap repository. Caveat for unsigned macOS binaries: the docs offer an xattr post-install hook to strip `com.apple.quarantine`, warning "Apple may disable this bypass method in future macOS versions without notice".
- winget, scoop, and nfpm (deb/rpm/apk) pipes exist when demand appears ([winget](https://goreleaser.com/customization/publish/winget/), [scoop](https://goreleaser.com/customization/publish/scoop/), [nfpm](https://goreleaser.com/customization/package/nfpm/)).

## 5. Distribution channels

- **GitHub Releases**: the artifact host; GoReleaser uploads archives plus `checksums.txt`. Zero extra work.
- **`go install github.com/svyatov/hookify@latest`**: works for free once the module root is `package main` and releases are semver git tags; since Go 1.16 version-suffixed installs run in module-aware mode ignoring the local go.mod ([go.dev/ref/mod#go-install](https://go.dev/ref/mod#go-install)). GoReleaser does not document an explicit "keep it go-installable" rule, but its defaults (main at `.`, enforced semver tags, [limitations/semver](https://goreleaser.com/resources/limitations/semver/)) preserve it.
- **Homebrew tap**: a repo named `homebrew-<something>`, e.g. `svyatov/homebrew-tap`; users can `brew install svyatov/tap/hookify` without tapping first ([docs.brew.sh/Taps](https://docs.brew.sh/Taps), [How-to-Create-and-Maintain-a-Tap](https://docs.brew.sh/How-to-Create-and-Maintain-a-Tap)). Casks live in a top-level `Casks/` directory and need globally unique names. GoReleaser's `homebrew_casks` pipe pushes to the tap automatically.
- **Install script (curl | sh)**: GoReleaser has no built-in install-script feature; the building blocks are release assets plus checksums.txt. Skip it at launch; add one only if users on machines without Go or brew actually ask.

**Recommendation**: ship GitHub Releases + `go install` + a brew tap via GoReleaser from day one; defer install script, winget, scoop, and nfpm until requested.

## 6. Testing conventions

- **Table-driven tests with subtests**: slice-of-struct cases run via `t.Run` ([go.dev/wiki/TableDrivenTests](https://go.dev/wiki/TableDrivenTests), [pkg.go.dev/testing](https://pkg.go.dev/testing)). `t.Parallel` where tests are independent; note `t.Setenv` cannot be used in parallel tests.
- **Fixtures in `testdata/`**: "The go tool will ignore a directory named 'testdata', making it available to hold ancillary data needed by the tests" ([cmd/go](https://pkg.go.dev/cmd/go#hdr-Test_packages)). Golden files are not formally documented but are the Go repo's own practice (paired `.input`/`.golden` in [cmd/gofmt/testdata](https://github.com/golang/go/tree/master/src/cmd/gofmt/testdata)).
- **Helpers**: `t.TempDir` (auto-removed), `t.Cleanup` (LIFO), `TestMain` for whole-package setup ([pkg.go.dev/testing](https://pkg.go.dev/testing)).
- **Race detector in CI**: `go test -race` ([race detector article](https://go.dev/doc/articles/race_detector)).
- **End-to-end CLI tests**: cmd/go itself is tested with txtar script tests in isolated work dirs ([script test README](https://github.com/golang/go/blob/master/src/cmd/go/testdata/script/README)); the reusable extraction is `rogpeppe/go-internal/testscript`, which registers the binary's commands via `testscript.Main` in `TestMain` and runs `testdata/*.txtar` scripts ([pkg.go.dev/github.com/rogpeppe/go-internal/testscript](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript)). This is the one test-only dependency worth taking for a git-hook CLI: it exercises the real binary against real files, and being test-scoped it never touches the shipped binary's import graph.
- **Later, if needed**: native fuzzing (`FuzzXxx(*testing.F)`, seed corpus in `testdata/fuzz/`, [go.dev/doc/security/fuzz](https://go.dev/doc/security/fuzz/)) for any config-parsing surface; `benchstat` for A/B benchmark comparisons ([x/perf/cmd/benchstat](https://pkg.go.dev/golang.org/x/perf/cmd/benchstat)).

## 7. golangci-lint

The v2 line is current (v2.0.0 released 2025-03-24, [release](https://github.com/golangci/golangci-lint/releases/tag/v2.0.0); docs show v2.12.2 as of research date).

- **Install as a binary, never `go install`**: the docs state "Using go install/go get, 'tools pattern', and tool command/directives installations aren't guaranteed to work. We recommend using binary installation." ([install docs](https://golangci-lint.run/docs/welcome/install/local/)). Use the install script or `brew install golangci-lint`.
- **Config**: `.golangci.yml` requires `version: "2"`; linters and formatters are separate sections; `linters.default` accepts `standard`, `all`, `none`, `fast` ([configuration](https://golangci-lint.run/docs/configuration/file/)).
- **Linter set**: the `standard` group is errcheck, govet, ineffassign, staticcheck, unused (source of truth: `pkg/lint/lintersdb/builder_linter.go` in the [golangci-lint repo](https://github.com/golangci/golangci-lint)). That is exactly the right minimal set for a small CLI, and `default: standard` gets it with a two-line config.
- **CI**: the official [golangci/golangci-lint-action](https://golangci-lint.run/docs/welcome/install/ci/) with a pinned golangci-lint version.

## 8. Minimum Go version policy

- Support window: "Each major Go release is supported until there are two newer major releases" ([release policy](https://go.dev/doc/devel/release)). As of 2026-08-15 the supported majors are 1.26 (current stable go1.26.6) and 1.25 ([go.dev/dl](https://go.dev/dl/)).
- Since Go 1.21 the `go` directive in go.mod is a hard minimum, not advisory: "Go toolchains refuse to use modules declaring newer Go versions", and it pins language semantics ([go.dev/ref/mod](https://go.dev/ref/mod#go-mod-file-go)). With default `GOTOOLCHAIN=auto`, a newer required toolchain is downloaded automatically ([go.dev/doc/toolchain](https://go.dev/doc/toolchain)).
- There is no separate official mandate for tool authors; "support the last two releases" simply mirrors the upstream security-fix window.

**Recommendation**: set `go 1.25` (the older supported major) in go.mod today, and bump to the new trailing major shortly after each February/August release. Never set the `go` line above what the code actually needs; it is a floor imposed on every `go install` user. Omit the `toolchain` directive.

## Bottom line for hookify

Root `main.go` + `internal/`, stdlib `flag`, zero third-party runtime dependencies, one test-only dependency (testscript), CGO_ENABLED=0 with `-s -w`, GoReleaser v2 releasing to GitHub Releases, `go install`, and a `svyatov/homebrew-tap` cask on tag push, golangci-lint v2 at `default: standard`, go.mod at the trailing supported Go major. Everything else (cobra, install script, winget/scoop/nfpm, fuzzing) has a named trigger for when to add it and stays out until then.
