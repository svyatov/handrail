# Hot path budget (T36)

Measurements for [T36](https://github.com/svyatov/handrail/issues/126). All numbers are wall time on one machine, page cache warm (the state a session's second and later calls find), 200 to 1000 runs, median and p90.

Machine: Apple M1 Pro, 32 GB, macOS 26.6.2, APFS. Go 1.27.1, git 2.54.0 (Apple Git-157). handrail built from `main` at `c404ab4` with the release flags.

Reproduce: `bench/` is a scratch module (`go build -o bench ./docs/research/hot-path-budget/bench`), and `internal/rule/examples_bench_test.go` on this branch is the engine half. Test repos are synthetic: `git update-index --index-info` with one blob, so the index is real and there is no working tree. The 120,000-path index is 13.4 MB as v2 and 9.6 MB as v4.

## Baseline: the whole hook process

| Invocation, 50 Global rules | median | p90 |
|---|---|---|
| `hook claude PreToolUse`, no match | 4.67 ms | 5.61 ms |
| `hook claude SessionStart` | 4.69 ms | 5.94 ms |
| `/usr/bin/true` (the OS spawn floor) | 1.85 ms | 2.67 ms |

T1 measured 4.87 ms with one rule, so 50 rules cost nothing visible. Linking `mvdan.cc/sh/v3/syntax` makes the binary 0.46 MB larger and adds at most 0.1 ms to process start.

## T21: is anything under `.handrail/local` in the git index?

| Repo | `git ls-files --error-unmatch` via `/usr/bin/git` | same, real git binary | in process, read whole index | in process, stop past the prefix |
|---|---|---|---|---|
| 200 paths | 14.5 / 17.4 ms | 4.8 / 6.1 ms | 23 / 33 µs | 11 / 14 µs |
| 120k paths, v2 | 20.4 / 22.7 ms | 10.7 / 12.0 ms | 6.8 / 7.2 ms | 12 / 15 µs |
| 120k paths, v4 | 21.3 / 23.7 ms | | 7.6 / 8.1 ms | 14 / 17 µs |
| 120k paths, `local/` tracked, v2 | 22.5 / 27.2 ms | | 6.8 / 7.2 ms | 14 / 17 µs |
| 120k paths, `local/` tracked, v4 | 21.5 / 23.9 ms | | 7.5 / 8.1 ms | 13 / 16 µs |

(median / p90)

- A git spawn costs one to four whole hook invocations, and grows with the repo because git reads the whole index. On macOS `/usr/bin/git` is the `xcrun` shim, which adds about 9 ms; that is what a harness launched from a GUI finds on `PATH`.
- Reading in process is flat in repo size only if it stops early. Index entries are sorted by path, and `.handrail/` sorts near the top because `.` precedes every letter and digit, so the reader stops after a handful of entries. Read whole, it costs 7 ms at 120k paths.
- The reader is about 100 lines of stdlib: header, fixed entry fields, v3 extended flags, v4 prefix-compressed names, and a sparse-index directory entry that covers the prefix. It agrees with git on all five repos.
- What the reader must also cover, not measured here: SHA-256 repos (32-byte object names, read `extensions.objectFormat`); a linked worktree keeps its own index in the directory its `.git` file points at, not in `commondir`, which is where `gitDir` in `exclude.go` ends; and a split index (`core.splitIndex`, off by default), where entries also live in `sharedindex.<hash>` and deletions sit in an EWAH bitmap at the end of the file, so the early exit does not hold. A repository cannot ship any of this: the index and `.git/config` are local to the clone.

## T15: one Decision log append

Read the grant file, `MkdirAll` the state dir, `O_APPEND|O_CREATE` open, one 386-byte write, close.

| | median | p90 |
|---|---|---|
| no `fsync` | 58 µs | 148 µs |
| with `fsync` | 5.0 ms | 6.0 ms |

Go's `File.Sync` on darwin is `F_FULLFSYNC`, which flushes the drive cache. T15 decided one `O_APPEND` write and no lock, and never named `fsync`; with one the matching invocation would cost twice a no-match one.

## T32: Examples at `SessionStart`

50 rules, two Examples each, 50 of the 100 Examples shell commands.

| Part | cost |
|---|---|
| Parse 50 rule files and evaluate 100 Examples, each against its own rule (today's engine) | 190 µs |
| Parse 50 commands with `mvdan.cc/sh/v3/syntax`, walk every call, re-parse literal `-c` code to four levels (T2 + T31) | 81 µs median, 171 µs p90 |

The commands lean harder than real rules: compound lines, substitutions, heredocs, and a three-deep nested shell (`bench/commands.go`). Examples add about 0.3 ms to a 4.7 ms `SessionStart`, so T32's version gate and its state file are not needed. Not measured: `domain` derivation (T26), which is one `net/url` parse per Example that carries a `url`.
