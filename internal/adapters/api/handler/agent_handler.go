package handler

import (
	"errors"
	"fmt"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"

	"github.com/orange/hivemind/internal/adapters/api/dto"
	"github.com/orange/hivemind/internal/adapters/api/middleware"
	"github.com/orange/hivemind/internal/application"
	"github.com/orange/hivemind/internal/domain/cluster"
	"github.com/orange/hivemind/internal/domain/user"
	"github.com/orange/hivemind/internal/ports"
	"github.com/orange/hivemind/pkg/domainerrors"
)

// AgentHandler exposes the agent handshake: admin-only enrollment (protected)
// and the agent-facing register/heartbeat endpoints (public — the agent
// authenticates with its enrollment token or agent id, not a JWT).
type AgentHandler struct {
	svc *application.AgentService
}

func NewAgentHandler(svc *application.AgentService) *AgentHandler {
	return &AgentHandler{svc: svc}
}

// Register wires the agent routes.
func (h *AgentHandler) Register(public, protected *gin.RouterGroup) {
	a := public.Group("/agent")
	a.POST("/register", h.AgentRegister)
	a.POST("/heartbeat", h.AgentHeartbeat)

	protected.POST("/clusters/:id/enroll", middleware.RequireRole(user.RoleAdmin), h.Enroll)
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
