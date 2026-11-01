package publishallowlist

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/test"
)

func newAllowlist(t *testing.T, addr string, password string, failOpen bool) *Allowlist {
	a := &Allowlist{
		RedisAddress:  addr,
		RedisPassword: password,
		Key:           "camera:stream_ids",
		PathPrefix:    "live/",
		FailOpen:      failOpen,
		Parent:        test.NilLogger,
	}
	a.Initialize()
	t.Cleanup(a.Close)
	return a
}

func TestCheck(t *testing.T) {
	s := miniredis.RunT(t)
	_, err := s.SAdd("camera:stream_ids", "CAM1")
	require.NoError(t, err)

	a := newAllowlist(t, s.Addr(), "", false)

	for _, ca := range []struct {
		name    string
		path    string
		wantErr string
	}{
		{"registered", "live/CAM1", ""},
		{"unregistered", "live/CAM2", "stream 'CAM2' is not registered"},
		{"wrong prefix", "other/CAM1", "path 'other/CAM1' is not a registered stream path"},
		{"no prefix", "CAM1", "path 'CAM1' is not a registered stream path"},
		{"empty id", "live/", "path 'live/' is not a registered stream path"},
		{"nested", "live/CAM1/x", "path 'live/CAM1/x' is not a registered stream path"},
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

		require.NoError(t, newAllowlist(t, s.Addr(), "", true).Check("live/CAM1"))
		require.ErrorContains(t, newAllowlist(t, s.Addr(), "", false).Check("live/CAM1"), "does not exist")
	})

	t.Run("redis down", func(t *testing.T) {
		s := miniredis.RunT(t)
		addr := s.Addr()
		s.Close()

		require.NoError(t, newAllowlist(t, addr, "", true).Check("live/CAM1"))
		require.ErrorContains(t, newAllowlist(t, addr, "", false).Check("live/CAM1"), "unavailable")
	})

	t.Run("wrong password", func(t *testing.T) {
		s := miniredis.RunT(t)
		s.RequireAuth("secret")

		require.ErrorContains(t, newAllowlist(t, s.Addr(), "wrong", false).Check("live/CAM1"), "unavailable")
	})

	t.Run("password", func(t *testing.T) {
		s := miniredis.RunT(t)
		s.RequireAuth("secret")
		_, err := s.SAdd("camera:stream_ids", "CAM1")
		require.NoError(t, err)

		require.NoError(t, newAllowlist(t, s.Addr(), "secret", false).Check("live/CAM1"))
	})
}
