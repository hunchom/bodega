---
description: List or control Homebrew background services (launchd agents)
argument-hint: "[list|start|stop|restart|run] [name]"
allowed-tools: Bash(yum:*), Bash(/opt/homebrew/bin/yum:*), Bash(which:*)
---

Manage Homebrew background services (the launchd-managed agents that `brew services` controls).

Run: `yum services $ARGUMENTS`

With no arguments, bodega prints the list of services with their status (`started` / `stopped` / `error`) and the user they run as.

Actions you can pass as the first argument:
- `list` — same as running with no args
- `start <name>` — start the named service
- `stop <name>` — stop it
- `restart <name>` — stop then start
- `run <name>` — run once without registering it with launchd

If the command fails with `yum: not found`, tell the user to run `go install ./cmd/yum` from the bodega repo.

For `list`, render a markdown table: **Name | Status | User | Plist**. For any control verb, report what changed and the resulting status (the command should print this itself — just pass it through cleanly).
