You are running the upstream sync check for orbit-launch (a fork of bluenviron/mediamtx). Run locally with full git/gh access.

## Context

orbit-launch has these custom patches that must be preserved:

1. **gortmplib-patched/** — local fork via `go.mod replace` directive:
   - `pkg/handshake/c0s0.go` — accept non-standard RTMP versions
   - `pkg/handshake/handshake.go` — bundled S0+S1+S2, skip C2 validation
   - `pkg/rawmessage/reader.go` — lenient chunk types 1/2/3, extended chunk stream IDs
   - `server_conn.go` — optional tcUrl, releaseStream/FCPublish/FCUnpublish responses, StreamBegin before onStatus
   - `reader.go` — nil checks for codec configs

2. **Stream registry** — Redis-backed stream-to-server lookup:
   - `internal/streamregistry/registry.go`, `internal/api/api_stream_registry.go`
   - Config in `internal/conf/conf.go`, wiring in `core.go`, `path_manager.go`, `api.go`

3. **Recorder enhancements** — `internal/recorder/recorder.go`

## Conflict-prone files
- `internal/core/core.go` (createResources/closeResources lifecycle)
- `internal/core/path_manager.go` (run loop, ready/not-ready handlers)
- `internal/api/api.go` (route registration, struct fields)
- `internal/conf/conf.go` (struct fields, defaults)
- `go.mod` (gortmplib version — we use replace directive)

## Steps

### 1. Fetch upstream and find new commits
```bash
git remote add upstream https://github.com/bluenviron/mediamtx.git 2>/dev/null || true
git fetch upstream
```

Read `.claude/sync-reports/LAST_SYNC.md` for the last synced commit hash. Then:
```bash
git log --oneline <last_sync_hash>..upstream/main
```

If no new commits, say so and stop.

### 2. Analyze commits
Categorize each as: **Feature**, **Bug fix**, **Dependency bump**, **Docs**, **Breaking**, **Conflict risk**.

For features and bug fixes, read the actual diff. For dep bumps, note package + version.

### 3. Check for conflicts
```bash
git diff main..upstream/main -- internal/core/core.go internal/core/path_manager.go internal/api/api.go internal/conf/conf.go go.mod
```
Check if upstream bumped gortmplib — if so, we need to update `gortmplib-patched/`.

### 4. Write sync report
Write to `.claude/sync-reports/YYYY-MM-DD-upstream-sync.md`:

```
# Upstream Sync Report — YYYY-MM-DD

## Summary
- X new commits since last sync (<hash>)
- Y features, Z bug fixes, W dep bumps
- Conflict risk: HIGH/MEDIUM/LOW/NONE

## Important Changes
### Features
### Bug Fixes
### Breaking Changes
### Dependency Bumps
### Docs Only (N commits, no action needed)

## Conflict Analysis
- Each conflict-prone file: touched? what changed? conflicts with our patches?

## gortmplib Update Needed?
- Upstream bump? Version? Rebase needed?

## Recommendation
MERGE (safe) / REVIEW (conflicts but manageable) / SKIP (too risky)
```

### 5. Present the report and ask what to do
Show the report summary and ask the user:
- **Merge all** — create `sync/upstream-YYYY-MM-DD` branch, merge upstream, resolve conflicts, build, vet, push, open PR
- **Cherry-pick** — let user pick specific commits to integrate
- **Skip** — just save the report, no code changes

### 6. If merging
```bash
git checkout -b sync/upstream-YYYY-MM-DD main
git merge upstream/main
```

Resolve conflicts:
- Keep our additions (stream registry, recorder) while accepting upstream structural changes
- Keep our `replace` directive in go.mod
- If gortmplib bumped: update `gortmplib-patched/go.mod`, run `go mod tidy && go mod vendor`

Verify:
```bash
go build ./...
go vet ./...
```

If passing, commit, push, and open PR via `gh pr create`.

### 7. Update LAST_SYNC.md
Update `.claude/sync-reports/LAST_SYNC.md` with new upstream HEAD hash and date.
