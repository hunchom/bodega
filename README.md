# bodega

Package manager for macOS. Wraps Homebrew with a `yum` / `dnf` command surface and adds rollback, transaction history, dependency trees, semver-diff on outdated, and a TOML manifest.

Project: `bodega`. Binary: `yum`.

```sh
yum install ripgrep
yum outdated
yum tree openssl@3
yum why openssl@3
yum history
yum rollback
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

**Beyond yum.** `why` (reverse deps), `size` (per-package disk usage), `pin` / `unpin`, `sync` (update + upgrade + autoremove + cleanup), `rollback`, `history info <id>`, `history undo <id>`, `doctor`, `manifest export` / `apply`, `completions <shell>`.

**Global flags.** `--json`, `--yes` / `-y`, `--no-color`, `--debug`, `--dry-run`, `--refresh`, `--no-refresh`, `--config <path>`.

`--json` works on every read command.

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
internal/backend/   backend interface and brew adapter
internal/cmd/       cobra commands (one per file)
internal/config/    TOML loader
internal/journal/   SQLite transaction log + rollback planner
internal/runner/    exec abstraction
internal/semver/    semver-diff classification
internal/ui/        tables, panels, trees, progress, picker
```

## License

MIT — see [LICENSE](LICENSE).
