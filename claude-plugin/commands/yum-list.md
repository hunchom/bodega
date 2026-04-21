---
description: List packages, optionally filtered by state (installed, outdated, leaves)
argument-hint: "[installed|outdated|leaves]"
allowed-tools: Bash(yum:*), Bash(/opt/homebrew/bin/yum:*), Bash(which:*)
---

List packages, optionally narrowed by state.

Run: `yum list --json $ARGUMENTS`

The optional argument is one of:
- `installed` — everything currently installed
- `outdated` — installed packages that have a newer version upstream
- `leaves` — installed packages that nothing else depends on (candidates for cleanup)
- (empty) — every known package

If the command fails with `yum: not found`, tell the user to run `go install ./cmd/yum` from the bodega repo.

Parse the JSON and render a concise markdown table: **Name | Installed Version | Latest Version | Kind**. If the list is longer than ~50 rows, summarize with counts per kind and show the first 25 plus the last 5, or ask the user to narrow the filter.
