# Publisher Stream Limit

**Date:** 2026-04-18  
**Status:** Approved

## Context

The system ingests RTMP streams from 50+ cheap CCTV cameras. Without a global publisher ceiling, a misconfiguration or camera storm could flood the server with thousands of concurrent publishing paths, exhausting memory and goroutines. This change adds a configurable hard limit with hysteresis so the server sheds excess publishers cleanly before that happens.

## Requirements

- Reject new publisher connections once active publisher count reaches `MaxPublishers` (default 2000).
- Resume accepting publishers once count drops to `MaxPublishers - PublisherHysteresis` (default 1900).
- Both values configurable via the existing YAML config system.
- `MaxPublishers = 0` means unlimited (disabled).
- Rejected publishers receive a standard error response — no special handling needed for cheap cameras.

## Architecture

### Config (`internal/conf/conf.go`)

Add two fields to the `Conf` struct after the `StreamRegistry` block:

```go
MaxPublishers        int `json:"maxPublishers"`
PublisherHysteresis  int `json:"publisherHysteresis"`
```

Set defaults in `setDefaults()`:
- `MaxPublishers = 2000`
- `PublisherHysteresis = 100`

### Path Manager (`internal/core/path_manager.go`)

Add to `pathManager` struct:
```go
maxPublishers       int
publisherHysteresis int
activePublishers    int
publisherLimitHit   bool
```

**Counter lifecycle — `doSetPathReady`:**
```go
pm.activePublishers++
if pm.maxPublishers > 0 && pm.activePublishers >= pm.maxPublishers {
    pm.publisherLimitHit = true
}
```

**Counter lifecycle — `doSetPathNotReady`:**
```go
pm.activePublishers--
threshold := pm.maxPublishers - pm.publisherHysteresis
if pm.publisherLimitHit && pm.activePublishers < threshold {
    pm.publisherLimitHit = false
}
```

**Enforcement — `doAddPublisher`** (after auth check, before `createPath`):
```go
if pm.maxPublishers > 0 && pm.publisherLimitHit {
    req.Res <- defs.PathAddPublisherRes{Err: fmt.Errorf("maximum publisher count reached")}
    return
}
```

No mutex required — all three functions run on the path_manager's single goroutine.

### Core wiring (`internal/core/core.go`)

Pass new fields when constructing `pathManager` (lines ~432–446):
```go
maxPublishers:       p.conf.MaxPublishers,
publisherHysteresis: p.conf.PublisherHysteresis,
```

## Files to Modify

| File | Change |
|------|--------|
| `internal/conf/conf.go` | Add `MaxPublishers`, `PublisherHysteresis` fields + defaults |
| `internal/core/path_manager.go` | Add counter fields, update `doSetPathReady`, `doSetPathNotReady`, `doAddPublisher` |
| `internal/core/core.go` | Pass new conf fields to `pathManager` constructor |

## Verification

1. Set `maxPublishers: 3` and `publisherHysteresis: 1` in `mediamtx.yml`.
2. Push 4 simultaneous RTMP streams — 4th should be rejected.
3. Drop one stream — count goes to 2 (below threshold of 2), push a new stream — should be accepted.
4. Reset to defaults (2000/100) and verify `maxPublishers: 0` allows unlimited publishers.
