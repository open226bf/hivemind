package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gorilla/websocket"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/domainerrors"
)

// ExecHandler upgrades an HTTP request to a WebSocket and bridges it to an
// interactive container exec session (web terminal, Admin only).
type ExecHandler struct {
	svc      *application.DeploymentService
	upgrader websocket.Upgrader
}

func NewExecHandler(svc *application.DeploymentService) *ExecHandler {
	return &ExecHandler{
		svc: svc,
		upgrader: websocket.Upgrader{
			// Same-origin in prod (served behind the same host); the dev proxy
			// forwards from the Angular dev server, so allow all origins here.
			CheckOrigin: func(*http.Request) bool { return true },
		},
	}
}

// resizeMsg is the control message the client sends to resize the PTY.
type resizeMsg struct {
	Cols uint `json:"cols"`
	Rows uint `json:"rows"`
}

// Exec godoc
//
//	@Summary		Interactive container shell (WebSocket, Admin)
//	@Description	Upgrades to a WebSocket and attaches an interactive TTY exec session to one of the service's containers. Client→server text messages are tagged by a leading byte: '0'=stdin data, '1'=JSON resize {cols,rows}. Server→client messages are raw terminal output. Authenticate with ?token=<access_token> (WebSocket cannot carry an Authorization header). Requires the Admin role.
//	@Tags			supervision
//	@Param			id			path	string	true	"Service ID (UUID)"
//	@Param			container	query	string	true	"Target container ID (from /services/{id}/tasks)"
//	@Param			token		query	string	true	"Access token"
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
	defer conn.Close()

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
