package httpp

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestHandlerOriginDynamic(t *testing.T) {
	origins := []string{"https://first.example.com"}
	h := &handlerOrigin{
		h:                   http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
		allowOrigins:        []string{},
		dynamicAllowOrigins: func() []string { return origins },
	}

	allowed := func(origin string) string {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Origin", origin)
		h.ServeHTTP(w, r)
		return w.Header().Get("Access-Control-Allow-Origin")
	}

	require.Equal(t, "https://first.example.com", allowed("https://first.example.com"))
	require.Empty(t, allowed("https://second-domain.net"))

	// a newly registered application works without a restart
	origins = append(origins, "https://second-domain.net")
	require.Equal(t, "https://second-domain.net", allowed("https://second-domain.net"))
}
