# bodega

Package manager for macOS. `yum` / `dnf` command surface over Homebrew with a native Go bottle installer (~4× faster than `brew install`), rollback, transaction history, dependency trees, semver-diff on outdated, a TOML manifest, an interactive TUI, a Claude Code plugin, and a Model Context Protocol server.

Project: `bodega`. Binary: `yum`.

```sh
yum install ripgrep            # native fast path, bottle via GHCR
yum browse                     # interactive TUI
yum search "text editor" --deps
yum outdated
yum log ripgrep                # per-package event history
yum verify                     # integrity check: missing deps, broken symlinks, orphans
yum duplicates --prune         # collapse multiple cellar versions
yum services                   # launchd services
yum history                    # every transaction
yum rollback                   # undo the last one
yum manifest export > packages.toml
```

## Install

```sh
git clone https://github.com/hunchom/bodega ~/bodega
cd ~/bodega
./build.sh --install
```

Binary lands at `~/.local/bin/yum`; zsh completions at `~/.zsh/completions/_yum`. Any legacy `yum()` function in `~/.zshrc` is removed with a timestamped backup.

Make sure `~/.local/bin` is on `PATH`. For zsh completions:

```sh
fpath=(~/.zsh/completions $fpath)
autoload -U compinit && compinit
```

## Commands

**yum parity.** `install`, `reinstall`, `remove` / `erase`, `autoremove`, `update` / `upgrade`, `check-update` / `outdated`, `search`, `info`, `list [installed|available|updates|leaves|pinned|casks]`, `provides`, `deplist` / `tree`, `clean`, `repolist`, `history`.

**Beyond yum.** `why` (reverse deps), `size` (per-package disk usage), `pin` / `unpin`, `sync` (update + upgrade + autoremove + cleanup), `rollback`, `history info <id>`, `history undo <id>`, `log <pkg>` (per-package event history), `verify` (integrity check), `duplicates` (multi-version cellar), `services` (launchd), `browse` (interactive TUI), `doctor`, `manifest export` / `apply`, `completions <shell>`.

**Global flags.** `--json`, `--yes` / `-y`, `--no-color`, `--debug`, `--dry-run`, `--refresh`, `--no-refresh`, `--config <path>`.

`--json` works on every read command and on `verify`, `log`, `duplicates`, `services list`, `history`.

## Claude Code integration

bodega ships a Claude Code plugin under [`claude-plugin/`](./claude-plugin) with 12 slash commands (`/yum-install`, `/yum-search`, …), a proactive `package-doctor` subagent, and a `yum-packages` skill.

It also ships an **Anthropic-native MCP server** under [`mcp-server/`](./mcp-server) — TypeScript, `@modelcontextprotocol/sdk`, zod schemas, 12 tools, 3 static resources + 2 templates, 2 prompts. Once installed, Claude can call `yum_install`, `yum_search`, etc. as native tools mid-conversation.

```sh
cd mcp-server && npm install && npm run build && npm link   # bodega-mcp on PATH
```

## Data

| path | content |
|---|---|
| `~/.config/yum/config.toml` | theme, defaults, aliases |
| `~/.local/share/yum/history.db` | transaction journal |
| `~/.cache/yum/info/` | 5-minute info-lookup cache |

## Build script

```sh
./build.sh                 # build and prompt to install
./build.sh --install       # build and install, no prompt
./build.sh --no-install    # build only
./build.sh --clean         # remove artifacts, test cache, info cache
./build.sh --uninstall     # remove installed binary and completions
./build.sh --help
```

## Requirements

- macOS (Apple Silicon or Intel)
- [Homebrew](https://brew.sh) at `/opt/homebrew` or `/usr/local`
- Go 1.26+

## Layout

```
cmd/yum/            entrypoint
internal/backend/   backend interface and brew adapter (native bottle install, GHCR, link/unlink, resolver)
internal/cmd/       cobra commands (one per file)
internal/config/    TOML loader
internal/journal/   SQLite transaction log + rollback planner
internal/runner/    exec abstraction
internal/semver/    semver-diff classification
internal/tui/       interactive TUI (`yum browse` via bubbletea + lipgloss)
internal/ui/        tables, panels, trees, picker, theme
internal/verify/    install-tree integrity checks (`yum verify`)
claude-plugin/      Claude Code plugin (slash commands + agents + skills)
mcp-server/         Model Context Protocol server (TypeScript)
```

## License

MIT — see [LICENSE](LICENSE).
