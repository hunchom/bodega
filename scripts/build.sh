#!/usr/bin/env bash
# Direct go-build wrapper. Does not require Xcode Command Line Tools.
# Equivalent to `make build` for hosts without CLT installed.
set -euo pipefail

cd "$(dirname "$0")/.."

VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

LDFLAGS="-s -w \
  -X github.com/hunchom/bodega/internal/version.Version=${VERSION} \
  -X github.com/hunchom/bodega/internal/version.Commit=${COMMIT} \
  -X github.com/hunchom/bodega/internal/version.Date=${DATE}"

CGO_ENABLED=0 go build -trimpath -ldflags="${LDFLAGS}" -o yum ./cmd/yum
echo "built → $(pwd)/yum"
