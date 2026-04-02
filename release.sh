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

# ── Build inside Docker (native Linux — no cross-compile surprises) ───────────
BUILD_DIR="$(mktemp -d)"
trap 'rm -rf "$BUILD_DIR"' EXIT

if ! command -v docker &>/dev/null; then
    echo "ERROR: docker not found. Install Docker Desktop to build release binaries."
    exit 1
fi

cd "$SCRIPT_DIR"

# Detect Go version from go.mod for an exact match in the build image
GO_VERSION=$(grep '^go ' go.mod | awk '{print $2}' | cut -d. -f1,2)
GO_IMAGE="golang:${GO_VERSION}-alpine"
echo "==> Building mediamtx inside Docker ($GO_IMAGE)..."

docker run --rm \
    --platform linux/amd64 \
    -v "$SCRIPT_DIR":/src \
    -v "$BUILD_DIR":/out \
    -w /src \
    -e CGO_ENABLED=0 \
    -e GOOS=linux \
    -e GOARCH=amd64 \
    "$GO_IMAGE" \
    sh -c 'go build -o /out/mediamtx .'

cp mediamtx.yml "$BUILD_DIR/mediamtx.yml"

# Verify ELF binary is Linux x86-64 — catches ARM cross-compile from Apple Silicon
file "$BUILD_DIR/mediamtx" | grep -q "ELF 64-bit.*x86-64" \
    || { echo "ERROR: mediamtx is not a Linux x86-64 binary (got: $(file "$BUILD_DIR/mediamtx")) — aborting"; exit 1; }

echo "==> Build complete: $(du -sh "$BUILD_DIR/mediamtx" | cut -f1) binary (ELF 64-bit x86-64 verified)"

# Verify yml is compatible with the binary — catches version mismatch (e.g. unknown fields)
# mediamtx exits immediately with an error if the config has unrecognised fields.
# We run it with a 2s timeout; it will either print an ERR and exit 1 (bad yml)
# or start listening and be killed by the timeout (good yml → exit 143, treated as success).
echo "==> Validating mediamtx.yml against binary..."
VALIDATE_OUTPUT=$(docker run --rm \
    --platform linux/amd64 \
    -v "$BUILD_DIR":/out \
    -w /out \
    alpine \
    sh -c 'timeout 2 ./mediamtx mediamtx.yml 2>&1 || true')

if echo "$VALIDATE_OUTPUT" | grep -q "^ERR:"; then
    echo "ERROR: mediamtx rejected the yml config:"
    echo "$VALIDATE_OUTPUT" | grep "^ERR:"
    exit 1
fi
echo "==> mediamtx.yml validated OK"

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
