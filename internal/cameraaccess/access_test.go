package cameraaccess

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/test"
)

func newAccess(t *testing.T, addr string, password string) *Access {
	a := &Access{
		RedisAddress:  addr,
		RedisPassword: password,
		BlockKey:      "camera:blocked_stream_ids",
		OwnerKey:      "camera:stream_ids",
		PathPrefix:    "live/",
		Parent:        test.NilLogger,
	}
	a.Initialize()
	t.Cleanup(a.Close)
	return a
}

func TestCheck(t *testing.T) {
	s := miniredis.RunT(t)
	_, err := s.SAdd("camera:blocked_stream_ids", "BAD1", "NOPREFIX")
	require.NoError(t, err)

	a := newAccess(t, s.Addr(), "")

	for _, ca := range []struct {
		name    string
		path    string
		wantErr string
	}{
		{"blocked", "live/BAD1", "stream 'BAD1' is blocked"},
		{"not blocked", "live/CAM1", ""},
		{"unregistered camera", "live/ANYTHING", ""},
		{"blocked without prefix", "NOPREFIX", "stream 'NOPREFIX' is blocked"},
		{"other prefix", "other/BAD1", ""},
	} {
		t.Run(ca.name, func(t *testing.T) {
			err := a.Check(ca.path)
			if ca.wantErr == "" {
				require.NoError(t, err)
			} else {
				require.EqualError(t, err, ca.wantErr)
			}
		})
	}
}

func TestCheckFailurePolicy(t *testing.T) {
	t.Run("missing set", func(t *testing.T) {
		s := miniredis.RunT(t)
		require.NoError(t, newAccess(t, s.Addr(), "").Check("live/CAM1"))
	})

	t.Run("redis down", func(t *testing.T) {
		s := miniredis.RunT(t)
		addr := s.Addr()
		s.Close()
		require.NoError(t, newAccess(t, addr, "").Check("live/CAM1"))
	})

	t.Run("wrong password", func(t *testing.T) {
		s := miniredis.RunT(t)
		s.RequireAuth("secret")
		_, err := s.SAdd("camera:blocked_stream_ids", "BAD1")
		require.NoError(t, err)
		require.NoError(t, newAccess(t, s.Addr(), "wrong").Check("live/BAD1"))
	})

	t.Run("password", func(t *testing.T) {
		s := miniredis.RunT(t)
		s.RequireAuth("secret")
		_, err := s.SAdd("camera:blocked_stream_ids", "BAD1")
		require.NoError(t, err)
		require.Error(t, newAccess(t, s.Addr(), "secret").Check("live/BAD1"))
	})
}

func TestCheckViewToken(t *testing.T) {
	s := miniredis.RunT(t)
	_, err := s.SAdd("camera:stream_ids:owned:one", "CAM1")
	require.NoError(t, err)
	_, err = s.SAdd("camera:stream_ids:owned:two", "CAM2")
	require.NoError(t, err)

	a := newAccess(t, s.Addr(), "")
	a.ExemptKeys = []string{"orchestrator"}

	require.NoError(t, a.CheckViewToken("one", "live/CAM1"))
	require.NoError(t, a.CheckViewToken("orchestrator", "live/CAM2"))
	require.EqualError(t, a.CheckViewToken("one", "live/CAM2"), "key 'one' does not own stream 'CAM2'")
	require.EqualError(t, a.CheckViewToken("unknown", "live/CAM1"), "key 'unknown' does not own stream 'CAM1'")
	require.Error(t, a.CheckViewToken("one", "other/CAM1"))

	// a Redis outage: pairs seen before keep working, nothing new does.
	s.Close()
	require.NoError(t, a.CheckViewToken("one", "live/CAM1"))
	require.ErrorContains(t, a.CheckViewToken("one", "live/CAM2"), "unavailable")
	require.ErrorContains(t, a.CheckViewToken("two", "live/CAM1"), "unavailable")
}
