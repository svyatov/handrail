# Plugin verification

The plugins, the skills, and the bootstrap script sit outside the testscript
seam ([ADR 0009](adr/0009-testing-strategy.md)), so this checklist is their
acceptance gate. Re-run it whenever `scripts/bootstrap.sh`, `hooks/hooks.json`,
or a plugin manifest changes, and record what the run showed.

## Checklist

Run each case against an isolated `HOME`, never the real one, so a failed run
cannot leave hook entries behind on the machine. Create `$HOME/.claude` in it:
harness detection reads that path and nothing else, so without it sync finds no
harness and case 1 cannot pass. Isolate the plugin install with
`CLAUDE_CONFIG_DIR`, which moves where Claude Code itself stores plugins but,
per the findings below, does not move where sync looks.

| # | Case | Expected |
|---|---|---|
| 1 | Fresh `HOME`, no binary anywhere | The artifact for this OS/arch downloads, its sha256 matches `checksums.txt`, the binary lands in `$XDG_DATA_HOME/handrail/bin/handrail`, and `sync` runs once |
| 2 | Corrupted archive | Install refused with the expected and actual digest named; no `bin` directory created |
| 3 | `handrail` on `PATH` | Nothing downloaded, nothing written, exit 0 |
| 4 | Managed binary at the wrong version | Reinstalled at the pinned version |
| 5 | Managed binary at the pinned version | Nothing downloaded, exit 0, no output |
| 6 | Plugin installed from a marketplace | `scripts/bootstrap.sh` survives the copy into the plugin cache and the SessionStart hook fires |
| 7 | `/handrail:add` from a plain-language description | A rule file that `check` accepts, `test` matches against its own incident, and `advise` reports on |

## 2026-08-18, v0.1.0-rc.1 on darwin/arm64

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
- **`CLAUDE_CONFIG_DIR` is not honored by sync.** Harness detection looks at
  `~/.claude` only, while Codex's `CODEX_HOME` is honored. A session started with
  `CLAUDE_CONFIG_DIR` set therefore installs the binary and then reports no
  harness found. Engine-side gap, tracked separately from the plugin.
