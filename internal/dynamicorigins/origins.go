// Package dynamicorigins keeps a list of allowed CORS origins that applications
// register in Redis, so that a new application needs no media server change.
package dynamicorigins

import (
	"context"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/bluenviron/mediamtx/internal/logger"
)

const (
	refreshPeriod  = 15 * time.Second
	refreshTimeout = 3 * time.Second
)

// Origins is a periodically refreshed copy of a Redis set.
type Origins struct {
	RedisAddress  string
	RedisPassword string
	RedisDB       int
	Key           string
	Parent        logger.Writer

	client   *redis.Client
	current  atomic.Pointer[[]string]
	failing  bool
	ctx      context.Context
	ctxClose func()
	done     chan struct{}
}

// Initialize loads the list once and starts refreshing it.
func (o *Origins) Initialize() {
	o.client = redis.NewClient(&redis.Options{
		Addr:     o.RedisAddress,
		Password: o.RedisPassword,
		DB:       o.RedisDB,
	})
	o.ctx, o.ctxClose = context.WithCancel(context.Background())
	o.done = make(chan struct{})

	empty := []string{}
	o.current.Store(&empty)
	o.refresh()

	go o.run()
}

// Close stops refreshing.
func (o *Origins) Close() {
	o.ctxClose()
	<-o.done
	o.client.Close() //nolint:errcheck
}

// Get returns the latest list. It never touches the network, since it runs on every
// HTTP request.
func (o *Origins) Get() []string {
	return *o.current.Load()
}

func (o *Origins) run() {
	defer close(o.done)

	ticker := time.NewTicker(refreshPeriod)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			o.refresh()
		case <-o.ctx.Done():
			return
		}
	}
}

// refresh keeps the previous list on failure: a Redis outage must not break video in
// browsers that were working.
func (o *Origins) refresh() {
	ctx, cancel := context.WithTimeout(o.ctx, refreshTimeout)
	defer cancel()

	members, err := o.client.SMembers(ctx, o.Key).Result()
	if err != nil {
		if !o.failing {
			o.failing = true
			o.Parent.Log(logger.Error, "[dynamic origins] keeping the last list: %v", err)
		}
		return
	}

	o.failing = false
	o.current.Store(&members)
}
