#!/usr/bin/env bash
#
# Layer 1 of the pre-merge suite: verify the orbit-launch fork delta survived an
# upstream merge. Static grep checks only — no build — so it runs in seconds.
#
# Each sentinel is a distinctive string from one of the 6 custom changes. If an
# upstream merge restores a stock file, the sentinel disappears and this fails,
# naming exactly which patch was clobbered.
#
# Spec: docs/superpowers/specs/2026-06-07-pre-merge-test-suite-design.md
# Delta inventory: memory project-fork-delta.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

fail=0
pass() { printf '  \033[32m✓\033[0m %s\n' "$1"; }
bad()  { printf '  \033[31m✗\033[0m %s\n' "$1"; fail=1; }

# contains FILE SUBSTRING LABEL — fail if SUBSTRING is absent from FILE.
contains() {
  if [ -f "$1" ] && grep -qF -- "$2" "$1"; then pass "$3"; else bad "$3 — expected \"$2\" in $1"; fi
}

# absent FILE SUBSTRING LABEL — fail if SUBSTRING is present in FILE.
absent() {
  if [ -f "$1" ] && grep -qF -- "$2" "$1"; then bad "$3 — unexpected \"$2\" in $1"; else pass "$3"; fi
}

# exists FILE LABEL — fail if FILE is missing.
exists() {
  if [ -f "$1" ]; then pass "$2"; else bad "$2 — missing $1"; fi
}

echo "== gortmplib replace directive =="
contains go.mod "replace github.com/bluenviron/gortmplib => ./gortmplib-patched" \
  "go.mod points gortmplib at ./gortmplib-patched"

echo "== gortmplib camera patches (5 files) =="
G=gortmplib-patched
contains "$G/pkg/handshake/c0s0.go"        "Accept any version"                                "c0s0: accept any RTMP version"
contains "$G/pkg/handshake/handshake.go"   "Bundle S0+S1+S2 into a single write"               "handshake: bundled S0+S1+S2 write"
contains "$G/pkg/rawmessage/reader.go"     "type 1 chunks without a prior type 0"              "rawmessage: lenient chunk handling"
contains "$G/reader.go"                    "missing AVC config"                                "reader: H264 nil-guard"
contains "$G/reader.go"                    "missing HEVC config"                               "reader: H265 nil-guard"
absent   "$G/reader.go"                    "should not happen"                                 "reader: panic(\"should not happen\") removed"
contains "$G/server_conn.go"               "construct from app"                                "server_conn: synthesize missing tcURL"

echo "== publisher limits =="
contains internal/conf/conf.go        "MaxPublishers"       "conf: MaxPublishers field"
contains internal/conf/conf.go        "PublisherHysteresis" "conf: PublisherHysteresis field"
contains internal/core/path_manager.go "publisherLimitHit"  "path_manager: publisher-limit logic"
contains mediamtx.yml                 "maxPublishers"       "mediamtx.yml: maxPublishers key"
contains mediamtx.yml                 "publisherHysteresis" "mediamtx.yml: publisherHysteresis key"

echo "== stream registry =="
exists   internal/streamregistry/registry.go "streamregistry package present"
contains internal/conf/conf.go "StreamRegistry"        "conf: StreamRegistry fields"
contains mediamtx.yml          "streamRegistry"        "mediamtx.yml: streamRegistry key"

echo "== chaos suite present =="
exists .ai/chaos_test.py "chaos suite present"

if [ "$fail" -ne 0 ]; then
  printf '\n\033[31mFORK DELTA CHECK FAILED\033[0m — an upstream merge likely clobbered a custom patch above.\n'
  exit 1
fi
printf '\n\033[32mFork delta intact.\033[0m\n'
