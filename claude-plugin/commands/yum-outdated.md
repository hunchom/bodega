---
description: Show every installed package that has a newer version available
allowed-tools: Bash(yum:*), Bash(/opt/homebrew/bin/yum:*), Bash(which:*)
---

List every installed package that is behind its latest available version.

Run: `yum outdated --json`

If the command fails with `yum: not found`, tell the user to run `go install ./cmd/yum` from the bodega repo.

Parse the JSON and render a markdown table: **Name | Installed | Latest | Pinned?**. After the table, suggest:
- `yum upgrade` — upgrade everything at once
- `yum upgrade <pkg>` — upgrade a specific package
- `yum info <pkg>` — inspect before upgrading if anything looks risky

If nothing is outdated, say so plainly — no table needed.
