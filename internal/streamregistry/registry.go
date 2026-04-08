package streamregistry

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/bluenviron/mediamtx/internal/logger"
)

// StreamInfo contains the server information for a stream.
type StreamInfo struct {
	Server string `json:"server"`
	API    string `json:"api"`
}

// Registry is a Redis-backed stream-to-server registry.
type Registry struct {
	RedisAddress  string
	RedisPassword string
	RedisDB       int
	ServerID      string
	APIAddress    string
	TTL           time.Duration
	Parent        logger.Writer

	client *redis.Client
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	mu     sync.Mutex
	paths  map[string]struct{}
}

func (r *Registry) log(level logger.Level, format string, args ...any) {
	r.Parent.Log(level, "[stream registry] "+format, args...)
}

func redisKey(streamID string) string {
	return "stream:" + streamID
}

// Initialize connects to Redis and starts the heartbeat goroutine.
func (r *Registry) Initialize() error {
	if r.ServerID == "" {
		hostname, err := os.Hostname()
		if err != nil {
			return fmt.Errorf("stream registry: unable to determine hostname: %w", err)
		}
		r.ServerID = hostname
	}

	r.client = redis.NewClient(&redis.Options{
		Addr:     r.RedisAddress,
		Password: r.RedisPassword,
		DB:       r.RedisDB,
	})

	r.ctx, r.cancel = context.WithCancel(context.Background())
	r.paths = make(map[string]struct{})

	err := r.client.Ping(r.ctx).Err()
	if err != nil {
		r.client.Close()
		return fmt.Errorf("stream registry: Redis connection failed: %w", err)
	}

	r.log(logger.Info, "connected to Redis at %s (server ID: %s)", r.RedisAddress, r.ServerID)

	r.wg.Add(1)
	go r.refreshLoop()

	return nil
}

// Close deregisters all tracked keys and shuts down.
func (r *Registry) Close() {
	r.mu.Lock()
	keys := make([]string, 0, len(r.paths))
	for name := range r.paths {
		keys = append(keys, redisKey(name))
	}
	r.paths = nil
	r.mu.Unlock()

	if len(keys) > 0 {
		pipe := r.client.Pipeline()
		for _, key := range keys {
			pipe.Del(r.ctx, key)
		}
		_, err := pipe.Exec(r.ctx)
		if err != nil {
			r.log(logger.Warn, "failed to deregister keys on close: %v", err)
		}
	}

	r.cancel()
	r.wg.Wait()
	r.client.Close()

	r.log(logger.Info, "closed")
}

func (r *Registry) infoJSON() string {
	info := StreamInfo{
		Server: r.ServerID,
		API:    r.APIAddress,
	}
	b, _ := json.Marshal(info)
	return string(b)
}

// Register adds a stream to the registry asynchronously.
func (r *Registry) Register(streamID string) {
	r.mu.Lock()
	r.paths[streamID] = struct{}{}
	r.mu.Unlock()

	go func() {
		err := r.client.Set(r.ctx, redisKey(streamID), r.infoJSON(), r.TTL).Err()
		if err != nil {
			r.log(logger.Warn, "failed to register stream %s: %v", streamID, err)
		}
	}()
}

// Deregister removes a stream from the registry asynchronously.
func (r *Registry) Deregister(streamID string) {
	r.mu.Lock()
	delete(r.paths, streamID)
	r.mu.Unlock()

	go func() {
		err := r.client.Del(r.ctx, redisKey(streamID)).Err()
		if err != nil {
			r.log(logger.Warn, "failed to deregister stream %s: %v", streamID, err)
		}
	}()
}

// RegisterAll bulk-registers streams via a Redis pipeline.
func (r *Registry) RegisterAll(names []string) {
	if len(names) == 0 {
		return
	}

	r.mu.Lock()
	for _, name := range names {
		r.paths[name] = struct{}{}
	}
	r.mu.Unlock()

	val := r.infoJSON()
	pipe := r.client.Pipeline()
	for _, name := range names {
		pipe.Set(r.ctx, redisKey(name), val, r.TTL)
	}
	_, err := pipe.Exec(r.ctx)
	if err != nil {
		r.log(logger.Warn, "failed to bulk-register %d streams: %v", len(names), err)
	} else {
		r.log(logger.Info, "registered %d existing streams", len(names))
	}
}

// Lookup retrieves stream info from Redis. Returns (nil, nil) if the key doesn't exist.
func (r *Registry) Lookup(streamID string) (*StreamInfo, error) {
	val, err := r.client.Get(r.ctx, redisKey(streamID)).Result()
	if err == redis.Nil {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	var info StreamInfo
	err = json.Unmarshal([]byte(val), &info)
	if err != nil {
		return nil, err
	}

	return &info, nil
}

func (r *Registry) refreshLoop() {
	defer r.wg.Done()

	interval := r.TTL / 2
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.refreshAll()
		case <-r.ctx.Done():
			return
		}
	}
}

func (r *Registry) refreshAll() {
	r.mu.Lock()
	keys := make([]string, 0, len(r.paths))
	for name := range r.paths {
		keys = append(keys, name)
	}
	r.mu.Unlock()

	if len(keys) == 0 {
		return
	}

	pipe := r.client.Pipeline()
	for _, name := range keys {
		pipe.Expire(r.ctx, redisKey(name), r.TTL)
	}
	_, err := pipe.Exec(r.ctx)
	if err != nil {
		r.log(logger.Warn, "failed to refresh TTL for %d streams: %v", len(keys), err)
	}
}
