# yum

A modern package manager for macOS. yum/dnf command surface over Homebrew — with rollback, real transaction history, dep-tree visualization, manifest export/apply, and a proper TUI.

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

## Install

    git clone <repo> ~/src/yum
    cd ~/src/yum
    make install

## Command reference

| command | what it does |
|---|---|
| `install`, `reinstall`, `remove` / `erase`, `autoremove` | mutate |
| `update` / `upgrade`, `check-update` / `outdated` | upgrades |
| `search`, `info`, `list [selector]`, `provides`, `deplist` / `tree`, `why`, `size` | inspect |
| `pin`, `unpin`, `clean [all]`, `repolist` | housekeeping |
| `history`, `history info <id>`, `history undo <id>`, `rollback [id]` | undo |
| `sync`, `doctor`, `manifest export`/`apply`, `completions`, `version` | meta |

## Global flags

`--json` · `--yes/-y` · `--no-color` · `--debug` · `--dry-run` · `--config <path>`

## Data

- Config: `~/.config/yum/config.toml`
- Journal: `~/.local/share/yum/history.db`

## License

MIT
