# Upstream Sync Report — 2026-08-06

## Summary
- **133 new commits** since local `main` (`6d1f152c`, 2026-06-03) → `upstream/main` (`03a6e91e`, 2026-08-06)
- 51 dependency bumps, 5 docs, 5 tool/base-image bumps, ~72 real changes
- **Conflict risk: MEDIUM**
- gortmplib bumped **v0.4.0 → v1.0.0** (major) — rebase required, but 4 of 5 patched files are byte-identical

Note: this sync is layered on top of an *already-merged but not-yet-promoted* batch of 195
upstream commits sitting on `staging`. `production` is 328 commits behind `upstream/main`.

## Important Changes

### Breaking / renames
- **`runOnReady` → `runOnAvailable`, `runOnNotReady` → `runOnUnavailable`** (#5957).
  **We use both** — `mediamtx.yml:759,764`, wired to the local agent on `localhost:50052`.
  Old keys retained as `deprecated:"true"` and auto-mapped with a startup warning
  (`internal/conf/path.go:945-953`), so nothing breaks today. Rename during this sync.
- `moqHTTPS2Address` → `moqHTTP2Address`, `moqHTTPS3Address` → `moqHTTP3Address` (#5841),
  plus new `moqQUICAddress`. We do not use MoQ.
- gortmplib `buildURL(tcURL, app, streamKey)` replaced by `joinURL(tcURL, streamKey)` —
  see gortmplib section below. This is the highest-risk item in the sync.

### Features
- **Native stream forwarding** (#5558) — new `internal/forward/` (RTMP/RTSP/SRT dests),
  `+117/-20` in `path_manager.go`, new `Forward` path conf, new API + metrics.
  Conceptually adjacent to our Redis stream registry; worth evaluating later, not now.
- `rtmp, rtsp: support PROXY protocol` (#5754) — adds `rtmpTrustedProxies` / `rtspTrustedProxies`.
- `runOnOnline` / `runOnOffline` hooks (#5956) — distinct from available/unavailable.
- `rtspScale` parameter to inject Scale header on PLAY (#5800).
- rpicamera: MJPEG primary stream, H264 secondary stream, unified `rpiCameraH264Profile`/`Level`.
- MoQ: drafts 16/17/19, native QUIC publish+read (~20 commits — new subsystem stabilizing).
- OpenAPI definition of the Playback server (#5546); most of OpenAPI now generated (#5918).

### Bug fixes (relevant to us)
- `fix(recordstore): decode timezone offset minutes correctly` (#5884) — **we record 15-min segments**
- `playback: fix panic when MP4 muxer flushes with no samples` (#5867)
- `rtsp: restore support for H264 packetization-mode 0` (#5857)
- `fix inability to read some AV1 streams with RTSP` (#6006)
- `srt: close sources immediately when path is closed` (#6038)
- `prevent truncation of 64-bit values on 32-bit platforms` (#5902)
- `improve HTTP server performance` (#5886)
- `fix support for regexp groups greater than 10` (#6033)
- `fix clearing lists with environment variables` (#5924)
- `webrtc: fix "packet lost" error when routing streams` (#6034)
- Several race conditions fixed in metrics/webrtc/hls tests

### Security
- `hls, webrtc: prevent cross-origin unauthorized access` (#5975)
- `avoid potential timing attack when validating SHA256 credentials` (#5961)
- `improve anti-brute force mechanism` (#5835)
- `redact sensitive headers in HTTP debug logs` (#5873)
- `rtsp: restrict UDP port range to 32768-60999` (#5958) — **check firewall rules**
- `restrict GitHub Actions workflow permissions` (#5840)
- Multiple CodeQL warning fixes (#6024, #6027, #6029)

### Dependency Bumps (51)
Notable: `gortmplib v0.4.0→v1.0.0`, `gortsplib/v5 v5.5.4→v5.6.3`, `gohlslib/v2 v2.4.0→v2.4.2`,
`mediacommon/v2 v2.9.0→v2.9.2`, `go-mp4 v1.6.0→v1.7.1`, `bytefmt v0.74.0→v0.83.0`,
`kong v1.15.0→v1.16.0`, `argon2 v1.5.4→v1.5.6`.
Go directive stays `1.26.0` (local toolchain matches).

### Docs Only (5 commits, no action needed)

## Conflict Analysis

Upstream churn in fork-touched files:

| File | Churn | Assessment |
|---|---|---|
| `internal/core/path_manager.go` | +117 / −20 | **Main conflict.** Additive: `chAPIForwardDest*` channels, `udpMaxPayloadSize`, `Forward` in `pathConfCanBeUpdated`, and `FindPathConf` now uses `errors.AsType[*auth.Error]` + `auth.LogAndDelayError`. Our `activePublishers` counter/hysteresis fields sit in the same struct block that upstream gofmt-realigned → expect textual conflict, trivial semantic resolution. |
| `mediamtx.yml` | +87 / −40 | Fork config diverged; ours wins. See config-drift note. |
| `internal/servers/rtmp/server.go` | +46 / −17 | PROXY protocol / TrustedProxies plumbing. |
| `internal/conf/conf.go` | +33 / −11 | New fields (`RTSPTrustedProxies`, `RTMPTrustedProxies`, `MoQQUICAddress`) + gofmt realignment where our `StreamRegistry*` / `MaxPublishers` fields live. |
| `internal/core/core.go` | +15 / −4 | Additive struct-literal fields + MoQ renames; gofmt realignment near our registry wiring. |
| `internal/servers/rtmp/conn.go` | +12 / −15 | Adapts to gortmplib v1.0.0. |
| `internal/api/api.go` | +11 / −8 | Forward-dest route registration. |

**Config drift (pre-existing, carried from the last sync):** the fork's `mediamtx.yml` never
absorbed upstream's new keys. Missing: `authJWTAudience`, `authJWTIssuer`, `hlsCDNSecret`, and
the `moq*` family. Not a boot failure (defaults apply), but the file has stopped tracking
upstream's sample config, and `TestSampleConfFile` is skipped in the pre-merge gate so nothing
flags it. `authJWTInHTTPQuery` is also still set and now deprecated.

## gortmplib Update Needed?

**Yes — v0.4.0 → v1.0.0.** Less painful than the major version implies:

| Patched file | v0.4.0 → v1.0.0 |
|---|---|
| `pkg/handshake/c0s0.go` | **identical** |
| `pkg/handshake/handshake.go` | **identical** |
| `pkg/rawmessage/reader.go` | **identical** |
| `reader.go` | **identical** (21440 bytes both) |
| `server_conn.go` | 38 lines changed |

`server_conn.go` is the only rebase. Upstream removed `buildURL(tcURL, app, streamKey)` — which
composed the URL path from `app` — and replaced it with `joinURL(tcURL, streamKey)`, which derives
the URL entirely from `tcURL`. `CheckCredentials` also moved from reading `c.app` to `c.tcURL`.

Our three hunks and their status under v1.0.0:
1. **tcURL synthesis** (`c.tcURL = "rtmp://localhost/" + c.app` when cameras omit tcUrl) — lands in
   `Initialize`, untouched region. `c.app` is still populated (`server_conn.go:154`). Because the
   synthesized value is a *full* URL (scheme + host + path), `joinURL` yields the same
   `/app/streamKey` path the old `buildURL` produced. **Semantics preserved.**
2. **`releaseStream`/`FCPublish`/`FCUnpublish`/`_checkbw`/`deleteStream` → `_result`** — new switch
   case, different lines from upstream's edits. Clean.
3. **`StreamBegin` before `onStatus`** — ~38 lines below upstream's `publish`-case edit. Clean.

Recipe (per `project-fork-delta` memory): rebuild `gortmplib-patched/` from pristine v1.0.0, then
3-way merge the patched `server_conn.go` with
`git merge-file -p --diff3 <patched> <v0.4.0> <v1.0.0>`, and copy the other 4 patched files across
unchanged. Then re-run the chaos suite — non-negotiable after a gortmplib bump.

## Recommendation

**MERGE (review).** No blocking issues. Order of operations:

1. Fast-forward `main` to `upstream/main`.
2. Branch `sync/upstream-2026-08-06` off the current integration branch (which already carries the
   195-commit batch), merge `main`.
3. Resolve conflicts — `path_manager.go` is the only one needing thought.
4. Rebase gortmplib to v1.0.0 (1 file).
5. Rename `runOnReady`/`runOnNotReady` → `runOnAvailable`/`runOnUnavailable` in `mediamtx.yml`.
6. `go build` / `go vet` / `go mod tidy`, then the full pre-merge gate **including Layer 4 chaos**.
7. Combined testing, then promote to `production`.

Deferred, not part of this sync:
- Evaluate native stream forwarding (#5558) against the Redis stream registry.
- Reconcile `mediamtx.yml` against upstream's sample and re-enable `TestSampleConfFile`.
- Verify `rtspTrustedProxies` / RTSP UDP port range 32768-60999 against firewall rules.
