package web_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/orange/hivemind/internal/adapters/api/web"
)

func engine(t *testing.T) *gin.Engine {
	t.Helper()
	gin.SetMode(gin.TestMode)
	r := gin.New()
	// A representative API route so the /api namespace is owned by handlers.
	r.GET("/api/v1/ping", func(c *gin.Context) { c.String(http.StatusOK, "pong") })
	require.NoError(t, web.Register(r))
	return r
}

func get(r *gin.Engine, path string) *httptest.ResponseRecorder {
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, path, nil))
	return w
}

func TestServesIndexAtRoot(t *testing.T) {
	w := get(engine(t), "/")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Header().Get("Content-Type"), "text/html")
	assert.Equal(t, "no-cache", w.Header().Get("Cache-Control"))
}

func TestSPAFallbackForDeepLink(t *testing.T) {
	// A client-side route is not a real file → serve the shell so the SPA boots.
	w := get(engine(t), "/clusters/123")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "Hivemind")
}

func TestApiPathStays404NotSPA(t *testing.T) {
	// Unmatched /api routes must keep an API-style 404, not return the SPA shell.
	w := get(engine(t), "/api/v1/does-not-exist")
	assert.Equal(t, http.StatusNotFound, w.Code)
	assert.NotContains(t, w.Body.String(), "<html")
}

func TestApiRouteStillWins(t *testing.T) {
	w := get(engine(t), "/api/v1/ping")
	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, "pong", w.Body.String())
}
