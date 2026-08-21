# Research: what dropping the Unix-only constraint costs

Resolves [#94](https://github.com/svyatov/handrail/issues/94), part of [#85](https://github.com/svyatov/handrail/issues/85). Researched 2026-08-21 against primary sources only (code.claude.com/docs, learn.chatgpt.com/docs and the openai/codex tree, go.dev and the Go 1.26.6 source in GOROOT, learn.microsoft.com, goreleaser.com, microsoft/winget-pkgs, the Scoop wiki), plus the handrail tree at `dd28375`. Windows is v1 non-goal N1 (`docs/spec.md` section 11), reopened by D5.

## Findings at a glance

| Area | Verdict |
| --- | --- |
| Config locations | Free. Both harnesses keep `~/.claude` and `~/.codex` under `%USERPROFILE%`; `sync.go`'s `userDir()` already does the right thing |
| XDG dirs | One helper. `xdgSubdir` grows a Windows branch; the stdlib offers no state or data dir, so handrail keeps owning that choice |
| Project root discovery | Small, with two real traps: junctions are not resolved, and NTFS case-insensitivity breaks the Trust key |
| Path conditions | The sharp one. The glob dialect is already separator-independent; the **payload** is not, and Claude Code documents the resulting miss as silent |
| PowerShell | Already correct by construction. `classify` reaches it; the cost lands on T2, not on the current engine |
| Distribution | Zip archive plus a scoop bucket. winget and code signing both fail their own cost test |
| **Hook entry writing** | Not in the ticket, and the largest single cost: three different Windows hook shells across the two harnesses |

## 1. Config locations

**Claude Code supports native Windows, not just WSL.** System requirements name "Windows 10 1809+ or Windows Server 2019+" and list PowerShell and CMD alongside Bash and Zsh; the "Set up on Windows" table offers Native Windows, WSL 2, and WSL 1, and says outright that "Installing Git for Windows is optional. It enables the Bash tool by providing Git Bash." Sandboxing is unsupported on native Windows ([setup](https://code.claude.com/docs/en/setup.md), [tools reference](https://code.claude.com/docs/en/tools-reference.md)).

**`~/.claude` stays `~/.claude`.** Verbatim: "On Windows, paths shown as `~/.claude` resolve to `%USERPROFILE%\.claude`" ([settings](https://code.claude.com/docs/en/settings.md)), repeated on the [directory reference](https://code.claude.com/docs/en/claude-directory.md) with "If you set `CLAUDE_CONFIG_DIR`, every `~/.claude` path on this page lives under that directory instead." `%APPDATA%` and `%LOCALAPPDATA%` appear nowhere in the settings, env-var, or hooks pages. `CLAUDE_CONFIG_DIR` is documented as behaving identically on Windows, on the [env-vars page](https://code.claude.com/docs/en/env-vars.md) rather than the settings page.

Correction for the record: the enterprise path on Windows is `C:\Program Files\ClaudeCode\managed-settings.json` plus an `HKLM\SOFTWARE\Policies\ClaudeCode` registry key. "The legacy Windows path `C:\ProgramData\ClaudeCode\managed-settings.json` is no longer supported as of v2.1.75" ([settings](https://code.claude.com/docs/en/settings.md)). handrail writes user-level config only, so this is context, not cost.

**Codex is native on Windows too, and its own repo docs disagree.** The [CLI install page](https://learn.chatgpt.com/docs/codex/cli.md) has a Windows tab with a PowerShell one-liner and no WSL caveat, and the [Windows sandbox page](https://learn.chatgpt.com/docs/windows/windows-sandbox.md) says the app "can run natively in PowerShell with a Windows sandbox instead of requiring WSL or a virtual machine." But [`docs/install.md`](https://github.com/openai/codex/blob/main/docs/install.md) in the repo still lists "Windows 11 **via WSL2**". Treat the docs site as current and the repo file as stale.

`CODEX_HOME` is documented with default `~/.codex`, and carries one behavioural difference worth knowing: "If you set it, the directory must already exist" ([environment variables](https://learn.chatgpt.com/docs/config-file/environment-variables.md)). The Windows expansion of `~/.codex` is **not documented anywhere**; it is `%USERPROFILE%\.codex` only by reading [`codex-rs/utils/home-dir/src/lib.rs`](https://github.com/openai/codex/blob/main/codex-rs/utils/home-dir/src/lib.rs), which resolves through the Rust `dirs` crate. `CODEX_INSTALL_DIR` defaults to `%LOCALAPPDATA%\Programs\OpenAI\Codex\bin`, but that is the binary, not the config.

**Cost: zero.** `Adapter.userDir()` reads the relocation variable, then falls back to `os.UserHomeDir()` joined with `.claude`/`.codex`. `os.UserHomeDir` "On Windows, it returns `%USERPROFILE%`" ([os](https://pkg.go.dev/os#UserHomeDir)). That is exactly the documented location for both harnesses. Nothing in `internal/harness/sync.go` changes.

## 2. The XDG assumption

`os.UserConfigDir` returns `%AppData%` on Windows, `$HOME/Library/Application Support` on Darwin, and `$XDG_CONFIG_HOME` (else `$HOME/.config`) on Unix; `os.UserCacheDir` returns `%LocalAppData%` on Windows ([os](https://pkg.go.dev/os#UserConfigDir)). Neither respects XDG on macOS, which is why ADR [0004](../adr/0004-rule-tiers-and-precedence.md) declined `UserConfigDir` in the first place. That reasoning survives; it just stops answering the question on a third platform.

**There is no stdlib state or data directory.** No `os.UserStateDir`, no `os.UserDataDir`; a grep of `src/os` finds only `XDG_CACHE_HOME` and `XDG_CONFIG_HOME`. The proposal to add them, [golang/go#62382](https://github.com/golang/go/issues/62382), was closed as not planned in 2023, and the earlier [#29960](https://github.com/golang/go/issues/29960) declined XDG data-dir support on the grounds that macOS and Windows have only one persistent per-user directory. There is no Go proposal for `XDG_STATE_HOME` at all. So handrail's Trust registry (XDG state) and bootstrap binary (XDG data) have no stdlib answer on any platform, which is already true today.

**Windows draws its line at roaming, not at config-versus-state.** `FOLDERID_RoamingAppData` is `%APPDATA%`, `FOLDERID_LocalAppData` is `%LOCALAPPDATA%` ([known folder IDs](https://learn.microsoft.com/en-us/windows/win32/shell/knownfolderid)); the intent is documented on [`Environment.SpecialFolder`](https://learn.microsoft.com/en-us/dotnet/api/system.environment.specialfolder): `ApplicationData` is "for the current **roaming** user. A roaming user works on more than one computer on a network", `LocalApplicationData` is "for the current, **non-roaming** user", and `UserProfile` carries "Applications should not create files or folders at this level". Microsoft documents no state tier at all.

The mapping that falls out, and it is a decision for T14 rather than a lookup:

| handrail tier | Unix today | Windows candidate | Why |
| --- | --- | --- | --- |
| Global rules | `$XDG_CONFIG_HOME/handrail/` | `%APPDATA%\handrail\` | Rules are exactly the thing a user wants on both their machines |
| Trust registry | `$XDG_STATE_HOME/handrail/trusted` | `%LOCALAPPDATA%\handrail\trusted` | Keyed by absolute path. `C:\src\repo` on the laptop is not the same repo on the desktop, so roaming it grants trust nobody gave |
| Bootstrap binary | `$XDG_DATA_HOME/handrail/bin/` | `%LOCALAPPDATA%\handrail\bin\` | Machine-and-arch specific; roaming an amd64 binary onto an arm64 box is a broken hook entry |

There is a live counter-argument for keeping `%USERPROFILE%\.config\handrail\`: both harnesses handrail supports put their own config at `%USERPROFILE%\.<name>` on Windows and neither touches `%APPDATA%` (section 1), so the dotfile precedent ADR 0004 followed on macOS is the *live* convention among handrail's own neighbours on Windows, not just a Unix habit. Splitting Global from Trust across `%APPDATA%` and `%LOCALAPPDATA%` is the more correct answer; keeping one `.config` root is the more consistent one. Either is one `runtime.GOOS` branch in `xdgSubdir`.

## 3. Project root discovery

`RepoRoot` does two things: `filepath.EvalSymlinks`, then a `filepath.Dir` walk looking for a `.git` entry.

**The walk terminates correctly**, on drive roots and UNC shares alike. The stdlib's own Windows table pins `C:\` and `\\host\share` as their own parents (`src/path/filepath/path_test.go`, `windirtests`), so the `parent == d` termination holds. Two caveats the same table pins: `c:.` is also its own parent, a *drive-relative* fixed point rather than a root, because Windows tracks a current directory per drive and `filepath.IsAbs("C:foo")` is false; and `\\host\share` and `\\host\share\` are both fixed points spelled differently, so string-comparing a root needs a `Clean` on both sides. Both argue for a `filepath.Abs` before the walk starts.

**Junctions are not resolved, and this is the first real trap.** Since Go 1.23 (`winsymlink=1`), "mount points no longer have `os.ModeSymlink` set ... As a result of these changes, `filepath.EvalSymlinks` no longer evaluates mount points" ([GODEBUG history](https://go.dev/doc/godebug)). `walkSymlinks` only follows an entry when `ModeSymlink` is set, and `types_windows.go` sets that bit for `IO_REPARSE_TAG_SYMLINK` only. Confusingly, `os.Readlink` still reads junction targets. Junctions are the ordinary unprivileged way to alias a directory on Windows, because real symlinks need Developer Mode or `SeCreateSymbolicLinkPrivilege` ([CreateSymbolicLinkW](https://learn.microsoft.com/en-us/windows/win32/api/winbase/nf-winbase-createsymboliclinkw), [Create symbolic links policy](https://learn.microsoft.com/en-us/previous-versions/windows/it-pro/windows-10/security/threat-protection/security-policy-settings/create-symbolic-links)). So a repo reached through a junction and the same repo reached directly produce two different roots, and therefore two different Trust keys. This is the Windows spelling of the `/tmp` versus `/private/tmp` problem the `EvalSymlinks` call was added to solve, and the call does not solve it.

**Case-insensitivity is the second.** "Do not assume case sensitivity ... NTFS supports POSIX semantics for case sensitivity but this is not the default behavior", and "Volume designators (drive letters) are similarly case-insensitive" ([naming a file](https://learn.microsoft.com/en-us/windows/win32/fileio/naming-a-file)); per-directory case sensitivity is opt-in via `fsutil` and inherited from the parent, so a CLI can assume neither ([case sensitivity](https://learn.microsoft.com/en-us/windows/wsl/case-sensitivity)). `isTrusted` does `slices.Contains` on exact strings, so `C:\Src\Repo` and `c:\src\repo` are two grants for one repo. The stdlib offers no case-folding path comparison: `os.SameFile` compares `FileInfo` identity, not strings, and `strings.EqualFold` implements Unicode simple folding, which is not Windows' uppercase-based file name comparison.

There is a partial rescue in the code already. On Windows `EvalSymlinks` calls `toNorm`, which rebuilds every component from `FindFirstFile` so the result carries the on-disk casing, and upper-cases the drive letter, with the source comment "result of EvalSymlinks must be unique" (`src/path/filepath/symlink_windows.go`). That is **not in the godoc**, and `RepoRoot` discards the error and keeps the raw path when it fails, which is exactly the junction case and the not-yet-existing-path case. So the normalization handrail needs is real but conditional, and the Trust gate must not depend on a conditional.

**Long paths and UNC are mostly handled, invisibly.** The Windows API limit is `MAX_PATH` 260, lifted on 1607+ only when both the `LongPathsEnabled` registry value is set and the app manifest declares `longPathAware`; "Because you cannot use the `\\?\` prefix with a relative path, relative paths are always limited to a total of MAX_PATH characters" ([maximum path length](https://learn.microsoft.com/en-us/windows/win32/fileio/maximum-file-path-limitation)). Go covers this two ways, neither documented in any godoc: the runtime sets the undocumented PEB `IsLongPathAwareProcess` bit on Windows 10.0.15063+ (`src/runtime/os_windows.go`, still present in 1.26.6 despite [#66560](https://github.com/golang/go/issues/66560) closing), and `fixLongPath` rewrites to `\\?\` form above 248 bytes inside `os.Open`, `os.Mkdir`, `os.Rename`, and friends. `filepath` understands `\\?\`, `\??\`, `\\.\`, and `\\host\share` lexically. Treat this as working and untestable rather than as a cost.

## 4. Path conditions

This is the rule-portability question, so it is the one that matters.

**The dialect is already fine.** `globToRegexp` in `internal/rule/rule.go` is hand-rolled and hardcodes `/` as the segment separator; it never calls `path.Match` or `filepath.Match`. That matters, because `filepath.Match`'s doc ends "On Windows, escaping is disabled. Instead, `'\\'` is treated as path separator" ([path/filepath](https://pkg.go.dev/path/filepath#Match)), and the grammar marks both escape productions "(except on Windows)". Had handrail delegated to `filepath.Match`, a rule's meaning would change with `GOOS` in two ways at once: separator and escape. It does not. **`docs/spec.md` section 2 needs no change.**

**The payload is not fine, and the failure is silent.** Claude Code's hooks reference states it plainly for `PreToolUse` and `PostToolUse` ([hooks](https://code.claude.com/docs/en/hooks.md)):

> On Windows, the path arrives with backslash separators, even when your hook runs under Git Bash where `$PWD` looks like `/c/project` [...] A comparison written with forward slashes, such as a `/src/` check, never matches a backslash path, and **the tool call proceeds as if the hook had nothing to block**.

The documented example payload is `"file_path": "C:\\project\\src\\index.ts"`. So every `path` condition a user wrote on macOS is dead on Windows, in either polarity, and dead the way spec section 2 forbids: "Silent failure is never allowed."

The fix is one line in the Adapter, not a dialect change: normalize `path` to forward slashes inside `Normalize`, unconditionally rather than under `runtime.GOOS`, so a Windows payload replayed through `handrail test --stdin` on a Mac still means what it meant. With that, `glob: **/*.env` matches `C:/project/.env` and a rule stays portable.

What normalization cannot fix is the drive letter. `C:/project/src/index.ts` has no Unix analogue, so `equals: /etc/passwd` and any rule anchored at a Unix absolute path are structurally machine-shaped. Section 2 already says patterns are anchored to the whole field, so an absolute-path rule was never portable across *machines*; Windows widens an existing limitation rather than introducing one. A `**/`-prefixed glob remains the portable spelling and should be what `skills/add` teaches.

**The Advisor loses path advice entirely.** `pathCondition` in `internal/harness/advise.go` requires `strings.HasPrefix(t.Value, "/")` for `equals` and `/` or `**/` for `glob`, on the stated reasoning that "harnesses report absolute paths, so only an absolute pattern or a `**/` prefix ever matches one." On Windows an absolute path starts `C:/`, so the `equals` branch refuses every one of them. Widening the test is easy; knowing what to emit is not, because Claude Code's permission-rule path dialect on Windows is **undocumented**: nothing says what `Edit(//C:/project/**)` denies. ADR [0005](../adr/0005-advisor-recommend-only.md) refuses translations that are not provably exact, so the correct v2 answer is that path advice is unavailable on Windows and says so, rather than guessing.

## 5. PowerShell as a shell kind

**handrail is already right here, and the spec is already wrong.** `classify` in `internal/harness/harness.go` returns `shell` for both `Bash` and `PowerShell`, and `Normalize` reads `tool_input.command` for the `shell` kind. Claude Code confirms both halves: `PowerShell` is a distinct `tool_name`, and "Your PreToolUse hooks receive the tool's command string in `tool_input.command`, with the same fields as the Bash tool" ([tools reference](https://code.claude.com/docs/en/tools-reference.md)).

The spec's matrix cell says PowerShell "is opt-in on Linux and macOS via `CLAUDE_CODE_USE_POWERSHELL_TOOL`", which is true but incomplete. The full documented behaviour ([env vars](https://code.claude.com/docs/en/env-vars.md)): "On Windows without Git Bash, the tool is enabled automatically; set to `0` to disable it. On Windows with Git Bash installed, the tool is rolling out progressively: set to `1` to opt in or `0` to opt out. On Linux, macOS, and WSL, set to `1` to enable it, which requires `pwsh` on your `PATH`." And on a Windows box without Git Bash, "**Claude Code doesn't register the Bash tool at all**" ([tools reference](https://code.claude.com/docs/en/tools-reference.md)). The docs then give the guardrail rule verbatim: "Match `Bash|PowerShell` in hooks that inspect shell commands ... A hook that matches only `Bash` never fires there." Matching by `kind` rather than by tool name is what makes handrail immune, which is the map's F5 limitation (no rule can name a non-MCP tool; only `kind` selects) paying for itself. Fix the matrix cell either way.

**The cost lands on T2.** Shell-awareness work will be POSIX-shaped, and PowerShell is not: backtick escapes rather than backslash, `$()` subexpressions with different semantics, object pipelines, `Invoke-Expression`. Whatever T2 builds must key its dialect off the payload's raw `tool_name`, not off `runtime.GOOS`, because the PowerShell tool is available on macOS and Linux too. A tokenizer that guesses POSIX for a PowerShell command line is the same class of error as the Advisor emitting a `Bash(...)` entry for a PowerShell rule, which `claudeEntries` already refuses in a `ponytail:` comment. Cheapest honest v2 answer: T2's semantics apply to `Bash` payloads, and a `PowerShell` payload falls back to the current string operators with the caveat named in the degradation report.

**Codex diverges further.** Its native Windows sandbox is real and configurable (`[windows] sandbox = "elevated"` or `"unelevated"`, [sandboxing](https://learn.chatgpt.com/docs/sandboxing.md)), but execpolicy has *zero* Windows content: the current [rules page](https://learn.chatgpt.com/docs/agent-configuration/rules.md) never mentions Windows or PowerShell, and `docs/execpolicy.md` in the repo is a three-line redirect stub. So `codexEntries`' `prefix_rule` promotion is unverifiable on Windows and should not be offered there.

## 6. Distribution, and the hook entry that is not in the ticket

### The hook entry writing, which is the largest single cost

Three hook shells, across two harnesses, none of them `sh`:

- **Claude Code shell form**: "The `command` string is passed to a shell: `sh -c` on macOS and Linux, **Git Bash on Windows, or PowerShell when Git Bash isn't installed**." A per-hook `shell` field takes `"bash"` or `"powershell"` ([hooks](https://code.claude.com/docs/en/hooks.md)).
- **Claude Code exec form** (`args` present) skips the shell entirely, with one Windows restriction: "On Windows, exec form requires `command` to resolve to a real executable such as a `.exe`."
- **Codex**: `%COMSPEC%` (fallback `cmd.exe`) with `/C`, versus `$SHELL` (fallback `/bin/sh`) with `-lc` elsewhere. This is **undocumented**; it is [`codex-rs/hooks/src/engine/command_runner.rs`](https://github.com/openai/codex/blob/main/codex-rs/hooks/src/engine/command_runner.rs) only. What *is* documented is the escape hatch: an optional per-hook `commandWindows` / `command_windows` override ([hooks](https://learn.chatgpt.com/docs/hooks.md)).

`sync.go` writes one POSIX-quoted string. `shellQuote` single-quotes anything containing a backslash, so `C:\...\handrail.exe` becomes `'C:\...\handrail.exe'`: correct under Git Bash, meaningless under `cmd.exe`, and needing a `&` call operator under PowerShell. Claude Code's exec form is the clean answer, because a Go binary satisfies the `.exe` restriction that breaks `.cmd` shims, and it sidesteps both shells' quoting. But `entryBinary` and `prune` recover handrail's own entries by suffix-cutting `" hook <harness> <event>"` off the command string, which exec form has no command string for. That is an args-aware reader plus a per-Adapter entry writer, and it touches idempotence, which is what keeps Codex's hash-based hook trust from re-prompting (spec section 4).

### The bootstrap

`scripts/bootstrap.sh` is POSIX `sh`, dispatching on `uname`. On native Windows Claude Code without Git Bash the hook runs under PowerShell, so `sh` is exactly what is missing. That needs a `bootstrap.ps1` and a per-platform hook entry in both plugin manifests. Three of its steps change shape:

- **`chmod +x` has no counterpart.** Windows resolves executables by extension through `PATHEXT` ([start](https://learn.microsoft.com/en-us/windows-server/administration/windows-commands/start)); a `.exe` written to disk runs. No Learn page states outright that there is no execute permission bit, and NTFS does carry a `Traverse Folder/Execute File` ACL right, so the flat claim would be an oversimplification. The practical conclusion is safe.
- **`runnable()` in `main.go` fails on every Windows binary.** It tests `fi.Mode().Perm()&0o111 != 0`, and Go synthesizes Windows file modes from one attribute: a regular file gets `0444` or `0666`, and only directories get `0111` (`src/os/types_windows.go`, `fileStat.mode`). So `doctor` would report every installed hook entry as pointing at something unrunnable.
- **`write()`'s mode preservation is a different promise.** `os.Chmod` on Windows only toggles `FILE_ATTRIBUTE_READONLY` (`src/os/root_windows.go`, `chmodat`), so the comment "Replacing the file must not silently change who can read it" is not honoured: NTFS permissions are ACLs, the temp file inherits the directory's, and the rename carries that over the user's `settings.json`. Whether that actually widens access needs verifying on a real machine, in the manner of `docs/plugin-verification.md`.

### GoReleaser and channels

- **Archives**: default format is `tar.gz`; the documented idiom is `format_overrides` with "Most common use case is to archive as zip on Windows" ([archives](https://goreleaser.com/customization/package/archives/)). Use the plural `formats:` key: `format` and `format_overrides.format` are both deprecated since v2.6, and "Deprecated options are only removed on major versions" ([deprecations](https://goreleaser.com/resources/deprecations/)). The `.exe` suffix is appended unconditionally, exposed as the `.Ext` template field and hardcoded in `internal/pipe/build/build.go`; it is nowhere stated in prose.
- **windows/arm64** is a supported Go port ([source install](https://go.dev/doc/install/source)), added in [1.17](https://go.dev/doc/go1.17). But the race detector "supports ... and `windows/amd64`" and nothing else on Windows ([race detector](https://go.dev/doc/articles/race_detector), confirmed by `internal/platform.RaceDetectorSupported`). So `task test` as spelled (`go test -race`) cannot run on a `windows-11-arm` runner, and an arm64 Windows artifact would ship covered only by the amd64 job.
- **The Homebrew cask is irrelevant**, as expected: Homebrew supports macOS, Linux, and WSL ([brew.sh](https://brew.sh/)), and the Cask Cookbook's `depends_on` offers `macos:`, `maximum_macos:`, and `linux:` with no Windows key ([Cask Cookbook](https://docs.brew.sh/Cask-Cookbook)). A WSL user wants the linux/amd64 tarball anyway.
- **scoop becomes necessary, and is cheap.** "A bucket is a Git repository containing JSON app manifests", added by any user with `scoop bucket add <name> <repo>` ([Buckets](https://github.com/ScoopInstaller/Scoop/wiki/Buckets)). A personal bucket is sufficient: no submission, no review, no fork. Manifests are JSON with SHA256 hashes Scoop verifies, and no signing requirement exists ([App Manifests](https://github.com/ScoopInstaller/Scoop/wiki/App-Manifests)). GoReleaser's scoop pipe needs a repository and the zip archive ([scoop](https://goreleaser.com/customization/publish/scoop/)). This is structurally the same move as `svyatov/homebrew-tap`, and with `go install` being the only alternative on Windows it stops being "only if demand appears".
- **winget stays a no.** No signing is required for non-MSIX (the only mandate is "MSIX installers must be signed to be included in the Microsoft community package repository", [installer schema](https://github.com/microsoft/winget-pkgs/blob/master/doc/manifest/schema/1.28.0/installer.md)), but the ongoing cost is per release: a fork of microsoft/winget-pkgs, a manifest triple, unique per-version URLs with redirects banned ([Policies](https://github.com/microsoft/winget-pkgs/blob/master/doc/Policies.md), [Validation](https://github.com/microsoft/winget-pkgs/blob/master/doc/Validation.md)), mandatory moderator approval, and a zip submission auto-labelled `Zip-Binary` or `Portable-Archive`, both of which "automatically add `Blocking-Issue`" ([Moderation](https://github.com/microsoft/winget-pkgs/blob/master/doc/Moderation.md)). GoReleaser's pipe also "will not fail the pipeline" on error, so a silently-skipped publish is the failure mode.
- **Code signing fails its own cost test.** Unsigned means SmartScreen shows "Windows protected your PC" and the user must click "Run anyway", and Microsoft's own current guidance is that a valid OV or EV certificate produces the *same* warning until reputation accrues: "Extended Validation (EV) certificates previously bypassed SmartScreen entirely on first download ... That behavior was removed in 2024", with "no exact threshold, but it can take several weeks and hundreds of clean installs" ([SmartScreen reputation](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/smartscreen-reputation), [code signing options](https://learn.microsoft.com/en-us/windows/apps/package-and-deploy/code-signing-options)). OV runs $150-300/year with a mandatory HSM or hardware token since June 2023; Azure Artifact Signing is ~$9.99/month and "does not provide instant SmartScreen trust"; SignPath Foundation signs qualifying open source projects free. GoReleaser documents no Windows signing pipe at all: `signs` and `binary_signs` are generic `cmd` wrappers that never mention signtool, osslsigncode, or Authenticode, and filtering signing to Windows artifacts with `if:` is Pro-only. Note also that Smart App Control on Windows 11 "will block execution of unsigned files unless the file has a positive reputation", which is the one scenario that would force the decision.
- **Attestations and `go install` are unaffected.** Neither the `actions/attest` docs nor `gh attestation verify` document any platform caveat, and attestations bind digests, so there is nothing OS-specific to break. `go install` puts the binary at `%USERPROFILE%\go\bin\handrail.exe` ([GOPATH code](https://go.dev/doc/gopath_code)).

### Tests

The test suite is the largest mechanical cost, and `docs/adr/0009` makes it unavoidable: testscript over the compiled binary is the primary seam.

testscript itself runs on Windows, proven by `rogpeppe/go-internal`'s own CI matrix (`ubuntu-latest, macos-latest, windows-latest`, running `go test -race ./...`), and it has no shell of its own, only a fixed built-in command set ([testscript](https://pkg.go.dev/github.com/rogpeppe/go-internal/testscript)). It documents `${/}` and `${:}` for separators, `$exe` as ".exe" on Windows, `[windows]`/`[unix]`/`[symlink]` conditions, and `HOME=/no-home (USERPROFILE on windows)`. Five concrete costs in handrail's 37 scripts:

1. **189 `exec sh -c` lines.** These are not testscript's shell, they spawn whatever `sh` is on `PATH`. Each needs a `[unix]` guard or a rewrite into testscript's own commands.
2. **`sandbox()` in `main_test.go` sets `HOME`.** `os.UserHomeDir` reads `%USERPROFILE%` on Windows, and testscript sets `USERPROFILE` for the script's home, so the helper leaks the real user's directory on Windows. `no_home.txtar`'s `env HOME=` then asserts nothing.
3. **9 `chmod` lines** collapse to a read-only toggle (`chmodat`, above).
4. **Expected-output blocks contain `/`-separated paths**, and handrail prints `filepath.Join` results.
5. **CRLF.** testscript documents nothing about line endings, and `cmp` is a byte comparison, so `core.autocrlf=true` on a Windows checkout would corrupt every `.txtar`. Adding `*.txtar text eol=lf` to a `.gitattributes` is the cheap defence. This one is inference from the absence of any handling, not a documented statement.

CI: `windows-latest` maps to `windows-2025`, with `windows-11-arm` available ([hosted runners](https://docs.github.com/en/actions/reference/runners/github-hosted-runners)). `-race` needs cgo and a mingw-w64 runtime of version 8 or later, which the hosted image satisfies in practice. The 95% coverage gate makes the Windows job load-bearing: any `GOOS`-branched code is uncovered on ubuntu and macos.

## Cost estimate

### What changes

| File | Change | Size |
| --- | --- | --- |
| `internal/rule/load.go` | `xdgSubdir` grows a Windows branch (`%APPDATA%` / `%LOCALAPPDATA%`, or a documented `.config` decision); `RepoRoot` calls `filepath.Abs` before the walk and decides what to do when `EvalSymlinks` fails | small |
| `internal/rule/trust.go` | Registry key needs case folding on Windows, or a documented refusal to grant a path that will not normalize | small, correctness-critical |
| `internal/harness/harness.go` | `Normalize` slash-normalizes `path` unconditionally | one line |
| `internal/harness/sync.go` | **The big one.** Exec-form entries for Claude Code, `command_windows` for Codex, an args-aware `entryBinary`/`prune`, `shellQuote` retired or dialected, `write`'s ACL behaviour verified | large |
| `internal/harness/advise.go` | `pathCondition` refuses Windows absolute paths; either widen or refuse explicitly | small |
| `main.go` | `runnable()` must not test the exec bit on Windows | one line |
| `.goreleaser.yml` | `goos: windows`, `format_overrides` to zip, a `scoops:` block | small |
| `scripts/bootstrap.ps1` (new), `hooks/hooks.json`, `.codex-plugin/` | Per-platform bootstrap entry, no `chmod`, PowerShell digest and extraction | medium, plus a verification pass in the manner of `docs/plugin-verification.md` |
| `.github/workflows/ci.yml` | A `windows-latest` job, amd64 only for `-race` | small |
| `.gitattributes` (new) | `*.txtar text eol=lf` | one line |
| `testdata/script/*.txtar`, `main_test.go` | 37 files, 189 `exec sh` lines, 9 `chmod`, path assertions, the `HOME` sandbox | **the bulk of the work** |
| `docs/spec.md` §3, §4, §9, §11; ADR 0004, ADR 0006; `README.md` | Tier locations, the PowerShell matrix cell, goos list, non-goal N1 | prose |
| `internal/rule/rule.go`, `internal/rule/exclude.go` | **No change.** The glob dialect is separator-independent; `excludeLine` is already `/`-joined for git | zero |

The engine changes are small and localized, and one of them (the payload slash normalization) is the only thing standing between a Windows user and silently dead path rules. The distribution changes are a config block and a second script. The test suite is where the days go.

### What stays broken

1. **Codex's hook shell on Windows is source-inferred.** `%COMSPEC% /C` with an extra double-quote wrapper is in the Rust source, not in any doc. Any entry handrail writes for Codex on Windows relies on an implementation detail that can change without a docs change. `command_windows` is documented; what runs it is not.
2. **The Advisor offers nothing on Windows.** Claude Code's permission-rule path dialect on Windows is undocumented, and Codex's execpolicy has no Windows documentation at all. ADR 0005 forbids guessing, so both promotions go quiet and every promoted rule loses its fail-closed backstop.
3. **Junctions defeat the Trust key.** `EvalSymlinks` stopped following mount points in Go 1.23. A repo reached through a junction is a different root, and `handrail trust` granted through one spelling reads as absent through the other.
4. **Case-insensitivity defeats it too, whenever normalization fails.** `EvalSymlinks` fixes casing only for paths that exist and only when it succeeds; `RepoRoot` swallows the error.
5. **No sandbox under native Claude Code on Windows.** Not handrail's layer, but users who assume defence in depth do not have it there.
6. **SmartScreen warns on every unsigned release, indefinitely.** Signing does not remove the first-run warning; it only lets reputation carry across versions. Smart App Control can escalate this from a warning to a block.
7. **windows/arm64 ships without `-race`.** The detector does not support it, so an arm64 Windows artifact is covered only by inference from the amd64 job.
8. **`disableAllHooks` is unchanged**, and the Git-Bash-absent case removes the `Bash` tool entirely. handrail is safe today because it matches on `kind`; any future tool-name-keyed logic would silently no-op there.

## Bottom line

Windows is affordable, and cheaper than the "Unix-only" framing suggests. The two things that would have been expensive are already right: the harnesses keep their config where `os.UserHomeDir` already looks, and the glob dialect never delegated to `filepath.Match`. What is left is one config-directory decision, one line of payload normalization that prevents a documented silent failure, a rewrite of how sync spells a hook entry, and a test suite that assumes a POSIX shell in 189 places. The permanent residue is that the Advisor cannot promote a rule on Windows and the Trust registry is only as reliable as `EvalSymlinks`, which on Windows is conditional. Ship the engine and the scoop bucket; skip winget and signing until a user reports Smart App Control blocking the binary.
