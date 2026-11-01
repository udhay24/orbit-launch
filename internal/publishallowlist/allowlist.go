// Package publishallowlist restricts publishing to stream IDs known to the backend.
package publishallowlist

import (
	"context"
	"fmt"
	"slices"
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
	// ExemptKeys may read any camera, like the orchestrator's key.
	ExemptKeys []string
	Parent     logger.Writer

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

// CheckViewToken returns an error when a token signed with keyID cannot read pathName.
// Each application has its own key, so a key stolen from one must not open another's
// cameras. Like Check, it performs network I/O.
func (a *Allowlist) CheckViewToken(keyID string, pathName string) error {
	if slices.Contains(a.ExemptKeys, keyID) {
		return nil
	}

	streamID, ok := strings.CutPrefix(pathName, a.PathPrefix)
	if !ok || streamID == "" {
		return fmt.Errorf("path '%s' is not a registered stream path", pathName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
	defer cancel()

	owned, err := a.client.SIsMember(ctx, a.Key+":owned:"+keyID, streamID).Result()
	if err != nil {
		return a.onUnavailable(pathName, err)
	}
	if !owned {
		return fmt.Errorf("key '%s' does not own stream '%s'", keyID, streamID)
	}

	return nil
}

func (a *Allowlist) onUnavailable(pathName string, err error) error {
	if a.FailOpen {
		// Error, because the shipped logLevel hides warnings and cameras would then be
		// let in with no trace of it.
		a.Parent.Log(logger.Error, "[publish allowlist] allowing '%s' without a check: %v", pathName, err)
		return nil
	}
	return fmt.Errorf("publish allowlist unavailable: %w", err)
}
