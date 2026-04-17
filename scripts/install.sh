#!/usr/bin/env bash
# CLT-free installer for yum.
# Builds via ./scripts/build.sh (plain `go build`, no Xcode CLT required),
# installs the binary to ~/.local/bin and the zsh completion to ~/.zsh/completions,
# then runs the legacy-yum zshrc patcher. Safe to re-run.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

BIN_DIR="${HOME}/.local/bin"
COMP_DIR="${HOME}/.zsh/completions"
BINARY="yum"

# 1. Build
./scripts/build.sh

# 2. Install binary
install -d "$BIN_DIR"
install -m 0755 "./${BINARY}" "${BIN_DIR}/${BINARY}"

# 3. Install zsh completion
install -d "$COMP_DIR"
"./${BINARY}" completions zsh > "${COMP_DIR}/_${BINARY}"

# 4. Patch ~/.zshrc (idempotent — no-op if already clean)
./scripts/patch-zshrc.sh || true

# 5. Success banner
VERSION_LINE="$("./${BINARY}" version 2>/dev/null | head -n1 || echo "version unknown")"
echo
echo "yum installed"
echo "  ${VERSION_LINE}"
echo "  binary      → ${BIN_DIR}/${BINARY}"
echo "  completion  → ${COMP_DIR}/_${BINARY}"
echo
echo "run 'hash -r' or open a new shell, then try: yum --help"
