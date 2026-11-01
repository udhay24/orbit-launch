package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/bluenviron/mediamtx/internal/conf"
	"github.com/bluenviron/mediamtx/internal/logger"
)

type discardLogger struct{}

func (discardLogger) Log(logger.Level, string, ...any) {}

func TestJWKSKeepsLastKeysWhenServerFails(t *testing.T) {
	var failing atomic.Bool
	var requests atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		if failing.Load() {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"keys": []any{}}) //nolint:errcheck
	}))
	defer server.Close()

	m := &Manager{
		Method:      conf.AuthMethodJWT,
		JWTJWKS:     server.URL,
		ReadTimeout: 5 * time.Second,
		Parent:      discardLogger{},
	}
	m.Initialize()

	first, err := m.pullJWTJWKS()
	require.NoError(t, err)
	require.NotNil(t, first)
	require.Equal(t, int32(1), requests.Load())

	failing.Store(true)
	m.mutex.Lock()
	m.jwksLastRefresh = time.Time{}
	m.mutex.Unlock()

	// returns at once with the cached keys, while the refresh fails in the background.
	second, err := m.pullJWTJWKS()
	require.NoError(t, err)
	require.NotNil(t, second)

	require.Eventually(t, func() bool {
		m.mutex.RLock()
		defer m.mutex.RUnlock()
		return !m.jwksRefreshing && requests.Load() == 2
	}, 5*time.Second, 10*time.Millisecond)

	// failed refreshes are not retried on every request.
	third, err := m.pullJWTJWKS()
	require.NoError(t, err)
	require.NotNil(t, third)
	require.Equal(t, int32(2), requests.Load())
}
