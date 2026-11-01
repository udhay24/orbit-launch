package streamregistry

import (
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
