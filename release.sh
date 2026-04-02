#!/bin/bash
# Publish a new orbit-launch (mediamtx) release to GitHub.
#
# Usage:
#   ./release.sh                  # auto-version from internal/core/VERSION
#   ./release.sh v2.2             # explicit tag
#   ./release.sh v2.2 --prerelease
#
# Prerequisites:
#   - gh CLI authenticated (gh auth status)
#   - go 1.21+ in PATH
#   - GOOS=linux GOARCH=amd64 build environment

set -euo pipefail

REPO="udhay24/orbit-launch"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

# ── Version ──────────────────────────────────────────────────────────────────
if [ -n "${1:-}" ] && [[ "$1" != --* ]]; then
    TAG="$1"
    shift
else
    # Strip git metadata — keep only vX.Y or vX.Y.Z
    RAW=$(cat "$SCRIPT_DIR/internal/core/VERSION")
    TAG=$(echo "$RAW" | grep -oE '^v[0-9]+\.[0-9]+(\.[0-9]+)?')
    if [ -z "$TAG" ]; then
        echo "ERROR: Could not parse version from internal/core/VERSION ('$RAW')"
        echo "  Pass an explicit tag: ./release.sh v2.2"
        exit 1
    fi
fi

PRERELEASE_FLAG=""
if [[ "${1:-}" == "--prerelease" ]]; then
    PRERELEASE_FLAG="--prerelease"
fi

echo "==> Releasing $TAG from $REPO"

# ── Check gh CLI ─────────────────────────────────────────────────────────────
if ! command -v gh &>/dev/null; then
    echo "ERROR: gh CLI not found. Install from https://cli.github.com/"
    exit 1
fi
gh auth status --hostname github.com >/dev/null

# ── Build ─────────────────────────────────────────────────────────────────────
BUILD_DIR="$(mktemp -d)"
trap 'rm -rf "$BUILD_DIR"' EXIT

echo "==> Building mediamtx (linux/amd64)..."
cd "$SCRIPT_DIR"
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o "$BUILD_DIR/mediamtx" .
cp mediamtx.yml "$BUILD_DIR/mediamtx.yml"

echo "==> Build complete: $(du -sh "$BUILD_DIR/mediamtx" | cut -f1) binary"

# ── Checksum ─────────────────────────────────────────────────────────────────
cd "$BUILD_DIR"
sha256sum mediamtx mediamtx.yml > checksums.txt
echo "==> SHA256:"
cat checksums.txt

# ── Publish ──────────────────────────────────────────────────────────────────
cd "$SCRIPT_DIR"

# Delete tag if it already exists locally/remotely (allows re-releasing same tag)
git tag -d "$TAG" 2>/dev/null || true
git push origin ":refs/tags/$TAG" 2>/dev/null || true

git tag "$TAG"
git push origin "$TAG"

echo "==> Creating GitHub release $TAG..."
gh release create "$TAG" \
    "$BUILD_DIR/mediamtx" \
    "$BUILD_DIR/mediamtx.yml" \
    "$BUILD_DIR/checksums.txt" \
    --repo "$REPO" \
    --title "orbit-launch $TAG" \
    --notes "Automated release of mediamtx binary and config.

## Assets
- \`mediamtx\` — Linux x86-64 binary
- \`mediamtx.yml\` — Default configuration
- \`checksums.txt\` — SHA-256 checksums" \
    $PRERELEASE_FLAG

echo ""
echo "==> Release published: https://github.com/$REPO/releases/tag/$TAG"
echo ""
echo "Servers will pick up this release on next boot (or run fetch-mediamtx.service manually)."
