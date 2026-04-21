---
description: Show detailed information about a package (version, deps, install state)
argument-hint: <pkg>
allowed-tools: Bash(yum:*), Bash(/opt/homebrew/bin/yum:*), Bash(which:*)
---

Fetch detailed metadata for a single package.

Run: `yum info --json $ARGUMENTS`

If the command fails with `yum: not found`, tell the user to run `go install ./cmd/yum` from the bodega repo.

Parse the JSON output and render a clean markdown summary with these sections:
- **Name & version** — currently installed version (if any) vs. latest
- **Description** — one-line summary
- **Homepage / license** — if present
- **Dependencies** — runtime deps, bulleted
- **Install state** — installed / not installed, install date if known, install path
- **Bottled** — yes/no, and size if known

Keep it compact. If the package is not found, say so and suggest `yum search <term>` to find similar names.
