#!/usr/bin/env bash
#
# Generate the //go:embed assets the build needs:
#   - internal/core/VERSION           (via versiongetter — local git, reliable)
#   - internal/servers/hls/hls.min.js (committed to the repo; only downloaded if absent)
#
# hls.min.js is committed because its hlsjsdownloader fetch hits the GitHub
# release CDN, which intermittently returns 504 and makes every CI build flaky.
# We only re-download it if it's missing (e.g. someone deleted it), with retry.
# The arm-only rpicamera blob is intentionally NOT generated (not needed on
# amd64/darwin builds; large flaky download).
#
# Shared by scripts/run-chaos.sh, scripts/pre-merge-check.sh, and the go-tests CI job.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# VERSION: derived from local git, never hits the network.
go generate ./internal/core/

# hls.min.js: use the committed copy if present.
if [ -f internal/servers/hls/hls.min.js ]; then
  exit 0
fi

echo "hls.min.js missing — downloading (with retry)..."
for attempt in 1 2 3 4 5; do
  if go generate ./internal/servers/hls/; then
    exit 0
  fi
  if [ "$attempt" -lt 5 ]; then
    echo "hls.js download failed (attempt $attempt/5) — retrying in $((attempt * 3))s..."
    sleep "$((attempt * 3))"
  fi
done

echo "hls.js download failed after 5 attempts (asset download unavailable)"
exit 1
