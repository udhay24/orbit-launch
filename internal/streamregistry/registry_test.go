package streamregistry

import (
	"net"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/test"
)

func newTestRegistry(t *testing.T) (*Registry, *miniredis.Miniredis) {
	mr := miniredis.RunT(t)

	r := &Registry{
		RedisAddress: mr.Addr(),
		ServerID:     "test-server",
		APIAddress:   "127.0.0.1:9997",
		TTL:          10 * time.Second,
		Parent:       test.NilLogger,
	}
	err := r.Initialize()
	require.NoError(t, err)
	t.Cleanup(r.Close)

	return r, mr
}

func TestRegistryRegisterAndLookup(t *testing.T) {
	r, _ := newTestRegistry(t)

	// Register is asynchronous; wait for the key to appear.
	r.Register("cam1")
	require.Eventually(t, func() bool {
		info, err := r.Lookup("cam1")
		return err == nil && info != nil
	}, time.Second, 10*time.Millisecond)

	info, err := r.Lookup("cam1")
	require.NoError(t, err)
	require.NotNil(t, info)
	require.Equal(t, "test-server", info.Server)
	require.Equal(t, "127.0.0.1:9997", info.API)
}

func TestRegistryLookupMissing(t *testing.T) {
	r, _ := newTestRegistry(t)

	info, err := r.Lookup("does-not-exist")
	require.NoError(t, err)
	require.Nil(t, info)
}

func TestRegistryKeyCarriesTTL(t *testing.T) {
	r, mr := newTestRegistry(t)

	r.Register("cam1")
	require.Eventually(t, func() bool {
		info, _ := r.Lookup("cam1")
		return info != nil
	}, time.Second, 10*time.Millisecond)

	require.Greater(t, mr.TTL(redisKey("cam1")), time.Duration(0))
}

func TestRegistryDeregister(t *testing.T) {
	r, _ := newTestRegistry(t)

	r.Register("cam1")
	require.Eventually(t, func() bool {
		info, _ := r.Lookup("cam1")
		return info != nil
	}, time.Second, 10*time.Millisecond)

	r.Deregister("cam1")
	require.Eventually(t, func() bool {
		info, _ := r.Lookup("cam1")
		return info == nil
	}, time.Second, 10*time.Millisecond)
}

func TestRegistryRegisterAll(t *testing.T) {
	r, _ := newTestRegistry(t)

	names := []string{"cam1", "cam2", "cam3"}
	r.RegisterAll(names) // synchronous

	for _, name := range names {
		info, err := r.Lookup(name)
		require.NoError(t, err)
		require.NotNil(t, info, "expected %s to be registered", name)
		require.Equal(t, "test-server", info.Server)
	}
}

func TestRegistryKeyExpires(t *testing.T) {
	r, mr := newTestRegistry(t)

	r.Register("cam1")
	require.Eventually(t, func() bool {
		info, _ := r.Lookup("cam1")
		return info != nil
	}, time.Second, 10*time.Millisecond)

	// Advance miniredis past the TTL (well before the TTL/2 real-time refresh
	// tick could fire) and confirm the key is gone — proving registrations are
	// not permanent.
	mr.FastForward(11 * time.Second)

	info, err := r.Lookup("cam1")
	require.NoError(t, err)
	require.Nil(t, info)
}

func freeAddr(t *testing.T) string {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

func TestRegistryStartsWithoutRedis(t *testing.T) {
	r := &Registry{
		RedisAddress: freeAddr(t),
		ServerID:     "test-server",
		TTL:          time.Second,
		Parent:       test.NilLogger,
	}
	require.NoError(t, r.Initialize())
	t.Cleanup(r.Close)

	r.Register("cam1")
	_, err := r.Lookup("cam1")
	require.Error(t, err)
}

func TestRegistryRegistersOnceRedisIsUp(t *testing.T) {
	addr := freeAddr(t)
	r := &Registry{
		RedisAddress: addr,
		ServerID:     "test-server",
		TTL:          400 * time.Millisecond,
		Parent:       test.NilLogger,
	}
	require.NoError(t, r.Initialize())
	t.Cleanup(r.Close)

	r.Register("cam1")

	mr := miniredis.NewMiniRedis()
	require.NoError(t, mr.StartAddr(addr))
	t.Cleanup(mr.Close)

	require.Eventually(t, func() bool {
		info, _ := r.Lookup("cam1")
		return info != nil && info.Server == "test-server"
	}, 3*time.Second, 20*time.Millisecond)
}

func TestRegistryRestoresLostKeys(t *testing.T) {
	mr := miniredis.RunT(t)
	r := &Registry{
		RedisAddress: mr.Addr(),
		ServerID:     "test-server",
		TTL:          400 * time.Millisecond,
		Parent:       test.NilLogger,
	}
	require.NoError(t, r.Initialize())
	t.Cleanup(r.Close)

	r.Register("cam1")
	require.Eventually(t, func() bool { return mr.Exists(redisKey("cam1")) }, time.Second, 10*time.Millisecond)

	mr.FlushAll()

	require.Eventually(t, func() bool { return mr.Exists(redisKey("cam1")) }, 2*time.Second, 20*time.Millisecond)
	require.Greater(t, mr.TTL(redisKey("cam1")), time.Duration(0))
}
