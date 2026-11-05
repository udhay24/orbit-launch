// Package cameraaccess checks cameras against Redis sets maintained by the orchestrator:
// a blocklist of stream IDs that may not publish, and which application owns each camera.
package cameraaccess

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/bluenviron/mediamtx/internal/logger"
)

const (
	lookupTimeout = 2 * time.Second
	ownedCacheTTL = 24 * time.Hour
)

// Access reads the orchestrator's camera sets.
type Access struct {
	RedisAddress  string
	RedisPassword string
	RedisDB       int
	// BlockKey is the set of stream IDs that may not publish.
	BlockKey string
	// OwnerKey prefixes the sets <OwnerKey>:owned:<key ID> of cameras each application owns.
	OwnerKey   string
	PathPrefix string
	// ExemptKeys may read any camera, like the orchestrator's key.
	ExemptKeys []string
	Parent     logger.Writer

	client *redis.Client
	owned  sync.Map
}

// Initialize creates the Redis client. Connections are established on first use,
// so a Redis outage at boot does not prevent the server from starting.
func (a *Access) Initialize() {
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
func (a *Access) Close() {
	a.client.Close() //nolint:errcheck
}

// Check returns an error when the camera publishing to pathName is blocked.
// It performs network I/O and must not be called from the path manager loop.
func (a *Access) Check(pathName string) error {
	streamID := strings.TrimPrefix(pathName, a.PathPrefix)

	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
	defer cancel()

	blocked, err := a.client.SIsMember(ctx, a.BlockKey, streamID).Result()
	if err != nil {
		// letting cameras in is the only safe choice: refusing would take every camera
		// down with Redis. Error, because the shipped logLevel hides warnings.
		a.Parent.Log(logger.Error, "[camera access] allowing '%s' without a blocklist check: %v", pathName, err)
		return nil
	}
	if blocked {
		return fmt.Errorf("stream '%s' is blocked", streamID)
	}
	return nil
}

// CheckViewToken returns an error when a token signed with keyID cannot read pathName.
// Each application has its own key, so a key stolen from one must not open another's
// cameras. Like Check, it performs network I/O.
func (a *Access) CheckViewToken(keyID string, pathName string) error {
	if slices.Contains(a.ExemptKeys, keyID) {
		return nil
	}

	streamID, ok := strings.CutPrefix(pathName, a.PathPrefix)
	if !ok || streamID == "" {
		return fmt.Errorf("path '%s' is not a registered stream path", pathName)
	}

	ctx, cancel := context.WithTimeout(context.Background(), lookupTimeout)
	defer cancel()

	cacheKey := keyID + "/" + streamID

	owned, err := a.client.SIsMember(ctx, a.OwnerKey+":owned:"+keyID, streamID).Result()
	if err != nil {
		// never let unknown pairs in: during an outage any application's key would open
		// any camera. Pairs already seen keep working, so viewers survive a Redis outage.
		if seenAt, ok := a.owned.Load(cacheKey); ok && time.Since(seenAt.(time.Time)) < ownedCacheTTL {
			return nil
		}
		return fmt.Errorf("camera ownership unavailable: %w", err)
	}
	if !owned {
		a.owned.Delete(cacheKey)
		return fmt.Errorf("key '%s' does not own stream '%s'", keyID, streamID)
	}

	a.owned.Store(cacheKey, time.Now())
	return nil
}
