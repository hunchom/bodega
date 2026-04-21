---
description: Remove one or more Homebrew formulae and record a journaled transaction
argument-hint: <pkg...>
allowed-tools: Bash(yum:*), Bash(/opt/homebrew/bin/yum:*), Bash(which:*)
---

Remove the requested package(s) using `yum remove`. The removal is recorded in bodega's transaction journal, so the user can roll it back with `yum history undo <id>` if they regret it.

Run: `yum remove $ARGUMENTS`

If the command fails with `yum: not found`, tell the user to run `go install ./cmd/yum` from the bodega repo.

After a successful removal, report:
- Which packages were removed and their prior versions
- The transaction ID that was recorded (so the user knows what to `yum history undo` if needed)
- Any dependent packages that are now orphaned (mention `yum autoremove` as a follow-up if so)

If removal fails because the package is depended on by something else, name the dependent and suggest removing it first or using `--force` only if the user understands the consequences.
