# handrail

Declare an agent guardrail once, in one neutral format, and enforce it through every harness's native hooks.

- **Both harnesses.** One rule file is enforced in Claude Code and in Codex CLI, through each one's own hook mechanism.
- **One binary, nothing else.** No third-party runtime dependencies. A lint allow-list keeps it that way.
- **hookify rules come with you.** hookify runs on Claude Code alone; `handrail import hookify` converts what you already wrote.
- **Single-digit milliseconds.** An event matching no rule costs a few ms end to end. A test fails CI if the median ever crosses 50ms.

Install the plugin in Claude Code. It downloads the binary on the next session start, verifies its checksum, and syncs once:

```bash
claude plugin marketplace add svyatov/handrail
claude plugin install handrail@handrail
```

A rule is one markdown file: a matcher in the frontmatter, the message you want the agent to read in the body. Save this as `.handrail/local/no-force-push.md`:

```markdown
---
event: PreToolUse
kind: shell
action: block
conditions:
  - field: command
    matches: git push .*--force
---
Never force-push a shared branch. Rewrite locally and open a new pull request.
```

Fire a synthetic event at it to prove it matches:

```bash
handrail test PreToolUse --kind shell --field command='git push --force origin main'
```

```text
block  no-force-push  project-personal
  Never force-push a shared branch. Rewrite locally and open a new pull request.

outcome: block
```

## Where to start

Run `handrail check` first. It validates every rule on disk and prints what is actually in effect in this repo, which is the answer to "why did that not fire?" nine times out of ten. With no rules yet it prints nothing and exits 0.

Adding a rule needs no re-sync, so `check` is also the command you run while writing one.

```bash
handrail check
```

```text
TIER              RULE           EVENT       KIND       ACTION  STATUS
global            no-force-push  PreToolUse  shell      block   shadowed by project-personal
global            no-secrets     PreToolUse  file_edit  block   shadowed by project-personal
project-personal  no-force-push  PreToolUse  shell      block   enabled
project-personal  no-secrets     -           *          warn    disabled
```

The rule you just wrote is the third row. The two `global` rows are rules this repo overrode: the first lost to your file, and the second was switched off by the `enabled: false` stub on the last row.

Starting from nothing, the sequence is:

1. Install the plugin, per the block above, and start a new session.
2. Describe a guardrail in words to `/handrail:add`, or write the file yourself.
3. `handrail check` to confirm it loaded and is not shadowed.
4. `handrail test` to prove it matches the call you meant to catch.

## Commands

| Command | Does |
|---|---|
| `check` | Validate every tier and print the effective ruleset, annotated with tier, shadowing, and disabling. |
| `test <event>` | Dry-run one event against the rules. Exit 2 when the outcome is block. |
| `sync` | Write handrail's hook entries into every detected harness. The plugin runs this for you after a fresh install. |
| `advise [rule]` | Report which rules also translate into a native harness entry, such as a Claude Code permission deny, for you to paste into your own config. |
| `trust` | Grant this repo's committed `.handrail/` rules permission to take effect. |
| `import hookify` | Convert upstream hookify rule files into personal rules, reporting anything the format cannot express. |
| `doctor` | Diagnose the install offline. The first thing to run when nothing fires. |
| `version` | Version, commit, and build date. |

`handrail hook` also exists. Sync installs it, harnesses call it, and you never type it.

## Rule tiers

Rules live in three places, and the most specific one wins.

| Tier | Location | Applies to |
|---|---|---|
| Global | `~/.config/handrail/` | Every project on this machine. |
| Project-shared | `.handrail/` | The repo, committed for the team. Needs `handrail trust`. |
| Project-personal | `.handrail/local/` | Just you, kept out of version control. |

A rule in a higher tier replaces a lower one of the same filename outright; there is no field-by-field merge. To switch an inherited rule off, shadow it with a stub carrying only `enabled: false`, which is the `disabled` row in the `check` output above.

## Codex CLI

The same plugin, from the same marketplace:

```bash
codex plugin marketplace add svyatov/handrail
codex plugin add handrail@handrail
```

Codex holds every non-managed hook until you approve it in its hooks screen, so expect that review before anything fires.

Where a harness cannot do what a rule asks, sync substitutes the strongest action that harness does support and reports the substitution. It never degrades a rule silently.

## Installing the binary yourself

The plugin fetches the binary for you, so this is for using handrail without a plugin, or for pinning your own version.

Homebrew, on macOS:

```bash
brew install svyatov/tap/handrail
handrail sync
```

On Linux, take the tarball for your architecture from [the releases page](https://github.com/svyatov/handrail/releases), or build it:

```bash
go install github.com/svyatov/handrail@latest
handrail sync
```

Prebuilt binaries cover macOS and Linux on amd64 and arm64. There is no Windows build.

## Documentation

- [`docs/spec.md`](docs/spec.md) is the behavioural source of truth: the event model, the rule format in full, every operator, and the per-harness capability matrix.
- [`CONTEXT.md`](CONTEXT.md) defines the vocabulary.
- [`docs/adr/`](docs/adr/) records why each decision went the way it did.

## Questions and bugs

Both go to [GitHub issues](https://github.com/svyatov/handrail/issues).

handrail is maintained and pre-1.0. The rule format and the nine-command surface are settled for v1; `docs/spec.md` records what was left out and why.

## License

[MIT](LICENSE).
