# Pre-Merge Test Suite — Design

**Date:** 2026-06-07
**Status:** Approved
**Author:** orbit-launch maintainers

## Problem

orbit-launch is a fork of `bluenviron/mediamtx` carrying 25 custom commits across 6 feature
areas (RTMP camera patches, chaos suite, Redis stream registry, publisher limits, HLS/recording
config, release pipeline). The dangerous moment is an **upstream merge silently breaking the fork
delta** before it reaches production.

Today there is no gate:
- Upstream Go tests (`make test` / `test-e2e`) run only on the `main` branch — never on `staging`.
- The headline RTMP camera patches are validated only by `.ai/chaos_test.py`, which needs a live
  server and isn't wired into any automation.
- Publisher limits and the stream registry have **no tests at all**.

## Goal

A pre-merge gate — runnable both locally and as a CI gate on PRs into `production` — that proves
the fork delta is intact and functional before promotion.

## Decisions

- **Form factor:** Both. One orchestrator script reused locally and in CI.
- **Coverage:** All four layers (fork-delta integrity, custom-feature Go tests, live chaos, upstream tests).
- **Chaos server:** Booted in CI from the freshly built binary (self-contained, no external dependency).
- **Gated branch:** PRs into `production`.
- **chaos_test.py:** Will be edited to read its target from env and exit non-zero on test failure.
- **Redis in tests:** `github.com/alicebob/miniredis/v2` (in-memory, runs inside `go test`) — no service container.

## Architecture

Layered, fastest-first. The orchestrator runs layers in order; CI runs them as separate jobs for
parallelism and clear failure attribution.

### Entry points
- `scripts/pre-merge-check.sh` — orchestrator. `--fast` runs layers 1–3 (no live server); full run adds chaos.
- `make pre-merge` — wraps the script.
- `.github/workflows/pre-merge.yml` — triggers on `pull_request` into `production`; each layer is a required job.

### Layer 1 — Fork-delta integrity (`scripts/fork-delta-check.sh`, static, ~seconds)
Greps for one documented sentinel per delta item; failure names the clobbered patch.
- `go.mod` contains `replace github.com/bluenviron/gortmplib => ./gortmplib-patched`
- 5 gortmplib patch sentinels present:
  1. `gortmplib-patched/pkg/handshake/c0s0.go` — accept any RTMP version
  2. `gortmplib-patched/pkg/handshake/handshake.go` — bundled S0+S1+S2 single write
  3. `gortmplib-patched/pkg/rawmessage/reader.go` — lenient chunks / extended CSIDs
  4. `gortmplib-patched/reader.go` — AVC/HEVC/AV1 nil-guards; no `panic("should not happen")`
  5. `gortmplib-patched/server_conn.go` — synthesized tcURL / `_result` responses / StreamBegin
- `mediamtx.yml` has `maxPublishers`, `publisherHysteresis`, LL-HLS + 15-min recording keys
- `internal/conf/conf.go` has `MaxPublishers` / `PublisherHysteresis` / `StreamRegistry*` fields

### Layer 2 — Build + upstream tests
`go build ./...`, `go vet ./...`, `make test-nodocker`. Catches upstream regressions on staging,
which today only runs on `main`.

### Layer 3 — New Go tests for custom features
- `internal/core/publisher_limit_test.go` — boot core with a low `maxPublishers`; assert the
  over-cap publisher is rejected and hysteresis re-enables after dropping below threshold. Modeled
  on the existing `internal/core/path_manager_test.go`.
- `internal/streamregistry/registry_test.go` — using `miniredis`: Register → Lookup, heartbeat
  refresh, TTL expiry, Deregister.

### Layer 4 — Live RTMP chaos (CI-booted)
- Edit `.ai/chaos_test.py`: read `CHAOS_TARGET_HOST` / `CHAOS_TARGET_PORT` from env (defaults
  unchanged); exit non-zero when any test fails (currently only exits 1 on a connect failure).
- CI job: build binary → write a minimal RTMP-enabled config → boot it → wait for `:1935` → run
  the 49-test suite against `localhost` → fail on non-zero exit.

### CI workflow
`pull_request: branches: [production]`. Jobs: `integrity` (Layer 1), `go-tests` (Layers 2–3,
setup-go 1.26), `chaos` (Layer 4). All required for merge.

## Out of scope
- Replacing or restructuring the existing upstream `test.yml`.
- Deploy/release automation (`release.sh` is unchanged).
- Rewriting the chaos suite — only target parameterization + exit code.

## Re-verify on every upstream sync
This suite operationalizes the [[project-fork-delta]] "on every sync, re-verify" checklist. When
the delta changes (new custom commit / removed feature), update Layer 1 sentinels and Layer 3 tests.
