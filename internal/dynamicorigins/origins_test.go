package dynamicorigins

import (
	"testing"

	"github.com/alicebob/miniredis/v2"
	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/test"
)

func newOrigins(t *testing.T, addr string, password string) *Origins {
	o := &Origins{RedisAddress: addr, RedisPassword: password, Key: "origins", Parent: test.NilLogger}
	o.Initialize()
	t.Cleanup(o.Close)
	return o
}

func TestLoadAndRefresh(t *testing.T) {
	s := miniredis.RunT(t)
	_, err := s.SAdd("origins", "https://a.example.com")
	require.NoError(t, err)

	o := newOrigins(t, s.Addr(), "")
	require.Equal(t, []string{"https://a.example.com"}, o.Get())

	_, err = s.SAdd("origins", "https://other-domain.net")
	require.NoError(t, err)
	o.refresh()
	require.ElementsMatch(t, []string{"https://a.example.com", "https://other-domain.net"}, o.Get())
}

func TestKeepsLastListWhenRedisFails(t *testing.T) {
	s := miniredis.RunT(t)
	_, err := s.SAdd("origins", "https://a.example.com")
	require.NoError(t, err)

	o := newOrigins(t, s.Addr(), "")
	s.Close()
	o.refresh()

	require.Equal(t, []string{"https://a.example.com"}, o.Get())
}

func TestPassword(t *testing.T) {
	s := miniredis.RunT(t)
	s.RequireAuth("secret")
	_, err := s.SAdd("origins", "https://a.example.com")
	require.NoError(t, err)

	require.Empty(t, newOrigins(t, s.Addr(), "wrong").Get())
	require.Len(t, newOrigins(t, s.Addr(), "secret").Get(), 1)
}

func TestKeepsLastListWhenSetDisappears(t *testing.T) {
	s := miniredis.RunT(t)
	_, err := s.SAdd("origins", "https://a.example.com")
	require.NoError(t, err)

	o := newOrigins(t, s.Addr(), "")
	s.FlushAll()
	o.refresh()

	require.Equal(t, []string{"https://a.example.com"}, o.Get())
}
