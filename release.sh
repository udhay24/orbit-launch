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
    # Strip git metadata — keep only vX.Y or vX.Y.Z, then bump minor
    RAW=$(cat "$SCRIPT_DIR/internal/core/VERSION")
    CURRENT=$(echo "$RAW" | grep -oE '^v[0-9]+\.[0-9]+(\.[0-9]+)?')
    if [ -z "$CURRENT" ]; then
        echo "ERROR: Could not parse version from internal/core/VERSION ('$RAW')"
        echo "  Pass an explicit tag: ./release.sh v2.2"
        exit 1
    fi
    MAJOR=$(echo "$CURRENT" | grep -oE '[0-9]+' | sed -n '1p')
    MINOR=$(echo "$CURRENT" | grep -oE '[0-9]+' | sed -n '2p')
    TAG="v${MAJOR}.$((MINOR + 1))"
    echo "$TAG" > "$SCRIPT_DIR/internal/core/VERSION"
    echo "==> Auto-bumped version $CURRENT → $TAG"
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

# ── Build (native cross-compile, CGO disabled) ───────────────────────────────
# Go cross-compiles to linux/amd64 natively with CGO disabled — no Docker
# emulation. Emulating amd64 on Apple Silicon (Rosetta/qemu) segfaults the Go
# toolchain on large builds, so we cross-compile on the host instead.
BUILD_DIR="$(mktemp -d)"
trap 'rm -rf "$BUILD_DIR"' EXIT

if ! command -v go &>/dev/null; then
    echo "ERROR: go not found in PATH."
    exit 1
fi

cd "$SCRIPT_DIR"

echo "==> Building mediamtx (linux/amd64, native cross-compile)..."
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$BUILD_DIR/mediamtx" .

cp mediamtx.yml "$BUILD_DIR/mediamtx.yml"

# Verify ELF binary is Linux x86-64
file "$BUILD_DIR/mediamtx" | grep -q "ELF 64-bit.*x86-64" \
    || { echo "ERROR: mediamtx is not a Linux x86-64 binary (got: $(file "$BUILD_DIR/mediamtx")) — aborting"; exit 1; }

echo "==> Build complete: $(du -sh "$BUILD_DIR/mediamtx" | cut -f1) binary (ELF 64-bit x86-64 verified)"

# Verify yml is compatible with the binary — catches version mismatch (e.g. unknown fields).
# Build a host-arch linux binary and parse the config in a clean native container
# (no emulation, no host port conflicts). Same source => same config schema as the
# released amd64 binary. mediamtx prints "ERR:" and exits if the config is invalid.
echo "==> Validating mediamtx.yml against binary..."
HOST_ARCH=$(go env GOARCH)
CGO_ENABLED=0 GOOS=linux GOARCH="$HOST_ARCH" go build -o "$BUILD_DIR/mediamtx-validate" .
VALIDATE_OUTPUT=$(docker run --rm \
    -v "$BUILD_DIR":/out \
    -w /out \
    alpine \
    sh -c 'timeout 2 ./mediamtx-validate mediamtx.yml 2>&1 || true')

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
