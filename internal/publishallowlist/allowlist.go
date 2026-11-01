// Package publishallowlist restricts publishing to stream IDs known to the backend.
package publishallowlist

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/bluenviron/mediamtx/internal/logger"
)

const lookupTimeout = 2 * time.Second

// Allowlist checks stream IDs against a Redis set maintained by the backend.
type Allowlist struct {
	RedisAddress  string
	RedisPassword string
	RedisDB       int
	Key           string
	PathPrefix    string
	FailOpen      bool
	Parent        logger.Writer

	client *redis.Client
}

// Initialize creates the Redis client. Connections are established on first use,
// so a Redis outage at boot does not prevent the server from starting.
func (a *Allowlist) Initialize() {
	a.client = redis.NewClient(&redis.Options{
		Addr:         a.RedisAddress,
		Password:     a.RedisPassword,
		DB:           a.RedisDB,
		DialTimeout:  lookupTimeout,
		ReadTimeout:  lookupTimeout,
		WriteTimeout: lookupTimeout,
	})
}

// Close closes the Redis client.
func (a *Allowlist) Close() {
	a.client.Close() //nolint:errcheck
}

// Check returns an error when publishing to pathName is not allowed.
// It performs network I/O and must not be called from the path manager loop.
func (a *Allowlist) Check(pathName string) error {
	streamID, ok := strings.CutPrefix(pathName, a.PathPrefix)
	if !ok || streamID == "" || strings.Contains(streamID, "/") {
		return fmt.Errorf("path '%s' is not a registered stream path", pathName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
	defer cancel()

	member, err := a.client.SIsMember(ctx, a.Key, streamID).Result()
	if err != nil {
		return a.onUnavailable(pathName, err)
	}
	if member {
		return nil
	}

	// a missing set means that the backend has not synced yet, not that every camera is unknown.
	exists, err := a.client.Exists(ctx, a.Key).Result()
	if err != nil {
		return a.onUnavailable(pathName, err)
	}
	if exists == 0 {
		return a.onUnavailable(pathName, fmt.Errorf("set '%s' does not exist", a.Key))
	}

	return fmt.Errorf("stream '%s' is not registered", streamID)
}

func (a *Allowlist) onUnavailable(pathName string, err error) error {
	if a.FailOpen {
		a.Parent.Log(logger.Warn, "[publish allowlist] allowing '%s' without a check: %v", pathName, err)
		return nil
	}
	return fmt.Errorf("publish allowlist unavailable: %w", err)
}
