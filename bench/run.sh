#!/usr/bin/env bash
# Benchmark suite for yum CLI. Uses hyperfine to measure wall-clock latency
# for a representative set of commands. Dumps a markdown table to the given
# output file (default: bench/results.md).
#
# Usage:
#   bench/run.sh [output_file]
#
# Environment:
#   YUM_BIN   Path to the yum binary under test.
#             Default: $HOME/.local/bin/yum
#   WARMUP    hyperfine warmup runs (default: 3)
#   RUNS      hyperfine sample count (default: 10)

set -euo pipefail

YUM_BIN="${YUM_BIN:-$HOME/.local/bin/yum}"
WARMUP="${WARMUP:-3}"
RUNS="${RUNS:-10}"
OUT="${1:-bench/results.md}"

if [[ ! -x "$YUM_BIN" ]]; then
  echo "yum binary not found or not executable: $YUM_BIN" >&2
  exit 1
fi

if ! command -v hyperfine >/dev/null 2>&1; then
  echo "hyperfine not installed; brew install hyperfine" >&2
  exit 1
fi

# Commands to benchmark. Each line is one command (argv after the binary).
CMDS=(
  "--help"
  "version"
  "list"
  "list leaves"
  "list pinned"
  "list casks"
  "info ripgrep"
  "repolist"
  "outdated"
  "size"
  "history"
  "doctor"
  "search ripgrep"
)

TMP_JSON="$(mktemp -t yum-bench.XXXXXX).json"
trap 'rm -f "$TMP_JSON"' EXIT

# Hyperfine lets us pass multiple --command-name/command pairs and aggregate
# into one JSON. We do it individually to keep invocation simple and robust
# to any single failing command.
echo "| command | mean (ms) | stddev (ms) | min (ms) | max (ms) |" > "$OUT"
echo "|---|---|---|---|---|" >> "$OUT"

for cmd in "${CMDS[@]}"; do
  # shellcheck disable=SC2086
  if hyperfine --warmup "$WARMUP" --runs "$RUNS" --export-json "$TMP_JSON" \
       --show-output --command-name "yum $cmd" "$YUM_BIN $cmd" \
       >/dev/null 2>&1; then
    mean_s=$(python3 -c "import json; d=json.load(open('$TMP_JSON')); print(d['results'][0]['mean'])")
    sd_s=$(python3   -c "import json; d=json.load(open('$TMP_JSON')); print(d['results'][0]['stddev'])")
    min_s=$(python3  -c "import json; d=json.load(open('$TMP_JSON')); print(d['results'][0]['min'])")
    max_s=$(python3  -c "import json; d=json.load(open('$TMP_JSON')); print(d['results'][0]['max'])")
    mean=$(awk "BEGIN{printf \"%.1f\", $mean_s*1000}")
    sd=$(awk   "BEGIN{printf \"%.1f\", $sd_s*1000}")
    mn=$(awk   "BEGIN{printf \"%.1f\", $min_s*1000}")
    mx=$(awk   "BEGIN{printf \"%.1f\", $max_s*1000}")
    printf "| yum %s | %s | %s | %s | %s |\n" "$cmd" "$mean" "$sd" "$mn" "$mx" >> "$OUT"
    printf "  %-24s  mean=%sms  sd=%sms\n" "yum $cmd" "$mean" "$sd"
  else
    printf "| yum %s | ERR | - | - | - |\n" "$cmd" >> "$OUT"
    printf "  %-24s  ERROR\n" "yum $cmd"
  fi
done

echo
echo "results → $OUT"
