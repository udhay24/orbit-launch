# Upstream Sync State

## Last Synced Commit
- **Hash:** 168e1723
- **Date:** 2026-11-01
- **Upstream:** bluenviron/mediamtx (https://github.com/bluenviron/mediamtx)
- **Merged into:** `production` (local, not pushed)
- **Report:** `.claude/sync-reports/2026-11-01-upstream-sync.md`

`main` is a clean fast-forward mirror of upstream and sits at `168e1723`.

## Our Patches (not in upstream)
Preserve these during any merge — verified by `scripts/fork-delta-check.sh` (Layer 1 of the gate):

1. **gortmplib-patched/** — local fork via `go.mod replace`, now based on **gortmplib v1.0.5**
   (rebased from v1.0.0 on 2026-11-01). 5 patched files + fork-local `CLAUDE.md`:
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
5. **Publish allowlist** — `internal/publishallowlist`, hook in `path_manager.go` outside the run loop,
   Redis password redaction in `internal/conf/redact.go`
6. **vendor/** — fork vendors dependencies (upstream does not). Run `go mod vendor` after every bump.

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
- **Not pushed.** `production` is local only.
- `moq` is set to **false** in `mediamtx.yml`; upstream's sample defaults it to `true`.
  Enabling opens `:8892` (HTTP2/HTTP3) and `:8893` (QUIC).
- `TestDefaultConfFile` stays skipped in the gate: `mediamtx.yml` is a deployment config.
- `internal/api` tests cannot compile on macOS (upstream inotify calls); the gate vets with `GOOS=linux`.
- Evaluate upstream's native stream forwarding (#5558) against the Redis stream registry.
- Verify `rtspTrustedProxies` and the new RTSP UDP port range 32768-60999 against firewall rules.
- The old `streamRegistryPassword` is still in git history and past release assets: rotate it.
