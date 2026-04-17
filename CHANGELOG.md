# Changelog

All notable changes to this project are documented here. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning follows [SemVer](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.1.0] — 2026-04-17

First public release.

### Added

- yum-parity command surface over Homebrew: `install`, `reinstall`, `remove` / `erase`, `autoremove`, `update` / `upgrade`, `check-update` / `outdated`, `search`, `info`, `list` (with `installed` / `available` / `updates` / `leaves` / `pinned` / `casks` selectors), `provides`, `deplist` / `tree`, `clean`, `repolist`, `history`.
- Features beyond yum: `why` (reverse dependency tree), `size` (per-package disk usage with inline bars), `pin` / `unpin`, `sync` (update + upgrade + autoremove + cleanup), `rollback`, `history info`, `history undo`, `doctor`, `manifest export` / `apply`, `completions` (zsh, bash, fish, powershell), `version`.
- SQLite-backed transaction journal at `~/.local/share/yum/history.db` — every install, remove, upgrade, reinstall, pin is recorded and reversible via `rollback`.
- TOML config at `~/.config/yum/config.toml` (theme, defaults, aliases).
- `--json` on every read-only command for scripting.
- `--yes` / `-y`, `--no-color`, `--debug`, `--dry-run`, `--config` global flags.
- Interactive fuzzy picker for `search -i` (bubbletea list).
- Semver-diff color coding on `outdated` — major = red, minor = amber, patch = green.
- CLT-free install via `./scripts/install.sh` (pure Go build; no Xcode command-line tools required).
- Shell completions for zsh, bash, fish, powershell.
- Golden-file tests for table, panel, tree, and root help output.

### Design

- Single accent color (muted amber). No emojis in output. Thin single-weight box drawing.
- Pure Go SQLite driver (modernc.org/sqlite) — no CGO, no build-time toolchain dependency beyond `go`.
- Backend abstraction so additional package sources (mas, pipx, cargo) can be compiled in later without changing the command layer.

[Unreleased]: https://github.com/hunchom/bodega/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/hunchom/bodega/releases/tag/v0.1.0
