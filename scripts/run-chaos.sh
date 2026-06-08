#!/usr/bin/env bash
#
# Layer 4 of the pre-merge suite: boot the freshly built binary and run the RTMP
# chaos suite (.ai/chaos_test.py) against it. Self-contained — builds, boots, and
# tears down a local mediamtx, so it needs no external server.
#
# Used by both scripts/pre-merge-check.sh and .github/workflows/pre-merge.yml.
# Spec: docs/superpowers/specs/2026-06-07-pre-merge-test-suite-design.md
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

HOST="127.0.0.1"
# Use an uncommon high port by default so we never collide with — and silently
# test — a real mediamtx already bound to the standard 1935.
PORT="${CHAOS_TARGET_PORT:-11935}"

command -v python3 >/dev/null 2>&1 || { echo "python3 not found — required for the chaos suite"; exit 1; }

port_open() {
  python3 -c "import socket,sys; s=socket.socket(); s.settimeout(1); sys.exit(0 if s.connect_ex(('$HOST',$PORT))==0 else 1)" 2>/dev/null
}

# Pre-flight: the port must be free, otherwise we'd test whatever is already there.
if port_open; then
  echo "port $HOST:$PORT is already in use — refusing to run (would test a foreign server)."
  echo "stop that process or set CHAOS_TARGET_PORT to a free port."
  exit 1
fi

workdir="$(mktemp -d)"
bin="$workdir/mediamtx"
cfg="$workdir/mediamtx.yml"
logf="$workdir/server.log"
server_pid=""

cleanup() {
  if [ -n "$server_pid" ]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$workdir"
}
trap cleanup EXIT

echo "generating embedded assets (VERSION, hls.min.js)..."
# Only the embeds the amd64/arm-agnostic build needs; skip the flaky, large
# rpicamera blob download (its embed is arm-only).
go generate ./internal/core/ ./internal/servers/hls/
echo "building binary..."
go build -o "$bin" .

cat > "$cfg" <<EOF
logLevel: error
api: no
metrics: no
pprof: no
playback: no
rtsp: no
hls: no
webrtc: no
srt: no
moq: no
rtmp: yes
rtmpAddress: :$PORT
# Keep the run hermetic: no recording, and any stray output stays in the temp dir.
pathDefaults:
  record: no
  recordPath: $workdir/recordings/%path/%Y-%m-%d-%H-%M-%S
paths:
  all_others:
EOF

echo "booting mediamtx on :$PORT ..."
"$bin" "$cfg" >"$logf" 2>&1 &
server_pid=$!

# Wait for the RTMP port to accept connections.
ready=0
for _ in $(seq 1 60); do
  if port_open; then ready=1; break; fi
  if ! kill -0 "$server_pid" 2>/dev/null; then
    echo "server exited before opening :$PORT"; cat "$logf"; exit 1
  fi
  sleep 0.5
done
if [ "$ready" -ne 1 ]; then
  echo "server did not open :$PORT in time"; cat "$logf"; exit 1
fi

echo "running chaos suite against $HOST:$PORT ..."
set +e
CHAOS_TARGET_HOST="$HOST" CHAOS_TARGET_PORT="$PORT" python3 .ai/chaos_test.py
rc=$?
set -e
exit "$rc"
