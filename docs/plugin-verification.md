# Plugin verification

The plugins, the skills, and the bootstrap script sit outside the testscript
seam ([ADR 0009](adr/0009-testing-strategy.md)), so this checklist is their
acceptance gate. Re-run it whenever `scripts/bootstrap.sh`, `hooks/hooks.json`,
or a plugin manifest changes, and record what the run showed.

## Checklist

Run each case against an isolated `HOME`, never the real one, so a failed run
cannot leave hook entries behind on the machine. Isolate the plugin install with
`CLAUDE_CONFIG_DIR` on Claude Code and `CODEX_HOME` on Codex, which move where
the harness stores plugins and where sync writes alike. Create that directory,
or `$HOME/.claude` and `$HOME/.codex` when the variable is unset: harness
detection reads it, so without it sync finds no harness and case 1 cannot pass.

Run the whole checklist once per harness. Installing the plugin is:

```sh
codex plugin marketplace add <repo path or git URL>
codex plugin add handrail@handrail
```

Codex holds every non-managed hook until it is trusted, so on Codex every case
that runs through a session needs that trust granted first; see the Codex
findings below.

| # | Case | Expected |
|---|---|---|
| 1 | Fresh `HOME`, no binary anywhere | The artifact for this OS/arch downloads, its sha256 matches `checksums.txt`, the binary lands in `$XDG_DATA_HOME/handrail/bin/handrail`, and `sync` runs once |
| 2 | Corrupted archive | Install refused with the expected and actual digest named; no `bin` directory created |
| 3 | `handrail` on `PATH` | Nothing downloaded, nothing written, exit 0 |
| 4 | Managed binary at the wrong version | Reinstalled at the pinned version |
| 5 | Managed binary at the pinned version | Nothing downloaded, exit 0, no output |
| 6 | Plugin installed from a marketplace | `scripts/bootstrap.sh` survives the copy into the plugin cache and the SessionStart hook fires |
| 7 | `/handrail:add` from a plain-language description | A rule file that `check` accepts, `test` matches against its own incident, and `advise` reports on |

## 2026-08-18, v0.1.0-rc.1 on Claude Code, darwin/arm64

Cases 1 to 6 pass. Case 2 was driven with a stub `curl` serving a corrupted
archive; the others hit the real release.

- Case 1: `handrail_0.1.0-rc.1_darwin_arm64.tar.gz` verified and installed, then
  `sync` wrote six hook entries naming the absolute managed path.
- Case 6: the plugin installed from a local marketplace clone, `hooks/hooks.json`
  registered by auto-discovery alone, and the SessionStart hook fired on a
  headless session, installing the binary at mode `755` and syncing all six
  entries. Case 7 was exercised command by command (`check`, `test`,
  `advise --json`) rather than through a live session, because the sandboxed
  session had no credentials.

### Findings

- **Exec-bit survival**: the execute bit survived the copy into the plugin cache
  on macOS from a relative-path marketplace source. The hook still invokes the
  script as `sh "$CLAUDE_PLUGIN_ROOT/scripts/bootstrap.sh"`, since upstream
  documents nothing here and archive and npm sources are untested.
- **Per-arch selection**: Claude Code offers no per-OS or per-arch source
  selection, so the script does its own `uname` dispatch. Confirmed by
  installing one plugin copy that works on either architecture.
- **Repo root is the plugin root**, so that both plugin manifests can reference
  one `skills/` directory (a copied plugin cannot reference `../`). The cost is
  one warning from `claude plugin validate --strict`: a `CLAUDE.md` at the plugin
  root is not loaded as plugin context. That file is the repo's own agent
  instructions and is meant to stay; the warning is expected.
- **`CLAUDE_CONFIG_DIR` was not honored by sync.** Harness detection looked at
  `~/.claude` only, while Codex's `CODEX_HOME` was honored, so a session started
  with `CLAUDE_CONFIG_DIR` set installed the binary and then reported no harness
  found. Fixed in #37: the claude adapter carries the variable the way the codex
  one does.

