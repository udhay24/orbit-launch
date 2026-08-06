# Upstream Sync State

## Last Synced Commit
- **Hash:** 03a6e91e (`bump hls.js to v1.6.17 (#6046)`)
- **Date:** 2026-08-06
- **Upstream:** bluenviron/mediamtx (https://github.com/bluenviron/mediamtx)
- **Merged into:** `sync/upstream-2026-08-06` (merge commit `1f837a82`, base `718d3b9d`)
- **Report:** `.claude/sync-reports/2026-08-06-upstream-sync.md`

`main` is a clean fast-forward mirror of upstream and sits at `03a6e91e`.

## Branch state at time of sync
- `production` — deployed, was 328 commits behind `upstream/main`. **Not yet promoted.**
- `staging` — carried the earlier 195-commit upstream batch + fork work.
- `test/pre-merge-suite` — `staging` + the pre-merge gate scripts (`718d3b9d`). Sync base.
- `sync/upstream-2026-08-06` — this sync. Contains all 195 + 133 upstream commits + full fork delta.

## Our Patches (not in upstream)
Preserve these during any merge — verified by `scripts/fork-delta-check.sh` (Layer 1 of the gate):

1. **gortmplib-patched/** — local fork via `go.mod replace`, now based on **gortmplib v1.0.0**
   (rebased from v0.4.0 on 2026-08-06). 5 patched files + fork-local `CLAUDE.md`:
   - `pkg/handshake/c0s0.go` — accept non-standard RTMP versions
   - `pkg/handshake/handshake.go` — bundled S0+S1+S2, skip C2 validation
   - `pkg/rawmessage/reader.go` — lenient chunk types 1/2/3, extended chunk stream IDs
   - `reader.go` — nil checks for codec configs
   - `server_conn.go` — synthesized tcUrl, releaseStream/FCPublish/FCUnpublish/_checkbw/deleteStream
     responses, StreamBegin before onStatus
2. **Stream registry** — `internal/streamregistry/registry.go`, `internal/api/api_stream_registry.go`,
   config in `internal/conf/conf.go`, wiring in `core.go` / `path_manager.go` / `api.go`
3. **Publisher limits** — `maxPublishers` / `publisherHysteresis`, counter in `path_manager.go`
4. **Recorder enhancements** — `internal/recorder/recorder.go`
5. **vendor/** — fork vendors dependencies (upstream does not). Run `go mod vendor` after every bump.

## v0.4.0 -> v1.0.0 gortmplib rebase notes (2026-08-06)
4 of the 5 patched files were **byte-identical** between v0.4.0 and v1.0.0; only `server_conn.go`
moved (38 lines). Upstream removed `buildURL(tcURL, app, streamKey)` and replaced it with
`joinURL(tcURL, streamKey)`, which derives the URL entirely from `tcURL` rather than composing it
from `app`. Our tcUrl-synthesis patch still holds because the synthesized value
(`"rtmp://localhost/" + c.app`) is a full URL, so `joinURL` yields the same `/app/streamKey` path.
`git merge-file -p --diff3 <patched> <v0.4.0> <v1.0.0>` merged cleanly with zero conflicts.

## Conflict-Prone Files
Any upstream change to these needs careful review:
- `internal/core/path_manager.go` — conflicted this sync (gofmt realign + `chAPIForwardDest*`)
- `internal/core/core.go` — conflicted this sync (gofmt realign + `udpMaxPayloadSize`)
- `go.mod` / `go.sum` — conflicted this sync (replace directives)
- `mediamtx.yml` — conflicted this sync (4 regions)
- `internal/api/api.go`, `internal/conf/conf.go`, `internal/servers/rtmp/*` — auto-merged this sync

## Verification at time of sync
- `go build ./...`, `go vet ./...`, `gofmt -l internal/` — all clean
- Fork-delta check — all 23 sentinels intact
- `internal/streamregistry` + `TestPathManagerPublisherLimit` under `-race` — pass
- **Chaos suite: 49/49, resilience 100/100** against the freshly built binary
- `scripts/pre-merge-check.sh` (Layers 1-4) — **PASSED**, exit 0

## Known pre-existing failures (NOT caused by this sync)
Verified by running the same tests on pristine upstream `main` with no fork code:
- `internal/core` — `TestPathMaxReaders`, `TestPathResolveSource`: RTSP DESCRIBE returns 404.
  **Fails identically on pure upstream `main`.** macOS environment issue.
- `internal/core` — `TestAPIProtocolListGet/webrtc`: flaky under parallel load; passes 3/3 in isolation
  on both merged and baseline.
- `internal/staticsources/{rtp,mpegts}` — `TestSourceUDP/multicast_with_interface` hangs 10 min.
  Fork does not touch `internal/staticsources/`. macOS multicast-interface binding.

Consequence: `--full` (Layer 2b) cannot pass on macOS. Use Linux CI for the full upstream suite.

## Pending / deferred
- **Not pushed, no PR opened.** `sync/upstream-2026-08-06` is local only.
- **Promote to `production`** after combined testing.
- `moq` is set to **false** in `mediamtx.yml`; upstream's sample defaults it to `true`.
  Enabling opens `:8892` (HTTP2/HTTP3) and `:8893` (QUIC).
- `mediamtx.yml` still does not carry upstream's `authJWTIssuer` / `authJWTAudience` / `hlsCDNSecret`
  keys; `TestSampleConfFile` remains skipped in the gate.
- Evaluate upstream's native stream forwarding (#5558) against the Redis stream registry.
- Verify `rtspTrustedProxies` and the new RTSP UDP port range 32768-60999 against firewall rules.
- `streamRegistryPassword` is committed in plaintext in `mediamtx.yml`.
