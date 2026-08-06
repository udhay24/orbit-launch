# Publisher Stream Limit Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add a configurable global publisher ceiling (default 2000) with hysteresis (default 100) that rejects new RTMP publishers when the limit is hit and resumes accepting once the count drops below the threshold.

**Architecture:** Two config fields (`MaxPublishers`, `PublisherHysteresis`) are added to `Conf`. The `pathManager` tracks active publishers via a counter incremented in `doSetPathReady` and decremented in `doSetPathNotReady`, with a boolean gate `publisherLimitHit` that `doAddPublisher` checks before allowing a new publisher in. All three functions run on the path_manager's single goroutine so no mutex is needed.

**Tech Stack:** Go, existing MediaMTX config/YAML system, no new dependencies.

---

### Task 1: Add config fields to `Conf`

**Files:**
- Modify: `internal/conf/conf.go:400-401` (after StreamRegistry block), `internal/conf/conf.go:533-534` (setDefaults)

- [ ] **Step 1: Add struct fields**

In `internal/conf/conf.go`, after line 400 (`StreamRegistryTTL Duration ...`), add:

```go
	// Publisher limits
	MaxPublishers       int `json:"maxPublishers"`
	PublisherHysteresis int `json:"publisherHysteresis"`
```

- [ ] **Step 2: Add defaults**

In `setDefaults()` (around line 533, after the stream registry defaults block):

```go
	// Publisher limits
	conf.MaxPublishers = 2000
	conf.PublisherHysteresis = 100
```

- [ ] **Step 3: Verify it compiles**

```bash
cd /Users/udhay/GolandProjects/orbit-launch && go build ./internal/conf/...
```

Expected: no output (clean build).

- [ ] **Step 4: Commit**

```bash
git add internal/conf/conf.go
git commit -m "feat(conf): add MaxPublishers and PublisherHysteresis config fields"
```

---

### Task 2: Add counter fields to `pathManager`

**Files:**
- Modify: `internal/core/path_manager.go:99-106` (struct fields)

- [ ] **Step 1: Add fields to struct**

In `internal/core/path_manager.go`, after line 99 (`parent pathManagerParent`), add:

```go
	maxPublishers       int
	publisherHysteresis int
	activePublishers    int
	publisherLimitHit   bool
```

- [ ] **Step 2: Verify it compiles**

```bash
cd /Users/udhay/GolandProjects/orbit-launch && go build ./internal/core/...
```

Expected: no output (clean build).

- [ ] **Step 3: Commit**

```bash
git add internal/core/path_manager.go
git commit -m "feat(path_manager): add publisher limit counter fields"
```

---

### Task 3: Wire config fields into `pathManager` construction

**Files:**
- Modify: `internal/core/core.go:432-446` (pathManager struct literal)

- [ ] **Step 1: Pass config values**

In `internal/core/core.go`, inside the `&pathManager{...}` literal (around lines 432–446), add after `metrics: p.metrics,`:

```go
			maxPublishers:       p.conf.MaxPublishers,
			publisherHysteresis: p.conf.PublisherHysteresis,
```

- [ ] **Step 2: Verify it compiles**

```bash
cd /Users/udhay/GolandProjects/orbit-launch && go build ./internal/core/...
```

Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add internal/core/core.go
git commit -m "feat(core): wire MaxPublishers and PublisherHysteresis into pathManager"
```

---

### Task 4: Increment counter in `doSetPathReady`

**Files:**
- Modify: `internal/core/path_manager.go:335-349`

- [ ] **Step 1: Update `doSetPathReady`**

In `internal/core/path_manager.go`, at the end of `doSetPathReady` (after `pm.streamRegistry.Register(pa.name)` block, before the closing `}`), add:

```go
	pm.activePublishers++
	if pm.maxPublishers > 0 && pm.activePublishers >= pm.maxPublishers {
		pm.publisherLimitHit = true
	}
```

The full function should look like:

```go
func (pm *pathManager) doSetPathReady(pa *path) {
	if pa2, ok := pm.paths[pa.name]; !ok || pa2 != pa {
		return
	}

	pm.paths[pa.name].ready = true

	if pm.hlsServer != nil {
		pm.hlsServer.PathReady(pa)
	}

	if pm.streamRegistry != nil {
		pm.streamRegistry.Register(pa.name)
	}

	pm.activePublishers++
	if pm.maxPublishers > 0 && pm.activePublishers >= pm.maxPublishers {
		pm.publisherLimitHit = true
	}
}
```

- [ ] **Step 2: Verify it compiles**

```bash
cd /Users/udhay/GolandProjects/orbit-launch && go build ./internal/core/...
```

Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add internal/core/path_manager.go
git commit -m "feat(path_manager): increment activePublishers on path ready"
```

---

### Task 5: Decrement counter in `doSetPathNotReady`

**Files:**
- Modify: `internal/core/path_manager.go:351-365`

- [ ] **Step 1: Update `doSetPathNotReady`**

In `internal/core/path_manager.go`, at the end of `doSetPathNotReady` (after the `pm.streamRegistry.Deregister` block, before the closing `}`), add:

