#!/usr/bin/env bash
# bodega build / install / uninstall — one script, three modes.
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

# ── help ──────────────────────────────────────────────────────────────────────
show_help() {
  cat <<EOF
${B}${A}bodega${R} — build, install, clean, uninstall

${B}usage${R}
  ${A}./build.sh${R}                build ${BINARY}; prompt to install
  ${A}./build.sh ${M}-i, --install${R}   build and install, no prompt
  ${A}./build.sh ${M}-n, --no-install${R} build only, skip the prompt
  ${A}./build.sh ${M}-c, --clean${R}     remove build artifacts, test cache, info cache
  ${A}./build.sh ${M}-u, --uninstall${R} remove installed ${BINARY} + completions
  ${A}./build.sh ${M}-h, --help${R}      show this message

${B}paths${R}
  ${M}binary     ${R} ${BIN_DIR}/${BINARY}
  ${M}completion ${R} ${COMP_DIR}/_${BINARY}
  ${M}info cache ${R} \${XDG_CACHE_HOME:-\$HOME/.cache}/yum/info

${B}env${R}
  ${M}NO_COLOR=1 ${R} disable ANSI output
EOF
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

# ── install ───────────────────────────────────────────────────────────────────
do_install() {
  title "install"
  [ -x "./${BINARY}" ] || { err "./${BINARY} not found — run build first"; exit 1; }

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

  echo
  VERSION_LINE="$("./${BINARY}" version 2>/dev/null | head -n1 || echo "(version unknown)")"
  kv "installed" "$VERSION_LINE"
  echo
  printf "  %sopen a new shell and try:%s ${A}yum --help${R}\n" "$D" "$R"
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

  [ "$cleaned" -eq 0 ] && warn "nothing to clean — repo was already tidy"
}

# ── uninstall ─────────────────────────────────────────────────────────────────
do_uninstall() {
  title "uninstall"
  removed=0
  if [ -e "${BIN_DIR}/${BINARY}" ]; then
    rm -f "${BIN_DIR}/${BINARY}"; ok "removed ${BIN_DIR}/${BINARY}"; removed=1
  fi
  if [ -e "${COMP_DIR}/_${BINARY}" ]; then
    rm -f "${COMP_DIR}/_${BINARY}"; ok "removed ${COMP_DIR}/_${BINARY}"; removed=1
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
  printf "  %sinstall to %s${BIN_DIR}/${BINARY}%s%s? [Y/n] %s" "$B" "$A" "$R" "$B" "$R"
  read -r answer || answer=""
  case "${answer:-y}" in
    y|Y|yes|YES|"") do_install ;;
    *) printf "  %sskipped. binary is at %s./${BINARY}%s.%s\n" "$D" "$A" "$D" "$R" ;;
  esac
}

# ── dispatch ──────────────────────────────────────────────────────────────────
case "${1:-}" in
  -h|--help)                    show_help ;;
  -i|--install)                 do_build; do_install ;;
  -n|--no-install|--build-only) do_build ;;
  -c|--clean)                   do_clean ;;
  -u|--uninstall)               do_uninstall ;;
  "")                           do_build; prompt_install ;;
  *)  err "unknown argument: $1"; echo; show_help; exit 2 ;;
esac
