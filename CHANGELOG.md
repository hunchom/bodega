# Changelog

All notable changes to this project are documented here. Format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/); versioning follows [SemVer](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Native Go bottle installer** (`install`) — resolves deps from brew's JWS API cache, downloads via GHCR OAuth2, extracts tar.gz, relocates Mach-O binaries via `install_name_tool` + re-codesigning, links into `$PREFIX`. ~4× faster than `brew install` for bottled formulae. Falls back to `brew install` for casks / source builds.
- **Native `remove`** — Cellar teardown + symlink unlink + opt-pointer cleanup, with reverse-dep guard. Transparent brew fallback.
- **`yum log <pkg>`** — per-package event history from the journal (`--json`, `--limit N`).
- **`yum verify`** — install-tree integrity: missing runtime deps, broken symlinks, orphaned Cellar versions, stale pins. Supports `--fix` (dangling symlinks), `--json`.
- **`yum duplicates`** — list formulae with multiple installed versions; `--prune` keeps the newest and unlinks/removes the rest (journaled as `prune-duplicates`).
- **`yum services`** — wraps `brew services` with normalized status enum and `--json` / colored status column; journals mutations as `services-<action>`.
- **`yum browse`** — interactive TUI (bubbletea + lipgloss). Two panes (list + detail), live filter, scope cycling (installed / outdated / leaves / all), inline install/remove/upgrade via `enter`/`r`/`u`, formula viewer on `v`, help overlay on `?`, clipboard yank on `y`.
- **Richer `yum search`** — ranks by exact / prefix / substring name match, then description, then tap; `--deps` to include reverse-dep expansion; `--name-only` for legacy behavior; `--limit N` cap; `★` prefix marks non-name matches.
- **Claude Code plugin** (`claude-plugin/`) — 12 slash commands, `package-doctor` proactive subagent, `yum-packages` skill, MCP server wired in via `.mcp.json`.
- **MCP server** (`mcp-server/`) — Anthropic-native TypeScript using `@modelcontextprotocol/sdk` + zod. 12 tools, 3 static resources + 2 templates, 2 prompts. Claude can call `yum_install`, `yum_search`, etc. as native tools mid-conversation.

### Fixed

- **Streamed brew failures now report the real reason instead of `exit N`.** `yum update` / `upgrade` / `reinstall` / `remove` and tap refresh (`brew update`) captured brew's stderr only to discard it; a failed `brew upgrade` surfaced a bald `✗ brew upgrade: exit 1`. The streaming path now tees stderr and surfaces brew's actual `Error:` line, matching the `brewErr` behavior of the non-streamed commands.
- `yum upgrade` / `update` now stream brew's progress live instead of buffering it silently (a long upgrade no longer looks frozen). Failure output is no longer suppressed by the `parallel` default; set `YUM_DEBUG=1` to always dump the full captured output.
- `yum upgrade --json` on a whole-system upgrade no longer reports `{"upgraded":[],"failed":[]}` on failure — the payload carries a top-level `error`.
- Interrupted transactions (Ctrl-C mid-mutation) are no longer journaled as `✓`. `End()` finalizes on a detached context so the terminal exit code always lands; a process killed before `End()` ran now renders `⚠` (incomplete) rather than a fake success.
- Native partial removes/installs now journal the packages that actually changed on disk (via a typed partial-error), so `yum history undo` can reverse real changes instead of recording an all-failed, un-undoable transaction.
- `yum rollback` / `history undo` now continues through every step (was bailing on the first failure, leaving the rest un-reverted), journals the undo as its own reversible transaction, and exits non-zero on any failed or wholly-unrevertable (downgrade) rollback.
- `yum sync` now journals its autoremoved packages (so a sync is partially undoable), aborts when the tap refresh fails instead of upgrading against stale metadata and claiming success, and respects `--json` (no more human chatter leaking into machine output). Its cleanup step is gated by the now-functional `auto_cleanup` config (default on).
- `yum doctor` surfaces an error when `brew` can't be run (was reporting "system healthy"), and scans both stdout and stderr for findings.
- **MCP parity:** `yum_verify` returns its report even when the tree has issues (the CLI exits 1 with the report on stdout; the server was discarding it), and `yum_install` / `yum_upgrade` / `yum_remove` surface the structured partial-failure payload (which packages succeeded vs failed) instead of collapsing to one opaque error. Fixed a stale `yum_remove --force` unit test left over from dropping the flag; added the MCP server (typecheck + tests) to CI.
- Install scripts hardened: `build.sh` no longer aborts the (succeeded) CLI install when the optional MCP install fails, no longer truncates a good zsh completion file when generation fails, and warns when npm's global bin is off PATH; `patch-zshrc.sh` writes atomically so a failed rewrite can't truncate `~/.zshrc`.
- `yum sync` now journals its steps and honors `--dry-run` (previously invisible to history and would execute on dry-run).
- `yum manifest apply` now journals installed formulae, casks, and pins under verb `manifest-apply`.
- `yum verify` orphan detection uses semver comparison (was string comparison — flagged 1.10 as older than 1.9).
- `brew.Autoremove`, `Pin`, and `Cleanup` now invalidate the info cache after mutating.
- `--dry-run` short-circuits `maybeRefreshTaps` so simulated runs don't touch taps.
- `outdated --json` no longer leaks "taps stale — refreshing" human chatter.

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
- CLT-free install via `./build.sh --install` (pure Go build; no Xcode command-line tools required). The same script handles build, install, uninstall, and help.
- Shell completions for zsh, bash, fish, powershell.
- Golden-file tests for table, panel, tree, and root help output.

### Design

- Single accent color (muted amber). No emojis in output. Thin single-weight box drawing.
- Pure Go SQLite driver (modernc.org/sqlite) — no CGO, no build-time toolchain dependency beyond `go`.
- Backend abstraction so additional package sources (mas, pipx, cargo) can be compiled in later without changing the command layer.

[Unreleased]: https://github.com/hunchom/bodega/compare/v0.1.0...HEAD
[0.1.0]: https://github.com/hunchom/bodega/releases/tag/v0.1.0
