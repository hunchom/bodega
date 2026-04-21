---
description: Find packages installed under multiple versions simultaneously
allowed-tools: Bash(yum:*), Bash(/opt/homebrew/bin/yum:*), Bash(which:*)
---

List packages where more than one version is currently installed side-by-side — a common source of stale library linkage and surprising path-resolution bugs.

Run: `yum duplicates --json`

If the command fails with `yum: not found`, tell the user to run `go install ./cmd/yum` from the bodega repo.

Parse the JSON and render a table: **Name | Installed Versions | Linked Version**. The *linked version* is the one currently on `PATH` / in the keg symlinks — the other copies are typically vestigial.

For each row, suggest the cleanup: `yum remove <name>@<old-version>` or `brew cleanup <name>` depending on what's safer. If there are no duplicates, say so — the install tree is clean.
