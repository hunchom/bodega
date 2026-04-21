---
description: Show the transaction log for a specific package
argument-hint: <pkg>
allowed-tools: Bash(yum:*), Bash(/opt/homebrew/bin/yum:*), Bash(which:*)
---

Display the per-package transaction journal — every install, upgrade, and removal bodega has recorded for this package.

Run: `yum log --json $ARGUMENTS`

If the command fails with `yum: not found`, tell the user to run `go install ./cmd/yum` from the bodega repo.

Parse the JSON and render a chronological markdown list, most-recent first. Each entry should show:
- **Timestamp** (ISO 8601)
- **Action** (install / upgrade / remove)
- **Version** (for upgrade: `from` → `to`)
- **Transaction ID** — for `yum history undo`
- **Result** (success / failure, and the error excerpt if it failed)

If there are no entries, say so — the package has never been touched through yum.
