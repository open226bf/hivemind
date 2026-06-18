package handler_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"

	"github.com/orange/hivemind/internal/adapters/api/handler"
	"github.com/orange/hivemind/internal/adapters/auth"
)

// The SSE status stream is authenticated solely by a single-use ticket bound to
// the service (EventSource can't send a bearer header), so it must reject any
// request without a valid, matching ticket before streaming anything.
func TestStreamStatus_RequiresValidServiceTicket(t *testing.T) {
	gin.SetMode(gin.TestMode)
	store := auth.NewTicketStore(time.Minute)
	h := handler.NewStreamHandler(nil, store) // svc is never reached on the reject paths
	r := gin.New()
	r.GET("/services/:id/status/stream", h.StreamStatus)

	sid := uuid.New()
	get := func(query string) int {
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/services/"+sid.String()+"/status/stream"+query, nil))
		return w.Code
	}

	assert.Equal(t, http.StatusUnauthorized, get(""), "no ticket")
	assert.Equal(t, http.StatusUnauthorized, get("?ticket=does-not-exist"), "unknown ticket")

	// A ticket minted for a different service must not stream this one.
	other, _ := store.Issue(auth.WSTicket{ServiceID: uuid.New()})
	assert.Equal(t, http.StatusUnauthorized, get("?ticket="+other), "ticket bound to another service")

	// A ticket is single-use: even a valid one can't be replayed.
	valid, _ := store.Issue(auth.WSTicket{ServiceID: sid})
	store.Consume(valid) // simulate the stream having consumed it once
	assert.Equal(t, http.StatusUnauthorized, get("?ticket="+valid), "already-used ticket")
}
