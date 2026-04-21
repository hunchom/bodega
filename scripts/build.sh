#!/usr/bin/env bash
# bodega build / install / uninstall — one script, three modes.
# Installs the yum CLI (Go) and, when node is available, the bodega-mcp server.
set -euo pipefail

# ── resolve script path through symlinks, cd to repo root ─────────────────────
SCRIPT="${BASH_SOURCE[0]}"
while [ -L "$SCRIPT" ]; do
  DIR="$(cd "$(dirname "$SCRIPT")" && pwd)"
  TARGET="$(readlink "$SCRIPT")"
  case "$TARGET" in /*) SCRIPT="$TARGET" ;; *) SCRIPT="$DIR/$TARGET" ;; esac
done
ROOT="$(cd "$(cd "$(dirname "$SCRIPT")" && pwd)/.." && pwd)"
cd "$ROOT"

# ── config ────────────────────────────────────────────────────────────────────
BINARY="yum"
BIN_DIR="${HOME}/.local/bin"
COMP_DIR="${HOME}/.zsh/completions"
MODULE="github.com/hunchom/bodega"
MCP_DIR="${ROOT}/mcp-server"
MCP_PKG="@bodega/mcp-server"
MCP_BIN="bodega-mcp"

# Known paths the Go binary may have landed in from earlier workflows.
# Order matters only for display — removal hits every match.
KNOWN_YUM_PATHS=(
  "${BIN_DIR}/${BINARY}"
  "${HOME}/go/bin/${BINARY}"
  "/opt/homebrew/bin/${BINARY}"
  "/usr/local/bin/${BINARY}"
)

# ── ANSI (TTY-aware, honors NO_COLOR) ─────────────────────────────────────────
if [ -t 1 ] && [ -z "${NO_COLOR:-}" ]; then
  B=$'\033[1m'; D=$'\033[2m'; R=$'\033[0m'
  A=$'\033[38;5;178m'     # amber accent
  G=$'\033[38;5;108m'     # ok green
  E=$'\033[38;5;167m'     # err red
  M=$'\033[38;5;245m'     # muted grey
else
  B=""; D=""; R=""; A=""; G=""; E=""; M=""
fi

title()  { printf "\n%s%sbodega%s %s%s\n%s%s%s\n\n" "$B" "$A" "$R" "$D" "$1" "$M" "────────────────────────────────────────" "$R"; }
step()   { printf "  %s→%s %s\n" "$M" "$R" "$1"; }
ok()     { printf "  %s✓%s %s\n" "$G" "$R" "$1"; }
warn()   { printf "  %s!%s %s\n" "$A" "$R" "$1"; }
err()    { printf "  %s✗%s %s\n" "$E" "$R" "$1" >&2; }
kv()     { printf "  %s%-12s%s %s\n" "$M" "$1" "$R" "$2"; }
sub()    { printf "  %s└%s %s\n" "$M" "$R" "$1"; }

# ── help ──────────────────────────────────────────────────────────────────────
show_help() {
  cat <<EOF
${B}${A}bodega${R} — build, install, clean, uninstall

${B}usage${R}
  ${A}./build.sh${R}                build ${BINARY}; prompt to install (CLI + MCP)
  ${A}./build.sh ${M}-i, --install${R}   build and install both, no prompt
  ${A}./build.sh ${M}--no-mcp${R}        with -i: install CLI only (skip MCP server)
  ${A}./build.sh ${M}--mcp-only${R}      rebuild and relink MCP server only
  ${A}./build.sh ${M}-n, --no-install${R} build only, skip the prompt
  ${A}./build.sh ${M}-c, --clean${R}     remove build artifacts, test cache, info cache
  ${A}./build.sh ${M}-u, --uninstall${R} remove installed ${BINARY}, completions, MCP link
  ${A}./build.sh ${M}--detect${R}        show what's already installed and exit
  ${A}./build.sh ${M}-h, --help${R}      show this message

${B}paths${R}
  ${M}binary     ${R} ${BIN_DIR}/${BINARY}
  ${M}completion ${R} ${COMP_DIR}/_${BINARY}
  ${M}info cache ${R} \${XDG_CACHE_HOME:-\$HOME/.cache}/yum/info
  ${M}mcp link   ${R} \$(npm prefix -g)/bin/${MCP_BIN}

${B}env${R}
  ${M}NO_COLOR=1 ${R} disable ANSI output
  ${M}FORCE=1    ${R} reinstall without the overwrite prompt
EOF
}

# ── detection ─────────────────────────────────────────────────────────────────
# Populates globals: FOUND_YUM_PATHS (array), FOUND_MCP_GLOBAL (0/1),
# FOUND_MCP_PATH (string), HAVE_NODE (0/1).
detect_existing() {
  FOUND_YUM_PATHS=()
  FOUND_MCP_GLOBAL=0
  FOUND_MCP_PATH=""
  HAVE_NODE=0

  # CLI: check the known dirs plus anything on PATH named $BINARY.
  local seen=""
  for p in "${KNOWN_YUM_PATHS[@]}"; do
    if [ -e "$p" ] || [ -L "$p" ]; then
      FOUND_YUM_PATHS+=("$p")
      seen+="|$p|"
    fi
  done
  # `type -a yum` finds every match on PATH — catch anything in an unusual dir.
  if command -v type >/dev/null 2>&1; then
    while IFS= read -r line; do
      # Parse "yum is /some/path" or "yum is aliased to ..." — skip aliases.
      case "$line" in
        *"aliased to"*) continue ;;
        *" is a shell builtin"*) continue ;;
      esac
      local path
      path="${line##* }"
      [ -x "$path" ] || continue
      case "$seen" in *"|$path|"*) continue ;; esac
      FOUND_YUM_PATHS+=("$path")
      seen+="|$path|"
    done < <(type -a "$BINARY" 2>/dev/null || true)
  fi

  # MCP: npm global link and the bodega-mcp shim.
  if command -v npm >/dev/null 2>&1 && command -v node >/dev/null 2>&1; then
    HAVE_NODE=1
    if npm ls -g --depth=0 "$MCP_PKG" >/dev/null 2>&1; then
      FOUND_MCP_GLOBAL=1
    fi
  fi
  if command -v "$MCP_BIN" >/dev/null 2>&1; then
    FOUND_MCP_PATH="$(command -v "$MCP_BIN")"
  fi
}

print_detection() {
  if [ "${#FOUND_YUM_PATHS[@]}" -eq 0 ] && [ "$FOUND_MCP_GLOBAL" -eq 0 ] && [ -z "$FOUND_MCP_PATH" ]; then
    kv "existing" "(none — fresh install)"
    return
  fi
  kv "existing" "found:"
  local p
  for p in "${FOUND_YUM_PATHS[@]}"; do
    local ver=""
    if [ -x "$p" ]; then
      ver="$("$p" version 2>/dev/null | head -n1 || true)"
    fi
    sub "${BINARY} → ${p}${ver:+  ${D}(${ver})${R}}"
  done
  if [ "$FOUND_MCP_GLOBAL" -eq 1 ] || [ -n "$FOUND_MCP_PATH" ]; then
    local display="$MCP_BIN"
    [ -n "$FOUND_MCP_PATH" ] && display="$MCP_BIN → $FOUND_MCP_PATH"
    [ "$FOUND_MCP_GLOBAL" -eq 1 ] && display+=" ${D}(npm global: ${MCP_PKG})${R}"
    sub "$display"
  fi
}

# ── build ─────────────────────────────────────────────────────────────────────
do_build() {
  title "build"
  VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
  COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
  DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

  kv "version" "$VERSION"
  kv "commit"  "$COMMIT"
  kv "go"      "$(go version | awk '{print $3}')"
  echo

  LDFLAGS="-s -w \
    -X ${MODULE}/internal/version.Version=${VERSION} \
    -X ${MODULE}/internal/version.Commit=${COMMIT} \
    -X ${MODULE}/internal/version.Date=${DATE}"

  step "compiling ./${BINARY}"
  CGO_ENABLED=0 go build -trimpath -ldflags="${LDFLAGS}" -o "${BINARY}" ./cmd/yum
  ok "built $(pwd)/${BINARY}"
}

# ── install (CLI) ─────────────────────────────────────────────────────────────
do_install_cli() {
  [ -x "./${BINARY}" ] || { err "./${BINARY} not found — run build first"; exit 1; }

  # Warn about prior installs in non-canonical locations. They shadow the new
  # one if they sit earlier on PATH, so call them out rather than silently
  # overwriting only the canonical copy.
  local shadow=()
  local p
  for p in "${FOUND_YUM_PATHS[@]}"; do
    [ "$p" = "${BIN_DIR}/${BINARY}" ] && continue
    shadow+=("$p")
  done
  if [ "${#shadow[@]}" -gt 0 ]; then
    warn "other ${BINARY} copies on disk — they may shadow ${BIN_DIR}/${BINARY} on PATH:"
    for p in "${shadow[@]}"; do sub "$p"; done
    echo
  fi

  step "placing binary"
  install -d "$BIN_DIR"
  install -m 0755 "./${BINARY}" "${BIN_DIR}/${BINARY}"
  ok "${BIN_DIR}/${BINARY}"

  step "writing zsh completion"
  install -d "$COMP_DIR"
  "./${BINARY}" completions zsh > "${COMP_DIR}/_${BINARY}"
  ok "${COMP_DIR}/_${BINARY}"

  step "patching ~/.zshrc (legacy yum shim)"
  if ./scripts/patch-zshrc.sh >/dev/null 2>&1; then ok "zshrc clean"; else warn "zshrc unchanged"; fi
}

# ── install (MCP) ─────────────────────────────────────────────────────────────
do_install_mcp() {
  if [ "$HAVE_NODE" -ne 1 ]; then
    warn "node/npm not found — skipping MCP server install"
    sub "install Node 18+ and re-run with --mcp-only to add it later"
    return 0
  fi
  if [ ! -d "$MCP_DIR" ]; then
    warn "no mcp-server/ directory — skipping MCP server install"
    return 0
  fi

  step "installing dependencies"
  (cd "$MCP_DIR" && npm install --silent --no-audit --no-fund)
  ok "npm install"

  step "compiling TypeScript"
  (cd "$MCP_DIR" && npm run --silent build)
  ok "dist/ built"

  step "linking ${MCP_PKG} globally"
  # Unlink first for a clean relink — npm link is idempotent but noisy.
  (cd "$MCP_DIR" && npm unlink -g --silent "$MCP_PKG" >/dev/null 2>&1 || true)
  (cd "$MCP_DIR" && npm link --silent)
  ok "${MCP_BIN} → $(command -v "$MCP_BIN" 2>/dev/null || echo '(not on PATH)')"
}

# ── install (combined) ────────────────────────────────────────────────────────
do_install() {
  title "install"
  detect_existing
  print_detection

  # Overwrite prompt — only fires if something already exists and stdin is a TTY.
  local has_existing=0
  if [ "${#FOUND_YUM_PATHS[@]}" -gt 0 ] || [ "$FOUND_MCP_GLOBAL" -eq 1 ] || [ -n "$FOUND_MCP_PATH" ]; then
    has_existing=1
  fi
  if [ "$has_existing" -eq 1 ] && [ -z "${FORCE:-}" ] && [ -t 0 ]; then
    echo
    printf "  %soverwrite existing install?%s [Y/n] " "$B" "$R"
    local answer=""
    read -r answer || answer=""
    case "${answer:-y}" in
      y|Y|yes|YES|"") ;;
      *) printf "  %saborted.%s\n" "$D" "$R"; return 0 ;;
    esac
  fi

  echo
  step "installing CLI"
  do_install_cli

  if [ "${INSTALL_MCP:-1}" -eq 1 ]; then
    echo
    step "installing MCP server"
    do_install_mcp
  else
    echo
    warn "--no-mcp set — skipping MCP server install"
  fi

  echo
  VERSION_LINE="$("./${BINARY}" version 2>/dev/null | head -n1 || echo "(version unknown)")"
  kv "installed" "$VERSION_LINE"
  echo
  printf "  %sopen a new shell and try:%s ${A}yum --help${R}\n" "$D" "$R"
  if [ "${INSTALL_MCP:-1}" -eq 1 ] && command -v "$MCP_BIN" >/dev/null 2>&1; then
    printf "  %sregister the MCP server with Claude Code:%s ${A}claude mcp add bodega -- ${MCP_BIN}${R}\n" "$D" "$R"
  fi
}

# ── clean ─────────────────────────────────────────────────────────────────────
do_clean() {
  title "clean"
  cleaned=0

  if [ -e "./${BINARY}" ]; then
    rm -f "./${BINARY}"; ok "removed ./${BINARY}"; cleaned=1
  fi

  shopt -s nullglob
  artifacts=(./${BINARY}-* ./coverage.out ./*.prof ./*.test)
  shopt -u nullglob
  for f in "${artifacts[@]}"; do
    [ -e "$f" ] || continue
    rm -f "$f"; ok "removed $f"; cleaned=1
  done

  step "go clean -testcache"
  go clean -testcache 2>/dev/null && ok "test cache cleared"

  INFO_CACHE="${XDG_CACHE_HOME:-${HOME}/.cache}/yum/info"
  if [ -d "$INFO_CACHE" ]; then
    count=$(find "$INFO_CACHE" -type f | wc -l | tr -d ' ')
    rm -rf "$INFO_CACHE"; ok "removed info cache (${count} entries)"; cleaned=1
  fi

  if [ -d "${MCP_DIR}/dist" ]; then
    rm -rf "${MCP_DIR}/dist"; ok "removed mcp-server/dist"; cleaned=1
  fi

  [ "$cleaned" -eq 0 ] && warn "nothing to clean — repo was already tidy"
}

# ── uninstall ─────────────────────────────────────────────────────────────────
do_uninstall() {
  title "uninstall"
  detect_existing
  removed=0

  # CLI: remove every copy we can find, not just the canonical one.
  local p
  for p in "${FOUND_YUM_PATHS[@]}"; do
    rm -f "$p" && { ok "removed $p"; removed=1; } || warn "could not remove $p"
  done
  if [ -e "${COMP_DIR}/_${BINARY}" ]; then
    rm -f "${COMP_DIR}/_${BINARY}"; ok "removed ${COMP_DIR}/_${BINARY}"; removed=1
  fi

  # MCP: unlink the global symlink if present.
  if [ "$FOUND_MCP_GLOBAL" -eq 1 ]; then
    (cd "$MCP_DIR" 2>/dev/null && npm unlink -g --silent "$MCP_PKG" >/dev/null 2>&1) \
      && { ok "unlinked ${MCP_PKG}"; removed=1; } \
      || warn "npm unlink -g ${MCP_PKG} failed"
  elif [ -n "$FOUND_MCP_PATH" ]; then
    warn "${MCP_BIN} on PATH but not an npm global link — leaving alone: $FOUND_MCP_PATH"
  fi

  [ "$removed" -eq 0 ] && warn "nothing to remove"
  echo
  printf "  %s~/.config/yum/ and ~/.local/share/yum/ were left alone.%s\n" "$D" "$R"
  printf "  %sdelete them manually if you want a clean slate.%s\n" "$D" "$R"
}

# ── prompt ────────────────────────────────────────────────────────────────────
prompt_install() {
  echo
  if [ ! -t 0 ]; then
    printf "  %s(non-interactive; skipping install — pass --install to force)%s\n" "$D" "$R"
    return
  fi
  printf "  %sinstall to %s${BIN_DIR}/${BINARY}%s %s(+ MCP server)%s? [Y/n] %s" "$B" "$A" "$R" "$D" "$R" "$R"
  read -r answer || answer=""
  case "${answer:-y}" in
    y|Y|yes|YES|"") do_install ;;
    *) printf "  %sskipped. binary is at %s./${BINARY}%s.%s\n" "$D" "$A" "$D" "$R" ;;
  esac
}

# ── dispatch ──────────────────────────────────────────────────────────────────
INSTALL_MCP=1
MODE=""
while [ $# -gt 0 ]; do
  case "$1" in
    -h|--help)                     MODE="help" ;;
    -i|--install)                  MODE="install" ;;
    -n|--no-install|--build-only)  MODE="build" ;;
    -c|--clean)                    MODE="clean" ;;
    -u|--uninstall)                MODE="uninstall" ;;
    --detect)                      MODE="detect" ;;
    --no-mcp)                      INSTALL_MCP=0 ;;
    --mcp-only)                    MODE="mcp-only" ;;
    "")                            ;;
    *)                             err "unknown argument: $1"; echo; show_help; exit 2 ;;
  esac
  shift || true
done

case "${MODE}" in
  help)       show_help ;;
  install)    do_build; do_install ;;
  build)      do_build ;;
  clean)      do_clean ;;
  uninstall)  do_uninstall ;;
  detect)     title "detect"; detect_existing; print_detection ;;
  mcp-only)   title "install (mcp only)"; detect_existing; print_detection; echo; do_install_mcp ;;
  "")         do_build; prompt_install ;;
esac
