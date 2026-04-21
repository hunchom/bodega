---
description: Install one or more Homebrew formulae via bodega's native fast path
argument-hint: <pkg...>
allowed-tools: Bash(yum:*), Bash(/opt/homebrew/bin/yum:*), Bash(which:*)
---

Install the requested package(s) using `yum install`. Bodega's native bottle installer is ~4x faster than `brew install` for cached/bottled formulae.

Run: `yum install $ARGUMENTS`

If the command fails with `yum: not found` or `command not found`, tell the user to build and install the binary first:

```
cd bodega && go install ./cmd/yum
```

Then parse the successful output and report:
- Which packages were installed and their versions
- Any failures with the error message and likely cause
- The `yum log <pkg>` journal entry that was just recorded, if successful (you can fetch it with `yum log <pkg> --json` for the first package to confirm the transaction landed)

Keep the summary tight — one line per package — and flag any warnings from the installer.
