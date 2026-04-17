# bodega

A modern package manager for macOS. `yum` / `dnf` command surface over Homebrew — with rollback, real transaction history, dependency-tree visualization, manifest export/apply, semver-diff outdated view, and a TUI that isn't embarrassing.

The project is `bodega`. The binary you invoke is `yum`.

```
yum install ripgrep
yum search terminal
yum outdated
yum tree openssl@3
yum why openssl@3
yum history
yum history info 4
yum rollback 4
yum manifest export > ~/packages.toml
yum doctor
yum sync
```

## Why

Homebrew is fine underneath. Its surface is not. Commands are inconsistent across formulae, casks, and taps. Output is chatty. There is no real transaction history, no rollback, no reverse-dependency tree, no manifest. `yum` and `dnf` nailed the command vocabulary a decade ago — bodega brings that vocabulary to macOS, on top of brew, with the features brew still doesn't have.

## Install

```sh
git clone https://github.com/hunchom/bodega ~/bodega
cd ~/bodega
./scripts/install.sh
```

The installer:

- builds the binary with `go build` (no CGO, no Xcode CLT required)
- drops it at `~/.local/bin/yum`
- installs zsh completions to `~/.zsh/completions/_yum`
- removes the legacy `yum()` shim from `~/.zshrc` if present (with a timestamped backup)

Make sure `~/.local/bin` is on your `PATH`. If you use zsh, add completions to `fpath` in `.zshrc`:

```sh
fpath=(~/.zsh/completions $fpath)
autoload -U compinit && compinit
```

## Command surface

### yum-parity

| command | behavior |
|---|---|
| `install <pkg>...` | Install; formula or cask, auto-detected |
| `reinstall <pkg>...` | Reinstall |
| `remove <pkg>...` / `erase` | Uninstall |
| `autoremove` | Prune orphaned dependencies |
| `update` / `upgrade [pkg]...` | Update taps + upgrade packages |
| `check-update` / `outdated` | List outdated, semver-diff colored |
| `search <term>` | Fuzzy search across formulae + casks; interactive picker with `-i` |
| `info <pkg>` | Rich panel: version, tap, deps, homepage, pinned state |
| `list [selector]` | `installed`, `available`, `updates`, `leaves`, `pinned`, `casks` |
| `provides <cmd>` | Which formula installs this command |
| `deplist <pkg>` / `tree <pkg>` | Forward dependency tree |
| `clean [all]` | `brew cleanup`; `all` = `--prune=all` |
| `repolist` | Active taps |
| `history` | Transaction history (journal) |

### Beyond yum

| command | behavior |
|---|---|
| `why <pkg>` | Reverse-dep tree: what pulled this in |
| `size [pkg]` | Disk usage per package, sorted, with inline bars |
| `pin <pkg>` / `unpin <pkg>` | Lock / unlock versions |
| `sync` | update + upgrade + autoremove + cleanup, one transaction |
| `rollback [id]` | Undo last transaction (or specific id) |
| `history info <id>` / `history undo <id>` | Inspect / invert a transaction |
| `doctor` | `brew doctor` + PATH conflicts + broken symlinks, each with fix hints |
| `manifest export [file]` | Snapshot state to TOML (taps, formulae, casks, pins) |
| `manifest apply <file>` | Reconcile system to a manifest |
| `completions <shell>` | zsh / bash / fish / powershell |
| `version` | Build info |

### Global flags

`--json` · `--yes` / `-y` · `--no-color` · `--debug` · `--dry-run` · `--config <path>`

`--json` works on every read command, so bodega composes with `jq`, scripts, and anything else that consumes JSON.

## Data & config

| path | what |
|---|---|
| `~/.config/yum/config.toml` | Theme, defaults, aliases. Loaded if present. |
| `~/.local/share/yum/history.db` | SQLite transaction journal for history + rollback |
| `~/.local/share/yum/cache/` | Search index cache |

(The binary is `yum`, so its data lives under `yum/` on disk. The project itself is `bodega`.)

Example `config.toml`:

```toml
[ui]
theme = "amber"
confirm_destructive = true

[defaults]
parallel = true

[aliases]
i = "install"
rm = "remove"
up = "upgrade"
```

## Development

```sh
./scripts/build.sh         # compile ./yum in place
go test ./...              # run the unit tests
./yum <command>            # run without installing
./scripts/install.sh       # install to ~/.local/bin
```

The source tree is small on purpose:

```
cmd/yum/                   entrypoint
internal/backend/          backend interface and brew adapter
internal/cmd/              cobra command definitions — one file per command
internal/config/           TOML config loader
internal/journal/          SQLite transaction log + rollback planner
internal/runner/           exec abstraction (swappable for tests)
internal/semver/           semver-diff classification for `outdated` coloring
internal/ui/               tables, panels, trees, progress, picker (lipgloss/bubbletea)
internal/ui/theme/         single-accent palette
```

## Requirements

- macOS (Apple Silicon or Intel)
- [Homebrew](https://brew.sh) at `/opt/homebrew` or `/usr/local`
- Go 1.26+ to build from source

## Design decisions

- **No emojis in output.** Glyphs only (`•`, `→`, `✓`, `✗`).
- **One accent color.** Muted amber. No rainbow fallback.
- **No banners, no splash.** Help is columnar, not prose.
- **Errors are one line** unless `--debug`.
- **`--json` is first-class** on every read command; pipe anything into anything.
- **No daemon, no web UI, no plugin system.** Backends are compiled in.

## License

MIT — see [LICENSE](LICENSE).
