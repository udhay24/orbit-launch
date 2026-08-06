# Upstream Sync Report — 2026-04-08

## Summary
- **50 new commits** since last sync (580bfe86)
- 6 features, 2 bug fixes, 18 dep bumps, 1 security fix, ~23 docs/CI/tooling
- **Conflict risk: MEDIUM** — all 5 conflict-prone files touched

## Important Changes

### Security Fix (CRITICAL — merge ASAP)
- `7418e510` **prevent directory traversal attacks** — path names used in recorder, playback, HTTP components now reject special chars. Affects `internal/conf/path.go`, `internal/api/api_recordings.go`, `internal/playback/`.

### Features
- `2b302e79` **api: provide track details** — codec info per track in API responses. New files: `api_path_track.go`, `api_path_track_codec_props.go`. Touches `internal/defs/api_path.go`, `internal/core/path.go`.
- `c8e07b55` **api: add new stats** — `outboundFramesDiscarded`, `inboundFramesInError` for RTSP/RTMP/SRT/WebRTC/HLS paths. Touches many `internal/defs/api_*.go` and `internal/core/path.go`.
- `d1fd3df2` **api: add user field** to RTSP/RTMP/SRT/WebRTC connections and sessions. Auth `Authenticate()` now returns `(string, *auth.Error)` — **breaking interface change**.
- `a7290a20` **webrtc: support publishing multiple video/audio renditions**
- `d4c6f952` **dump unencrypted TLS sessions** — Wireshark-compatible key logging when dumpPackets=true
- `f98c9c59` **rtsp: support unwrapping MPEG-TS tracks**

### Bug Fixes
- `8568d8c5` **webrtc: fix random absolute timestamps** with Opus, G711, LPCM
- `d5d1de0b` **webrtc: strip TWCC extension** of incoming RTP packets

### Breaking Changes
- `d1fd3df2` — `Authenticate()` signature changed: `*auth.Error` → `(string, *auth.Error)`. Affects ALL auth call sites including our stream registry.
- `6d6ebee8` — `authJWTInHTTPQuery` deprecated, type changed from `bool` → `*bool`
- `4472bcfc` — token query parameter standardized to `"token"`
- `0eb7089e` — `atomic.LoadInt64`/`atomic.AddInt64` → `atomic.Int64` struct methods in path_manager

### Dependency Bumps
| Package | From | To | Notes |
|---------|------|----|-------|
| **gortmplib** | 0.3.0 | **0.3.1** | Patch release — need to check if gortmplib-patched needs rebase |
| gortsplib | 5.3.2 → 5.5.1 | | Major version jump |
| gohlslib | 2.2.6 → 2.2.9 | | |
| mediacommon | 2.8.0 → 2.8.3 | | |
| kong | 1.14.0 → 1.15.0 | | |
| pion/webrtc | 4.2.9 → 4.2.11 | | |
| pion/ice | 4.2.1 → 4.2.2 | | |
| gin | 1.11.0 → 1.12.0 | | |
| argon2 | 1.4.6 → 1.5.1 | | |
| quic-go | 0.57.0 → 0.59.0 | | |
| go-mp4 | 1.4.1 → 1.5.0 | | |
| + others | | | Various indirect bumps |

### Docs/CI Only (~23 commits, no action needed)
- Documentation reorganization, architecture page, OBS Studio encryption guide, Keycloak JWT docs, link fixes
- `6a2bccd2` docsorder linter, `0f42f087` docslinks linter
- `ff4ca04f` golangci-lint bump to 2.11.4
- `5addbed3` revert+redo dockerhub modernization

## Conflict Analysis

| File | Upstream Changes | Conflict with Our Patches? |
|------|-----------------|---------------------------|
| `go.mod` | gortmplib 0.3.0→0.3.1, 18+ dep bumps, go 1.25→? | **YES** — our `replace` directive stays, but versions need reconciling |
| `internal/conf/conf.go` | `deprecated:"true"` struct tags, `AuthJWTInHTTPQuery` type `bool→*bool`, new `AuthJWTIssuer`/`AuthJWTAudience` fields, `writeQueueSize` validation, reflect API change | **LOW** — our Redis config fields are additive, should merge cleanly |
| `internal/core/core.go` | `IsTLS→Encryption` rename, JWT issuer/audience params, auth reload comparison | **MEDIUM** — our stream registry wiring is in `createResources`/`closeResources`, may conflict structurally |
| `internal/core/path_manager.go` | `Authenticate()` returns user string, `atomic.Int64` struct, `pendingRequests` changes, user field in response structs | **HIGH** — our stream registry hooks into `doAddPublisher`/`doAddReader` |
| `internal/api/api.go` | `Authenticate()` signature, `recordingsOfPath` moved to new file, status constants, listener labels | **MEDIUM** — our stream registry routes are additive but auth interface changed |

## gortmplib Update Needed?
- **Yes** — upstream bumped from 0.3.0 to 0.3.1 (patch)
- Need to check what changed in 0.3.1 and whether any of our patches in `gortmplib-patched/` conflict
- Our `replace` directive in go.mod keeps us on `gortmplib-patched/`, so the upstream bump won't auto-apply
- **Action**: diff gortmplib 0.3.0..0.3.1, rebase our patches onto 0.3.1, update `gortmplib-patched/go.mod`

## Recommendation
**REVIEW** — conflicts are manageable but require careful handling:
1. The `Authenticate()` signature change touches every auth call site including our stream registry
2. The `atomic.Int64` migration in path_manager affects areas near our hooks
3. The directory traversal security fix should be picked up ASAP
4. gortmplib patch rebase needed (minor — only a patch version)
