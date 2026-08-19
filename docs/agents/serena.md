# Serena

[Serena](https://github.com/oraios/serena) gives an agent LSP-backed navigation over this
repo's Go code: find a symbol, find its references, replace its body.

It is optional. Nothing in `task test`, `task lint`, or CI touches it. The repo builds
without it.

## Setup

Serena needs `gopls` on PATH. It does not install it:

```bash
go install golang.org/x/tools/gopls@latest
```

Start the server with the `claude-code` context. That context drops Serena's own file
and shell tools, which duplicate the ones a harness already has:

```bash
serena start-mcp-server --context=claude-code --project-from-cwd
```

On first load Serena writes `.serena/`, detects Go, and starts `gopls`. To confirm the
setup, run `serena project health-check .`. It ends in `Health check passed`.

## `.serena/` is not committed

Serena rewrites `.serena/project.yml` into its own annotated form every time it loads the
project. A hand-edited file does not survive that. The whole directory is gitignored, so
there is nothing to keep in sync. This page is the setup, and Serena generates the rest.

To override a setting for yourself, write it to `.serena/project.local.yml`. That file
takes precedence over `project.yml` key by key, and Serena does not regenerate it.

## What is deliberately unset

- **`ignored_paths`**. The default `ignore_all_files_in_gitignore: true` already covers
  the built binary, `cover.out`, `dist/`, and the worktree directory. The symbol index
  reads only `.go` files, of which this repo has fifteen.
- **`excluded_tools`**. The `claude-code` context already drops the duplicated tools.
- **`initial_prompt` and `memories/`**. `CLAUDE.md`, `CONTEXT.md`, and the rest of
  `docs/agents/` carry the vocabulary, the source-of-truth pointers, and the commands.
  Filling Serena's knowledge layer would duplicate them.
- **`activation_command` and `ls_specific_settings`**. Serena honours both only for a
  project matched by `trusted_project_path_patterns` in the user's global
  `serena_config.yml`.

Upstream documents no Go-specific settings beyond requiring `gopls`.
