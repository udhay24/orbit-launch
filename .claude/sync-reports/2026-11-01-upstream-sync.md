# Upstream Sync Report — 2026-11-01

## Summary
- 238 new commits since last sync (03a6e91e), up to 168e1723 (v1.19.x to v1.21.3)
- About 145 features and fixes, 93 dependency bumps
- Conflict risk was HIGH; all resolved. Go 1.26 to 1.27, gortmplib v1.0.0 to v1.0.5

## Important Changes
### Features
- Sessions close when their JWT expires (#6389), HTTP auth can set an expiry (#6391)
- API endpoints to manage internal users (#6293)
- `--validate-conf` flag (#6140)
- `alwaysAvailableRecorded` (#6182)
- WebRTC and MoQ stream forwarding (#6099, #6145), MoQ pull (#6111)
- `webrtcIPsFromInterfacesExcludeList` (#5788), `rtspDemuxMpegts` (#6181)
- fMP4 segment duration in the recordings API (#6412)

### Bug Fixes
- Recording segments never deleted when config reloads often (#6256)
- H264 recovery point SEI treated as random access (#6349)
- H264 units without IDR no longer silently skipped (#6354)
- WebRTC panic on crafted WHEP requests (#6309)
- Deadlocks on API and config reload (#6310, #6101)
- DJI drone decoding stuck (#6243), AV1 stuck (#6242)
- Stream-time discontinuity inflating fMP4 segments (#5984)

### Breaking Changes
- Server now exits on fatal listen errors (#6415)
- `authJWTInHTTPQuery` still deprecated; the fork keeps it on because iframe players need it
- API/metrics/pprof default origins empty (#6165); fork keeps `['*']` in `mediamtx.yml`
- `authHTTPExclude` default is empty (#6103); fork keeps api/metrics/pprof excluded
- Default `recordPath` adds timezone; fork keeps its format because downstream parses it

### Dependency Bumps
gortmplib 1.0.5, gortsplib 5.6.7, gohlslib 2.4.7, mediacommon 2.9.7, pion/webrtc 4.2.25,
quic-go 0.64.1, gosrt 0.13.0 (upstream dropped its gosrt replace; so did the fork).

## Conflict Analysis
- `core.go`, `path_manager.go`: struct realignment, new `supportsIPv6`, renamed forward-dest channels
- `rtmp/conn.go`: upstream split accept into AcceptConn/AcceptAction; fork deadline resets kept
- `go.mod`, `go.sum`: taken from upstream, fork deps re-added
- `mediamtx.yml`: 8 regions, fork values kept

## gortmplib Update Needed?
Yes, done. reader.go rebuilt on v1.0.5 with guards re-applied; StreamBegin moved into AcceptAction.
TestHandshake hangs and TestServerConn/publish fails on both old and new patched copies.

## Verification
`scripts/pre-merge-check.sh` passed: fork delta, build, Linux vet, custom tests, chaos 49/49 (100/100).

## Recommendation
MERGED locally. Not pushed.
