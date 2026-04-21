# bodega — Claude Code plugin

Manage macOS Homebrew packages from inside Claude Code using bodega's `yum` CLI.

The plugin ships slash commands, an agent, a skill, **and an MCP server** so Claude can call every yum operation natively during a conversation.

## Prerequisites

The `yum` binary must be on PATH. Build it from the bodega repo:

```
cd bodega
go install ./cmd/yum
```

It lands at `$HOME/.local/bin/yum` or `/opt/homebrew/bin/yum` depending on your `GOBIN`. If a slash command here ever fails with `yum: not found`, run the snippet above and retry.

### MCP server (for native Claude tool calls)

The plugin's `.mcp.json` points at a `bodega-mcp` binary. Build and link it once:

```
cd bodega/mcp-server
npm install
npm run build
npm link          # creates /opt/homebrew/bin/bodega-mcp
```

Claude Code picks up `.mcp.json` automatically when the plugin is installed.

## Installation

Once this plugin is published to a marketplace:

```
/plugin install bodega
```

For local development against the repo copy, point Claude Code at the plugin directory directly (e.g. add it to `~/.claude/plugins/` or register it via a local marketplace config pointing at `path/to/bodega/claude-plugin`).

## What you get

### Slash commands

| Command | What it does |
|---------|--------------|
| `/yum-install <pkg...>` | Install one or more formulae via bodega's fast native bottle path |
| `/yum-remove <pkg...>` | Remove packages, recording the removal in the journal |
| `/yum-search <term>` | Search the Homebrew index and render a results table |
| `/yum-info <pkg>` | Show metadata, deps, and install state for one package |
| `/yum-list [filter]` | List all / installed / outdated / leaves |
| `/yum-outdated` | Show every installed package with a newer version upstream |
| `/yum-upgrade [pkg...]` | Upgrade everything, or a specific list |
| `/yum-log <pkg>` | Per-package transaction history |
| `/yum-verify` | Integrity check the install tree |
| `/yum-history` | Recent transactions across all packages |
| `/yum-duplicates` | Packages installed under multiple versions simultaneously |
| `/yum-services [action] [name]` | List / start / stop / restart background services |

### Agent

- **package-doctor** — autonomously triages "command not found" errors, broken `brew`/`yum` runs, and stale dependency complaints. It runs `yum verify`, `yum duplicates`, and `yum log` as needed, and proposes a fix instead of applying one.

### Skill

- **yum-packages** — loads when the user's intent is package management. Reminds Claude that `yum` is faster than `brew` for bottled formulae, that every mutation is journaled, and that `yum history undo <id>` exists.

### MCP tools (12)

Served by `bodega-mcp` via the plugin's `.mcp.json`. Claude can call these natively without invoking a slash command:

| Tool | Purpose |
|------|---------|
| `yum_install` | Install one or more formulae via the fast bottle path |
| `yum_remove` | Uninstall packages (journaled, reversible) |
| `yum_search` | Search the Homebrew index |
| `yum_info` | Metadata, deps, and install state for one package |
| `yum_list` | List installed / available / outdated / leaves / pinned / casks |
| `yum_outdated` | Installed packages with a newer upstream version |
| `yum_upgrade` | Upgrade everything or a given subset |
| `yum_log` | Per-package transaction journal |
| `yum_verify` | Integrity check, with optional auto-fix |
| `yum_history` | Recent transactions across all packages |
| `yum_duplicates` | Packages installed under multiple versions at once |
| `yum_services` | List / start / stop / restart launchd-managed services |

### MCP resources

- `yum://installed` — JSON array of installed packages
- `yum://installed/{name}` — metadata for one installed package
- `yum://outdated` — current outdated list
- `yum://log/{name}` — per-package event history
- `yum://verify` — current integrity report

### MCP prompts

- `diagnose` — guided triage of a failing command (verify → duplicates → log → history → repair plan)
- `cleanup` — audit leaves, duplicates, and outdated packages, then propose a pruning plan

## Why bodega

- **~4x faster installs** for bottled formulae vs. `brew install` (native Go downloader, parallel, cached).
- **Transactional rollback** — every install, remove, and upgrade is recorded; broken upgrades are one `yum history undo` away.
- **yum/dnf command surface** — familiar from Fedora/RHEL.
- **Integrity checks** that Homebrew doesn't ship (`yum verify`, `yum duplicates`).

## Links

- Main repo: [github.com/hunchom/bodega](https://github.com/hunchom/bodega)
- License: MIT

## Constraints

Every slash command in this plugin restricts `allowed-tools` to `Bash(yum:*)`, `Bash(/opt/homebrew/bin/yum:*)`, and `Bash(which:*)` only — no broad shell access. The plugin cannot run arbitrary commands on your system.
