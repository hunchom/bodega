---
description: Verify the integrity of the installed package set and surface issues
allowed-tools: Bash(yum:*), Bash(/opt/homebrew/bin/yum:*), Bash(which:*)
---

Run bodega's integrity check against the installed Homebrew prefix and report any problems.

Run: `yum verify --json`

If the command fails with `yum: not found`, tell the user to run `go install ./cmd/yum` from the bodega repo.

Parse the JSON output and group issues by category, rendering each group as a markdown section:
- **Missing files** — formulae whose installed files are gone
- **Broken symlinks** — links pointing to nowhere
- **Checksum mismatches** — files that don't match their recorded hash
- **Missing dependencies** — installed packages whose deps aren't installed
- **Orphaned files** — files with no owning package

Under each group, list the affected package(s) and a one-line detail per issue.

If everything is clean, say so plainly. If there are problems, recommend `yum verify --fix` as the next step (but do not run it automatically — the user should approve).
