# Upstream Sync State

## Last Synced Commit
- **Hash:** 0a690204
- **Date:** 2026-04-08
- **Upstream:** bluenviron/mediamtx (https://github.com/bluenviron/mediamtx)
- **Our main branch tip at sync time:** 0a690204 (after merge)

## Our Patches (not in upstream)
These exist on `feat/rtmp-camera-compatibility` and must be preserved during any merge:

1. **gortmplib-patched/** — local fork via `go.mod replace` directive
   - `pkg/handshake/c0s0.go` — accept non-standard RTMP versions
   - `pkg/handshake/handshake.go` — bundled S0+S1+S2, skip C2 validation
   - `pkg/rawmessage/reader.go` — lenient chunk types 1/2/3, extended chunk stream IDs
   - `server_conn.go` — optional tcUrl, respond to releaseStream/FCPublish/FCUnpublish, StreamBegin before onStatus
   - `reader.go` — nil checks for codec configs

2. **Stream registry** — Redis-backed stream-to-server lookup
   - `internal/streamregistry/registry.go`
   - `internal/api/api_stream_registry.go`
   - Config fields in `internal/conf/conf.go`
   - Wiring in `internal/core/core.go`, `internal/core/path_manager.go`, `internal/api/api.go`

3. **Recorder enhancements** — `internal/recorder/recorder.go`

## Conflict-Prone Files
Any upstream change to these files needs careful review:
- `internal/core/core.go` (createResources/closeResources lifecycle)
- `internal/core/path_manager.go` (run loop, ready/not-ready handlers)
- `internal/api/api.go` (route registration, struct fields)
- `internal/conf/conf.go` (struct fields, defaults)
- `go.mod` (gortmplib version — we use replace directive)

## Pending Rebase
After merging sync/upstream-2026-04-08 into main, `feat/rtmp-camera-compatibility` needs rebasing:
- `Authenticate()` signature changed: `*auth.Error` → `(string, *auth.Error)`
- `atomic.Int64` struct replaces `atomic.LoadInt64`/`atomic.AddInt64` in path_manager
- gortmplib bumped to 0.3.1 — check if gortmplib-patched/ needs updating
