#!/usr/bin/env bash
# Remove the legacy yum() function from ~/.zshrc, if present.
# Safe to run multiple times. Creates a timestamped backup on first change.
set -euo pipefail
ZSHRC="${HOME}/.zshrc"
[[ -f "$ZSHRC" ]] || { echo "no ~/.zshrc"; exit 0; }

if ! grep -q '^yum() {' "$ZSHRC"; then
    echo "no legacy yum() in .zshrc — nothing to do"
    exit 0
fi

BACKUP="${ZSHRC}.yum-$(date +%Y%m%d-%H%M%S).bak"
cp "$ZSHRC" "$BACKUP"
echo "backed up → $BACKUP"

# Delete from "# ─── yum → brew shim" or "yum() {" marker down through its closing '}'
awk '
  /^# ─── yum → brew shim/ { skip=1; next }
  skip && /^}/             { skip=0; next }
  skip                     { next }
  /^yum\(\) \{/            { skip=1; next }
  { print }
' "$BACKUP" > "$ZSHRC"

echo "patched ~/.zshrc — old yum() removed"
echo 'reload shell or run: source ~/.zshrc'