```go
	if pm.activePublishers > 0 {
		pm.activePublishers--
	}
	if pm.publisherLimitHit && pm.activePublishers < pm.maxPublishers-pm.publisherHysteresis {
		pm.publisherLimitHit = false
	}
```

The full function should look like:

```go
func (pm *pathManager) doSetPathNotReady(pa *path) {
	if pa2, ok := pm.paths[pa.name]; !ok || pa2 != pa {
		return
	}

	pm.paths[pa.name].ready = false

	if pm.hlsServer != nil {
		pm.hlsServer.PathNotReady(pa)
	}

	if pm.streamRegistry != nil {
		pm.streamRegistry.Deregister(pa.name)
	}

	if pm.activePublishers > 0 {
		pm.activePublishers--
	}
	if pm.publisherLimitHit && pm.activePublishers < pm.maxPublishers-pm.publisherHysteresis {
		pm.publisherLimitHit = false
	}
}
```

- [ ] **Step 2: Verify it compiles**

```bash
cd /Users/udhay/GolandProjects/orbit-launch && go build ./internal/core/...
```

Expected: no output.

- [ ] **Step 3: Commit**

```bash
git add internal/core/path_manager.go
git commit -m "feat(path_manager): decrement activePublishers and clear limit flag on path not ready"
```

---

### Task 6: Enforce limit in `doAddPublisher`

**Files:**
- Modify: `internal/core/path_manager.go:435-465`

- [ ] **Step 1: Add enforcement gate**

In `internal/core/path_manager.go`, inside `doAddPublisher`, after the auth block (after line ~453 where `req.Res <- defs.PathAddPublisherRes{Err: err2}` returns) and before the `createPath` block (line ~456), add:

```go
	if pm.maxPublishers > 0 && pm.publisherLimitHit {
		req.Res <- defs.PathAddPublisherRes{Err: fmt.Errorf("maximum publisher count reached")}
		return
	}
```

The relevant section of `doAddPublisher` should look like:

```go
	if !req.AccessRequest.SkipAuth {
		err2 := pm.authManager.Authenticate(req.AccessRequest.ToAuthRequest())
		if err2 != nil {
			req.Res <- defs.PathAddPublisherRes{Err: err2}
			return
		}
	}

	if pm.maxPublishers > 0 && pm.publisherLimitHit {
		req.Res <- defs.PathAddPublisherRes{Err: fmt.Errorf("maximum publisher count reached")}
		return
	}

	// create path if it doesn't exist
	if _, ok := pm.paths[req.AccessRequest.Name]; !ok {
		pm.createPath(pathConf, req.AccessRequest.Name, pathMatches)
	}
```

- [ ] **Step 2: Verify it compiles**

```bash
cd /Users/udhay/GolandProjects/orbit-launch && go build ./...
```

Expected: no output.

- [ ] **Step 3: Run existing tests to confirm no regressions**

```bash
cd /Users/udhay/GolandProjects/orbit-launch && go test ./internal/core/... ./internal/conf/... -v 2>&1 | tail -30
```

Expected: all existing tests pass (PASS lines, no FAIL).

- [ ] **Step 4: Commit**

```bash
git add internal/core/path_manager.go
git commit -m "feat(path_manager): reject new publishers when limit is hit"
```

---

### Task 7: Manual end-to-end verification

> This is a runtime test — no automated test is possible without a live RTMP client. Follow these steps manually.

- [ ] **Step 1: Set a low limit in config**

In `mediamtx.yml`, set:

```yaml
maxPublishers: 3
publisherHysteresis: 1
```

- [ ] **Step 2: Start the server**

```bash
cd /Users/udhay/GolandProjects/orbit-launch && go run . mediamtx.yml
```

- [ ] **Step 3: Push 3 streams — all should be accepted**

Using FFmpeg (or any RTMP tool), push 3 streams:

```bash
ffmpeg -re -f lavfi -i testsrc -f flv rtmp://localhost:1935/stream1
ffmpeg -re -f lavfi -i testsrc -f flv rtmp://localhost:1935/stream2
ffmpeg -re -f lavfi -i testsrc -f flv rtmp://localhost:1935/stream3
```

Expected: all 3 connect and publish.

- [ ] **Step 4: Push a 4th stream — should be rejected**

```bash
ffmpeg -re -f lavfi -i testsrc -f flv rtmp://localhost:1935/stream4
```

Expected: connection is rejected / FFmpeg reports an error.

- [ ] **Step 5: Drop one stream, then push again — should be accepted**

Stop `stream3`. Then push:

```bash
ffmpeg -re -f lavfi -i testsrc -f flv rtmp://localhost:1935/stream4
```

Expected: accepted (count dropped to 2, below threshold of `3 - 1 = 2`).

- [ ] **Step 6: Verify unlimited mode**

Set `maxPublishers: 0` in config, restart, push 4+ streams — all should be accepted.

- [ ] **Step 7: Restore production defaults**

```yaml
maxPublishers: 2000
publisherHysteresis: 100
```

```bash
git add mediamtx.yml
git commit -m "feat(config): set production publisher limits (2000 max, 100 hysteresis)"
```
