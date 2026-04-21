---
description: Upgrade all outdated packages, or a specific list
argument-hint: "[pkg...]"
allowed-tools: Bash(yum:*), Bash(/opt/homebrew/bin/yum:*), Bash(which:*)
---

Upgrade packages. If no arguments are supplied, every outdated package is upgraded. If specific names are provided, only those are upgraded.

Run: `yum upgrade $ARGUMENTS`

If the command fails with `yum: not found`, tell the user to run `go install ./cmd/yum` from the bodega repo.

After the run, report:
- Which packages were upgraded (old version → new version)
- Any that were skipped (pinned, already latest, or failed) with the reason
- The transaction ID for the journal, so the user can `yum history undo <id>` if the new version is broken

If any package failed to upgrade, show the error excerpt and suggest `yum log <pkg>` for the full history.
