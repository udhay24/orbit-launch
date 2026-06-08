#!/usr/bin/env bash
#
# Generate the //go:embed assets the build needs:
#   - internal/core/VERSION         (via versiongetter — local git, never fails)
#   - internal/servers/hls/hls.min.js (via hlsjsdownloader — fetches from GitHub)
#
# The hls.js download hits GitHub release CDN and transiently returns 504, so we
# retry. The arm-only rpicamera blob is intentionally NOT generated (not needed
# on amd64/darwin builds, and it's a large flaky download).
#
# Shared by scripts/run-chaos.sh, scripts/pre-merge-check.sh, and the go-tests CI job.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

for attempt in 1 2 3 4 5; do
  if go generate ./internal/core/ ./internal/servers/hls/; then
    exit 0
  fi
  if [ "$attempt" -lt 5 ]; then
    echo "go generate failed (attempt $attempt/5) — retrying in $((attempt * 3))s..."
    sleep "$((attempt * 3))"
  fi
done

echo "go generate failed after 5 attempts (asset download unavailable)"
exit 1
