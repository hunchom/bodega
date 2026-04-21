---
description: Show the recent transaction history across all packages
allowed-tools: Bash(yum:*), Bash(/opt/homebrew/bin/yum:*), Bash(which:*)
---

Show the most recent entries from bodega's global transaction journal.

Run: `yum history --json | head`

If the command fails with `yum: not found`, tell the user to run `go install ./cmd/yum` from the bodega repo.

Parse the JSON stream and render a markdown table with: **ID | Timestamp | Action | Packages | Result**. Keep `Packages` short — for multi-package transactions, show the first 3 plus a `+N more` tail.

After the table, remind the user:
- `yum history undo <id>` rolls a transaction back
- `yum log <pkg>` drills into a single package's history
