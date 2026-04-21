---
description: Search Homebrew formulae and casks by name or description
argument-hint: <term>
allowed-tools: Bash(yum:*), Bash(/opt/homebrew/bin/yum:*), Bash(which:*)
---

Search bodega's index for packages matching the query.

Run: `yum search --limit 20 $ARGUMENTS`

If the command fails with `yum: not found`, tell the user to run `go install ./cmd/yum` from the bodega repo.

Render the results as a compact markdown table with these columns:
- **Name** — package name
- **Version** — latest available version
- **Kind** — formula or cask
- **Description** — short description, truncated to ~80 chars

If there are zero matches, suggest a broader term. If there are exactly 20 (the limit), tell the user to refine or re-run with a higher `--limit` flag.
