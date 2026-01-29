# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

MediaMTX is a zero-dependency real-time media server written in Go that routes media streams between protocols (RTSP, RTMP, HLS, WebRTC, SRT). It acts as a "media router" enabling publish, read, proxy, record, and playback of video/audio streams.

Repository: https://github.com/bluenviron/mediamtx

## Build Commands

```bash
# Build locally
go build -o mediamtx

# Build with self-update support
go build -tags enableUpgrade -o mediamtx

# Cross-platform binaries (requires Docker)
make binaries
```

## Testing

```bash
# Run all tests locally (recommended for development)
make test-nodocker

# Run only internal package tests (excludes core)
go test -v -race ./internal/...

# Run core tests separately
go test -v -race ./internal/core

# Run a single test
go test -v -race ./internal/conf -run TestName

# E2E tests (requires Docker)
make test-e2e
```

## Linting & Formatting

```bash
# Format code (uses gofumpt via Docker)
make format

# Run all linters
make lint

# Run Go linter only (faster, for quick checks)
make lint-go

# Check go.mod is tidy
make lint-go-mod
```

## Architecture

### Entry Point
`main.go` bootstraps `core.New()` which orchestrates all subsystems.

### Core Components (internal/)

- **core/** - Main server orchestration, path management, configuration loading
- **conf/** - YAML config parsing with environment variable substitution
- **stream/** - Media stream abstraction with track management and remuxing
- **servers/** - Protocol server implementations (RTSP, RTMP, HLS, WebRTC, SRT)
- **protocols/** - Lower-level protocol handling (MPEG-TS, WebSocket, WHIP)
- **api/** - REST API endpoints using Gin framework
- **auth/** - Authentication (internal, HTTP-based, JWT)
- **recorder/** - Stream recording (fMP4, MPEG-TS formats)
- **playback/** - Recorded stream playback

### Data Flow
```
Publisher → Path → Stream → Readers (via protocol servers)
                     ↓
                  Recorder → Recording Store → Playback API
```

### Key Patterns
- Interface-based abstraction for pluggability
- Goroutine-based concurrency with context cancellation
- Configuration-driven with hot-reload support (`confwatcher/`)
- Test mocks in `internal/test/`

## Configuration

Main config file: `mediamtx.yml`
- Global settings, authentication, protocol configs, recording, and path definitions
- Supports environment variable substitution
- Hot-reload without disconnecting clients

## API

OpenAPI spec: `api/openapi.yaml`
- Path listing/management
- Configuration CRUD
- Recording operations
- Protocol statistics

## Key Dependencies

- `gortsplib/v5`, `gortmplib`, `gohlslib/v2` - Protocol implementations (bluenviron)
- `pion/webrtc/v4` - WebRTC
- `datarhei/gosrt` - SRT protocol
- `gin-gonic/gin` - HTTP framework
- `golang-jwt/jwt/v5` - JWT authentication
