package handler

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/orange/hivemind/internal/adapters/api/dto"
	"github.com/orange/hivemind/internal/adapters/api/middleware"
	"github.com/orange/hivemind/internal/adapters/auth"
	"github.com/orange/hivemind/internal/application"
)

const (
	streamTickEvery = 2 * time.Second
	streamHeartbeat = 15 * time.Second
)

// StreamHandler streams a service's live state (replica counts + tasks) over
// Server-Sent Events so the UI updates reactively without per-second polling.
//
// SSE is chosen over WebSocket on purpose: it is plain HTTP/1.1, so it traverses
// reverse proxies (HAProxy, nginx) without an Upgrade handshake or per-tunnel
// timeout tuning, and the browser's EventSource reconnects automatically. A
// periodic heartbeat comment keeps the connection alive through proxy idle
// timeouts, and the client falls back to polling if a proxy blocks the stream.
type StreamHandler struct {
	svc     *application.DeploymentService
	tickets *auth.TicketStore
}

func NewStreamHandler(svc *application.DeploymentService, tickets *auth.TicketStore) *StreamHandler {
	return &StreamHandler{svc: svc, tickets: tickets}
}

// streamPayload is one SSE frame: the same shape the supervision view assembles
// from GET /status + GET /tasks, so the client reuses its existing types.
type streamPayload struct {
	Status dto.ServiceStatusResponse `json:"status"`
	Tasks  []dto.TaskStateResponse   `json:"tasks"`
}

// IssueTicket godoc
//
//	@Summary		Mint a single-use status-stream ticket (Viewer)
//	@Description	EventSource cannot send an Authorization header, so the client exchanges its bearer token (this request is header-authenticated) for a short-lived, single-use ticket and opens GET /services/{id}/status/stream?ticket=<id> with it — keeping the access token out of the URL.
//	@Tags			supervision
//	@Security		BearerAuth
//	@Param			id	path	string	true	"Service ID (UUID)"
//	@Success		200	{object}	map[string]any	"ticket and expires_in (seconds)"
//	@Failure		401	{object}	dto.ErrorResponse
//	@Router			/services/{id}/status/stream-ticket [post]
func (h *StreamHandler) IssueTicket(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	claims, ok := middleware.ClaimsFrom(c)
	if !ok {
		dto.Abort(c, http.StatusUnauthorized, dto.CodeUnauthorized, "authentication required")
		return
	}
	id, ttl := h.tickets.Issue(auth.WSTicket{UserID: claims.UserID, ServiceID: serviceID})
	c.JSON(http.StatusOK, gin.H{"ticket": id, "expires_in": ttl})
}

// StreamStatus godoc
//
//	@Summary		Stream a service's live state (SSE)
//	@Description	Server-Sent Events stream of the service's live state ({status, tasks}). Emits a `status` event whenever the state changes, plus heartbeat comments. Authenticate with ?ticket=<id> from POST /services/{id}/status/stream-ticket.
//	@Tags			supervision
//	@Param			id		path	string	true	"Service ID (UUID)"
//	@Param			ticket	query	string	true	"Single-use stream ticket"
//	@Success		200	{string}	string	"text/event-stream"
//	@Failure		401	{object}	dto.ErrorResponse
//	@Router			/services/{id}/status/stream [get]
func (h *StreamHandler) StreamStatus(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	t, ok := h.tickets.Consume(c.Query("ticket"))
	if !ok || t.ServiceID != serviceID {
		dto.Abort(c, http.StatusUnauthorized, dto.CodeUnauthorized, "invalid or expired stream ticket")
		return
	}

	w := c.Writer
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no") // disable nginx response buffering (HAProxy ignores it)
	w.WriteHeader(http.StatusOK)

	// SSE is long-lived; clear the server's WriteTimeout for this connection so
	// the stream is not torn down after a few seconds.
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	_ = rc.Flush()

	ctx := c.Request.Context()
	tick := time.NewTicker(streamTickEvery)
	defer tick.Stop()
	heartbeat := time.NewTicker(streamHeartbeat)
	defer heartbeat.Stop()

	var last string
	send := func() {
		state, err := h.svc.ServiceState(ctx, serviceID)
		if err != nil {
			return // transient (or not deployed yet) — keep the stream open
		}
		b, err := json.Marshal(streamPayload{
			Status: toServiceStatusResponse(state),
			Tasks:  toServiceTasksResponse(state).Tasks,
		})
		if err != nil || string(b) == last {
			return // unchanged: nothing to push
		}
		last = string(b)
		_, _ = fmt.Fprintf(w, "event: status\ndata: %s\n\n", b)
		_ = rc.Flush()
	}

	send() // initial state immediately
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			send()
		case <-heartbeat.C:
			_, _ = fmt.Fprint(w, ": ping\n\n")
			_ = rc.Flush()
		}
	}
}
