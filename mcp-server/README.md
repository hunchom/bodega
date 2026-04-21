# bodega-mcp

Model Context Protocol server that exposes bodega's `yum` package manager as
native tools, resources, and prompts inside Claude Code.

## Surface

### Tools (12)

| Tool | Description |
|---|---|
| `yum_install` | Install one or more Homebrew formulae via bodega's fast bottle path. |
| `yum_remove` | Uninstall packages (journaled; reversible with `yum history undo`). |
| `yum_search` | Search the Homebrew index for matching formulae. |
| `yum_info` | Full metadata for a single package (deps, install state, homepage). |
| `yum_list` | List installed / available / outdated / leaves / pinned / casks. |
| `yum_outdated` | List every installed package with a newer upstream version. |
| `yum_upgrade` | Upgrade all outdated packages, or a specific list. |
| `yum_log` | Per-package transaction journal (install, upgrade, remove events). |
| `yum_verify` | Integrity check the installed tree; optional auto-fix. |
| `yum_history` | Recent transactions across all packages. |
| `yum_duplicates` | Packages with multiple parallel versions installed. |
| `yum_services` | List or control launchd-managed Homebrew services. |

### Resources

- `yum://installed` - JSON array of every installed package.
- `yum://installed/{name}` - full metadata for one installed package.
- `yum://outdated` - current outdated list.
- `yum://log/{name}` - transaction journal for one package.
- `yum://verify` - current integrity-check report (read-only).

### Prompts

- `diagnose` - triage a failing command or library-load error using verify / duplicates / log / history.
- `cleanup` - audit leaves, duplicates, and outdated packages, then propose a pruning plan.

## Install

```
cd mcp-server
npm install
npm run build
npm link        # puts `bodega-mcp` on PATH
```

The `yum` binary must also be on PATH. Build it from the bodega repo root:

```
go install ./cmd/yum
```

## Use from Claude Code

The bodega plugin declares this server in its `.mcp.json`. Once the plugin is
installed and `bodega-mcp` is on PATH, Claude Code will discover the 12 tools,
5 resources, and 2 prompts automatically.

Manually run it for smoke testing:

```
echo '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"smoke","version":"0"}}}' \
  | bodega-mcp
```

## Test

```
npm test
```

Tests run against a fake runner (no real `yum` subprocess) and exercise:
capability negotiation, `tools/list`, `resources/list`, `prompts/list`,
every tool's argument wiring, subprocess-failure surfacing, and prompt
rendering.