## 2026-08-18, v0.1.0-rc.1 on Codex CLI 0.147.0, darwin/arm64

Cases 1 to 6 pass, each driven through a headless `codex exec` session against
an isolated `CODEX_HOME` and `HOME`, so the bootstrap ran as a real Codex hook
rather than as a script. Case 2 used a stub `curl` serving a corrupted archive;
the others hit the real release.

- The repo installs as a marketplace and a plugin: `codex plugin marketplace add
  <repo>` then `codex plugin add handrail@handrail` reported version
  `0.1.0-rc.1` and copied the plugin into `$CODEX_HOME/plugins/cache/`.
  `scripts/bootstrap.sh` survived the copy at mode `755`.
- Case 1 and case 6 are one run here: the SessionStart hook fired, installed
  `handrail_0.1.0-rc.1_darwin_arm64.tar.gz` at mode `755` under the sandbox
  `HOME`, and synced six entries into the sandbox `$CODEX_HOME/hooks.json`, each
  naming that absolute path.
- Case 2 refused the install naming both digests, and left no `bin` directory.
  Case 3 wrote nothing when a `handrail` sat on the session's `PATH`. Case 4
  replaced a stub reporting `handrail 0.0.1` with the pinned binary. Case 5 left
  the installed binary byte-identical.
- The add skill loads: a headless session listed it by name and read back its
  description verbatim, so one `skills/` directory serves both harnesses. Case 7
  was exercised command by command, as on Claude Code, because the sandboxed
  session had no credentials.

### Findings

- **Codex reads the Claude manifests when its own are absent.** Manifest lookup
  is `.codex-plugin/plugin.json`, then `.claude-plugin/plugin.json`, then
  `.cursor-plugin/plugin.json`; marketplace lookup is
  `.agents/plugins/marketplace.json`, then `.agents/plugins/api_marketplace.json`,
  then `.claude-plugin/marketplace.json`, then `.cursor-plugin/marketplace.json`.
  So the repo carries one marketplace file, the Claude-shaped one, which Codex
  accepts as written, and a `.codex-plugin/plugin.json` because the spec names it
  and because it is the manifest Codex prefers. Its `skills` and `hooks` paths
  are supplemented on top of Codex's own component discovery rather than
  replacing it, and naming a path Codex would have discovered anyway registers it
  once, not twice: an instrumented copy of the bootstrap recorded exactly one run
  per session.
- **The hook trust review is the expected first-run friction, twice over.** Codex
  holds every non-managed hook, plugin hooks included, until the user trusts it
  in the TUI's hooks screen; the trust state is a `[hooks.state]` entry keyed by
  config path, event, and index in `$CODEX_HOME/config.toml`. Until then the hook
  is skipped, and `codex exec` skips it silently: a headless run installed
  nothing and printed nothing. So a new user meets the review once for the
  plugin's bootstrap hook, and again for the entries `sync` then writes into
  `$CODEX_HOME/hooks.json`, before any guardrail fires. Each entry is reviewed
  once and re-reviewed only when it changes, which is what the stable
  one-entry-per-event shape buys ([spec](spec.md) section 3). The runs above were
  driven with `--dangerously-bypass-hook-trust` per invocation, against the
  sandbox home; that flag is for vetted automation, not for users.
- **A failing bootstrap is silent inside `codex exec`.** Case 2's refusal message
  never reached the session output, and the mismatch had to be read by running
  the script directly. The failure mode is safe (nothing installs) but
  undiagnosable from the session, so `handrail doctor` stays the first command
  when nothing fires.
- **Plugin hook env**: Codex's hook runner carries `PLUGIN_ROOT` and
  `CLAUDE_PLUGIN_ROOT` alike, so `hooks/hooks.json` invokes the bootstrap through
  `${CLAUDE_PLUGIN_ROOT}` on both harnesses, unchanged. The case 1 run resolved
  it and installed the binary, which is the proof.
