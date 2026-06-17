package handler

import (
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/open226bf/hivemind/internal/adapters/agenthub"
	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/cluster"
	"github.com/open226bf/hivemind/internal/domain/user"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/domainerrors"
)

// tunnelProto is the Upgrade token the agent and server agree on for the
// reverse tunnel.
const tunnelProto = "hivemind-tunnel"

// AgentHandler exposes the agent handshake: admin-only enrollment (protected),
// the agent-facing register/heartbeat endpoints and the reverse-tunnel connect
// endpoint (public — the agent authenticates with its enrollment token or agent
// id, not a JWT).
type AgentHandler struct {
	svc *application.AgentService
	hub *agenthub.Hub
}

func NewAgentHandler(svc *application.AgentService, hub *agenthub.Hub) *AgentHandler {
	return &AgentHandler{svc: svc, hub: hub}
}

// Register wires the agent routes.
func (h *AgentHandler) Register(public, protected *gin.RouterGroup) {
	a := public.Group("/agent")
	a.POST("/register", h.AgentRegister)
	a.POST("/heartbeat", h.AgentHeartbeat)
	a.GET("/connect", h.Connect)

	protected.POST("/clusters/:id/enroll", middleware.RequireRole(user.RoleAdmin), h.Enroll)
}

// Connect upgrades the request to the raw reverse tunnel: the agent dials out,
// the server hijacks the connection and hands it to the hub, which multiplexes
// Docker API calls over it. Authentication is the bound agent id (the tunnel is
// only useful for an already-enrolled agent).
func (h *AgentHandler) Connect(c *gin.Context) {
	agentID := c.Query("agent_id")
	if h.hub == nil || !h.svc.Bound(c.Request.Context(), agentID) {
		dto.Abort(c, http.StatusUnauthorized, dto.CodeUnauthorized, "unknown agent")
		return
	}
	if c.GetHeader("Upgrade") != tunnelProto {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "expected tunnel upgrade")
		return
	}

	hj, ok := c.Writer.(http.Hijacker)
	if !ok {
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "connection hijack unsupported")
		return
	}
	conn, _, err := hj.Hijack()
	if err != nil {
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "connection hijack failed")
		return
	}
	if _, err := io.WriteString(conn,
		"HTTP/1.1 101 Switching Protocols\r\nUpgrade: "+tunnelProto+"\r\nConnection: Upgrade\r\n\r\n"); err != nil {
		_ = conn.Close()
		return
	}

	node := toAgentNode(dto.AgentNodeDTO{
		NodeID:   c.Query("node_id"),
		Role:     c.Query("role"),
		Hostname: c.Query("hostname"),
	})
	slog.Info("agent tunnel attached", "agent_id", agentID, "node", node.NodeID)
	if err := h.hub.Attach(agentID, node.NodeID, node, conn); err != nil {
		slog.Warn("agent tunnel ended", "agent_id", agentID, "err", err)
	} else {
		slog.Info("agent tunnel closed", "agent_id", agentID)
	}
	_ = conn.Close()
}

// Enroll godoc
//
//	@Summary		Issue an agent enrollment token
//	@Description	Switches the cluster to agent connection mode (if needed) and returns a one-time enrollment token plus a ready-to-run deploy command. The token is shown only once.
//	@Tags			clusters
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Cluster ID"
//	@Success		200	{object}	dto.EnrollClusterResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/clusters/{id}/enroll [post]
func (h *AgentHandler) Enroll(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	enr, err := h.svc.Enroll(c.Request.Context(), id)
	if err != nil {
		writeAgentError(c, err)
		return
	}
	c.JSON(http.StatusOK, dto.EnrollClusterResponse{
		ClusterID:   enr.ClusterID.String(),
		ClusterName: enr.ClusterName,
		Token:       enr.Token,
		Command:     deployCommand(c, enr.Token),
		HubAddr:     enr.HubAddr,
		ClientCert:  enr.ClientCertPEM,
		ClientKey:   enr.ClientKeyPEM,
		CACert:      enr.CACertPEM,
	})
}

// AgentRegister godoc
//
//	@Summary	Agent enrollment / reconnection
//	@Tags		agent
//	@Accept		json
//	@Produce	json
//	@Param		body	body		dto.AgentRegisterRequest	true	"Agent registration"
//	@Success	200		{object}	dto.AgentRegisterResponse
//	@Failure	401		{object}	dto.ErrorResponse
//	@Router		/agent/register [post]
func (h *AgentHandler) AgentRegister(c *gin.Context) {
	var req dto.AgentRegisterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}
	reg, err := h.svc.Register(c.Request.Context(), application.RegisterInput{
		EnrollToken: req.EnrollToken,
		AgentID:     req.AgentID,
		Node:        toAgentNode(req.Node),
	})
	if err != nil {
		writeAgentError(c, err)
		return
	}
	c.JSON(http.StatusOK, dto.AgentRegisterResponse{
		AgentID:     reg.AgentID,
		ClusterID:   reg.ClusterID.String(),
		ClusterName: reg.ClusterName,
	})
}

// AgentHeartbeat godoc
//
//	@Summary	Agent liveness heartbeat
//	@Tags		agent
//	@Accept		json
//	@Param		body	body	dto.AgentHeartbeatRequest	true	"Heartbeat"
//	@Success	204		"no content"
//	@Failure	404		{object}	dto.ErrorResponse
//	@Router		/agent/heartbeat [post]
func (h *AgentHandler) AgentHeartbeat(c *gin.Context) {
	var req dto.AgentHeartbeatRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}
	if err := h.svc.Heartbeat(c.Request.Context(), req.AgentID, toAgentNode(req.Node)); err != nil {
		writeAgentError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ─── helpers ──────────────────────────────────────────────────────────────────

func toAgentNode(n dto.AgentNodeDTO) ports.AgentNode {
	return ports.AgentNode{
		NodeID:        n.NodeID,
		Hostname:      n.Hostname,
		Role:          n.Role,
		IsManager:     n.IsManager,
		IsLeader:      n.IsLeader,
		EngineVersion: n.EngineVersion,
	}
}

// deployCommand renders the one-liner to deploy the agent stack on the target
// cluster. The server URL comes from AGENT_PUBLIC_URL, falling back to the
// request's scheme+host.
func deployCommand(c *gin.Context, token string) string {
	server := os.Getenv("AGENT_PUBLIC_URL")
	if server == "" {
		scheme := "https"
		if c.Request.TLS == nil {
			scheme = "http"
		}
		server = scheme + "://" + c.Request.Host
	}
	return fmt.Sprintf(
		"HIVEMIND_SERVER=%s HIVEMIND_ENROLL_TOKEN=%s docker stack deploy -c hivemind-agent.yml hivemind-agent",
		server, token,
	)
}

func writeAgentError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, application.ErrInvalidEnrollment):
		dto.Abort(c, http.StatusUnauthorized, dto.CodeUnauthorized, "invalid or expired enrollment token")
	case errors.Is(err, application.ErrAgentNotRegistered):
		dto.Abort(c, http.StatusNotFound, dto.CodeNotFound, "agent is not registered")
	case errors.Is(err, domainerrors.ErrNotFound):
		dto.Abort(c, http.StatusNotFound, dto.CodeNotFound, "cluster not found")
	case errors.Is(err, cluster.ErrNotAgentMode), errors.Is(err, cluster.ErrNoEnrollment):
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, err.Error())
	default:
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "internal error")
	}
}
