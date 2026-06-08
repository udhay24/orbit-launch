#!/usr/bin/env bash
#
# Pre-merge gate for promoting the fork (staging -> production). Runs the
# fork-delta suite fastest-first and stops at the first failing layer.
#
#   scripts/pre-merge-check.sh           run all layers (1-4)
#   scripts/pre-merge-check.sh --fast    skip Layer 4 (no live server boot)
#   scripts/pre-merge-check.sh --full    also run the entire upstream test suite
#
# Spec: docs/superpowers/specs/2026-06-07-pre-merge-test-suite-design.md
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

RUN_CHAOS=1
RUN_FULL=0
for arg in "$@"; do
  case "$arg" in
    --fast|--no-chaos) RUN_CHAOS=0 ;;
    --full)            RUN_FULL=1 ;;
    -h|--help)
      grep '^#' "$0" | sed 's/^# \{0,1\}//; 1d'
      exit 0 ;;
    *) echo "unknown option: $arg"; exit 2 ;;
  esac
done

step() { printf '\n\033[1m== %s ==\033[0m\n' "$1"; }

step "Layer 1 — fork-delta integrity"
bash scripts/fork-delta-check.sh

step "Layer 2 — build & vet"
go generate ./...   # produces embedded VERSION / hls.min.js (absent on a clean checkout)
go build ./...
go vet ./...

step "Layer 3 — custom-feature tests"
go test -race ./internal/streamregistry/
go test -race -run TestPathManagerPublisherLimit ./internal/core/

if [ "$RUN_FULL" -eq 1 ]; then
  step "Layer 2b — full upstream test suite"
  go test ./internal/...
fi

if [ "$RUN_CHAOS" -eq 1 ]; then
  step "Layer 4 — live RTMP chaos"
  bash scripts/run-chaos.sh
else
  printf '\n(skipping Layer 4 chaos — --fast)\n'
fi

printf '\n\033[1;32mPRE-MERGE CHECK PASSED\033[0m\n'
