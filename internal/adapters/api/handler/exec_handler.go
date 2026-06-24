package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/adapters/auth"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/domainerrors"
)

// ExecHandler upgrades an HTTP request to a WebSocket and bridges it to an
// interactive container exec session (web terminal, Admin only).
type ExecHandler struct {
	svc      *application.DeploymentService
	tickets  *auth.TicketStore
	upgrader websocket.Upgrader
}

func NewExecHandler(svc *application.DeploymentService, tickets *auth.TicketStore) *ExecHandler {
	return &ExecHandler{
		svc:     svc,
		tickets: tickets,
		upgrader: websocket.Upgrader{
			// Same-origin in prod (served behind the same host); the dev proxy
			// forwards from the Angular dev server, so allow all origins here.
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
}

// IssueTicket godoc
//
//	@Summary		Mint a single-use exec ticket (Admin)
//	@Description	Exchanges the bearer token for a short-lived, single-use ticket bound to the caller and this service. The web terminal then opens the exec WebSocket with ?ticket=<id> instead of putting the access token in the URL (browsers cannot set headers on a WebSocket, and URLs leak into logs).
//	@Tags			supervision
//	@Security		BearerAuth
//	@Param			id	path	string	true	"Service ID (UUID)"
//	@Success		200	{object}	map[string]any	"ticket and expires_in (seconds)"
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Router			/services/{id}/exec/ticket [post]
func (h *ExecHandler) IssueTicket(c *gin.Context) {
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

// resizeMsg is the control message the client sends to resize the PTY.
type resizeMsg struct {
	Cols uint `json:"cols"`
	Rows uint `json:"rows"`
}

// Exec godoc
//
//	@Summary		Interactive container shell (WebSocket, Admin)
//	@Description	Upgrades to a WebSocket and attaches an interactive TTY exec session to one of the service's containers. Client→server text messages are tagged by a leading byte: '0'=stdin data, '1'=JSON resize {cols,rows}. Server→client messages are raw terminal output. Authenticate with ?ticket=<id> from POST /services/{id}/exec/ticket (WebSocket cannot carry an Authorization header; the ticket avoids putting the access token in the URL). Requires the Admin role.
//	@Tags			supervision
//	@Param			id			path	string	true	"Service ID (UUID)"
//	@Param			container	query	string	true	"Target container ID (from /services/{id}/tasks)"
//	@Param			ticket		query	string	true	"Single-use exec ticket"
//	@Param			cmd			query	string	false	"Shell command (default /bin/sh)"
//	@Success		101	{string}	string	"Switching Protocols"
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Failure		409	{object}	dto.ErrorResponse	"service not deployed"
//	@Router			/services/{id}/exec [get]
func (h *ExecHandler) Exec(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	// Authenticate the upgrade with a single-use ticket bound to this service.
	// Only the Admin-gated IssueTicket endpoint mints them, so a valid ticket
	// proves an authorised caller without a token (or role middleware) in the URL.
	ticket, ok := h.tickets.Consume(c.Query("ticket"))
	if !ok {
		dto.Abort(c, http.StatusUnauthorized, dto.CodeUnauthorized, "invalid or expired exec ticket")
		return
	}
	if ticket.ServiceID != serviceID {
		dto.Abort(c, http.StatusForbidden, dto.CodeForbidden, "ticket does not match this service")
		return
	}
	containerID := c.Query("container")
	if containerID == "" {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "missing container query parameter")
		return
	}

	// Resolve and validate the exec session BEFORE upgrading, so failures
	// surface as normal JSON errors rather than a closed socket.
	opts := ports.ExecOptions{Tty: true}
	if cmd := c.Query("cmd"); cmd != "" {
		opts.Cmd = []string{cmd}
	}
	stream, err := h.svc.ExecContainer(c.Request.Context(), serviceID, containerID, opts)
	if err != nil {
		writeExecError(c, err)
		return
	}
	defer stream.Close()

	conn, err := h.upgrader.Upgrade(c.Writer, c.Request, nil)
	if err != nil {
		return // upgrader already wrote the error
	}
	defer func() { _ = conn.Close() }()

	ctx, cancel := context.WithCancel(c.Request.Context())
	defer cancel()

	// client → container
	go func() {
		defer cancel()
		for {
			_, data, err := conn.ReadMessage()
			if err != nil {
				return
			}
			if len(data) == 0 {
				continue
			}
			switch data[0] {
			case '1': // resize control
				var r resizeMsg
				if json.Unmarshal(data[1:], &r) == nil && r.Cols > 0 && r.Rows > 0 {
					_ = stream.Resize(ctx, r.Rows, r.Cols)
				}
			default: // '0' or untagged: stdin
				if _, err := stream.Write(data[1:]); err != nil {
					return
				}
			}
		}
	}()

	// container → client
	buf := make([]byte, 4096)
	for {
		n, readErr := stream.Read(buf)
		if n > 0 {
			if err := conn.WriteMessage(websocket.BinaryMessage, buf[:n]); err != nil {
				return
			}
		}
		if readErr != nil {
			// Surface the shell exit / EOF to the client, then close.
			_ = conn.WriteMessage(websocket.TextMessage, []byte("\r\n[session terminée]\r\n"))
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
		}
	}
}

func writeExecError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domainerrors.ErrNotFound),
		errors.Is(err, application.ErrContainerNotInService):
		dto.Abort(c, http.StatusNotFound, dto.CodeNotFound, "container not found for this service")
	case errors.Is(err, application.ErrServiceNotDeployed):
		dto.Abort(c, http.StatusConflict, dto.CodeConflict, err.Error())
	case errors.Is(err, application.ErrOrchestratorUnavailable):
		dto.Abort(c, http.StatusServiceUnavailable, dto.CodeInternal, err.Error())
	default:
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "exec failed")
	}
}
