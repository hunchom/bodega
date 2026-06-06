---
description: Verify the integrity of the installed package set and surface issues
allowed-tools: Bash(yum:*), Bash(/opt/homebrew/bin/yum:*), Bash(which:*)
---

Run bodega's integrity check against the installed Homebrew prefix and report any problems.

Run: `yum verify --json`

If the command fails with `yum: not found`, tell the user to run `./build.sh --install` from the bodega repo (it places `yum` at `~/.local/bin/yum`).

The output is `{"issues": [...], "passed": bool}`. Note `yum verify` exits non-zero when `passed` is false, but the JSON report is still printed to stdout — parse it regardless of exit code.

Group issues by the JSON `kind` field, rendering each group as a markdown section:
- `missing-dep` — **Missing dependencies**: installed formulae whose deps aren't installed
- `broken-symlink` — **Broken symlinks**: links pointing to nowhere
- `orphaned` — **Orphaned Cellar versions**: stale `Cellar/<pkg>/<ver>` dirs that aren't the active version
- `stale-pin` — **Stale pins**: a pinned formula no longer present at its pinned version
- `unreadable` — **Unreadable paths**: a Cellar entry that couldn't be read

Render any unrecognized `kind` under an **Other** section rather than dropping it, so future checks don't vanish. Under each group, list the affected package(s)/path and a one-line detail per issue.

If everything is clean, say so plainly. If there are problems, recommend `yum verify --fix` as the next step (but do not run it automatically — the user should approve).
