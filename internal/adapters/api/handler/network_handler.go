package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/network"
	"github.com/open226bf/hivemind/internal/domain/user"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/domainerrors"
)

type NetworkHandler struct {
	svc      *application.NetworkService
	registry ports.OrchestratorRegistry
}

func NewNetworkHandler(svc *application.NetworkService, registry ports.OrchestratorRegistry) *NetworkHandler {
	return &NetworkHandler{svc: svc, registry: registry}
}

// Register wires network CRUD and service-attachment routes.
func (h *NetworkHandler) Register(protected *gin.RouterGroup) {
	// Network catalog management is Admin-only (F-V1-01); attaching an existing
	// network to a service stays with Operators as part of service management.
	n := protected.Group("/networks")
	n.GET("", middleware.RequireRole(user.RoleViewer), h.List)
	n.POST("", middleware.RequireRole(user.RoleAdmin), h.Create)
	n.GET("/swarm", middleware.RequireRole(user.RoleViewer), h.DiscoverSwarm)
	n.GET("/:id", middleware.RequireRole(user.RoleViewer), h.Get)
	n.DELETE("/:id", middleware.RequireRole(user.RoleAdmin), h.Delete)

	// Service ↔ network attachments (Operator: part of service management).
	s := protected.Group("/services/:id/networks")
	s.GET("", middleware.RequireRole(user.RoleViewer), h.ListForService)
	s.POST("", middleware.RequireRole(user.RoleOperator), h.AttachToService)
	s.DELETE("/:networkId", middleware.RequireRole(user.RoleOperator), h.DetachFromService)
}

// List godoc
//
//	@Summary		List networks
//	@Tags			networks
//	@Security		BearerAuth
//	@Produce		json
//	@Param			page	query		int	false	"Page number (default 1)"
//	@Param			size	query		int	false	"Page size (default 20, max 100)"
//	@Success		200		{object}	dto.NetworkListResponse
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Router			/networks [get]
func (h *NetworkHandler) List(c *gin.Context) {
	page := parsePage(c)
	items, total, err := h.svc.List(c.Request.Context(), currentCluster(c), page)
	if err != nil {
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "failed to list networks")
		return
	}

	resp := dto.NetworkListResponse{
		Items: make([]dto.NetworkResponse, len(items)),
		Total: total,
		Page:  page.Number,
		Size:  page.Size,
	}
	for i, n := range items {
		resp.Items[i] = toNetworkResponse(n)
	}
	c.JSON(http.StatusOK, resp)
}

// Create godoc
//
//	@Summary		Create a network
//	@Description	Registers an overlay network definition. It is materialised on Swarm when a service using it is deployed.
//	@Tags			networks
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		dto.CreateNetworkRequest	true	"Network definition"
//	@Success		201		{object}	dto.NetworkResponse
//	@Failure		400		{object}	dto.ErrorResponse	"validation_error"
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Failure		409		{object}	dto.ErrorResponse	"name already taken"
//	@Failure		422		{object}	dto.ErrorResponse	"invalid name"
//	@Router			/networks [post]
func (h *NetworkHandler) Create(c *gin.Context) {
	var req dto.CreateNetworkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}

	clusterID := currentCluster(c) // active cluster from X-Hivemind-Cluster
	n, err := h.svc.Create(c.Request.Context(), application.CreateNetworkInput{
		Name:       req.Name,
		Subnet:     req.Subnet,
		Attachable: req.Attachable,
		External:   req.External,
		Cluster:    clusterID,
	})
	if err != nil {
		h.writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toNetworkResponse(n))
}

// Get godoc
//
//	@Summary		Get a network
//	@Tags			networks
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Network ID (UUID)"
//	@Success		200	{object}	dto.NetworkResponse
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/networks/{id} [get]
func (h *NetworkHandler) Get(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	n, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		h.writeNetworkError(c, err)
		return
	}
	c.JSON(http.StatusOK, toNetworkResponse(n))
}

// Delete godoc
//
//	@Summary		Delete a network
//	@Description	Fails if the network is still attached to any service.
//	@Tags			networks
//	@Security		BearerAuth
//	@Param			id	path	string	true	"Network ID (UUID)"
//	@Success		204
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Failure		409	{object}	dto.ErrorResponse	"network in use"
//	@Router			/networks/{id} [delete]
func (h *NetworkHandler) Delete(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		h.writeNetworkError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListForService godoc
//
//	@Summary		List networks attached to a service
//	@Tags			networks
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Service ID (UUID)"
//	@Success		200	{array}		dto.NetworkResponse
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/services/{id}/networks [get]
func (h *NetworkHandler) ListForService(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	nets, err := h.svc.ListServiceNetworks(c.Request.Context(), serviceID)
	if err != nil {
		h.writeNetworkError(c, err)
		return
	}
	out := make([]dto.NetworkResponse, len(nets))
	for i, n := range nets {
		out[i] = toNetworkResponse(n)
	}
	c.JSON(http.StatusOK, out)
}

// AttachToService godoc
//
//	@Summary		Attach a network to a service
//	@Tags			networks
//	@Security		BearerAuth
//	@Accept			json
//	@Param			id		path	string						true	"Service ID (UUID)"
//	@Param			body	body	dto.AttachNetworkRequest	true	"Network to attach"
//	@Success		204
//	@Failure		400	{object}	dto.ErrorResponse	"validation_error"
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse	"service or network not found"
//	@Failure		409	{object}	dto.ErrorResponse	"already attached"
//	@Router			/services/{id}/networks [post]
func (h *NetworkHandler) AttachToService(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var req dto.AttachNetworkRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}
	networkID, ok := parseUUIDValue(c, req.NetworkID, "network_id")
	if !ok {
		return
	}

	if err := h.svc.AttachToService(c.Request.Context(), serviceID, networkID); err != nil {
		h.writeNetworkError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DetachFromService godoc
//
//	@Summary		Detach a network from a service
//	@Tags			networks
//	@Security		BearerAuth
//	@Param			id			path	string	true	"Service ID (UUID)"
//	@Param			networkId	path	string	true	"Network ID (UUID)"
//	@Success		204
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse	"attachment not found"
//	@Router			/services/{id}/networks/{networkId} [delete]
func (h *NetworkHandler) DetachFromService(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	networkID, ok := parseUUID(c, "networkId")
	if !ok {
		return
	}
	if err := h.svc.DetachFromService(c.Request.Context(), serviceID, networkID); err != nil {
		h.writeNetworkError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DiscoverSwarm godoc
//
//	@Summary		List overlay networks on the Swarm cluster
//	@Description	Returns lightweight info about every overlay network visible on the Docker Swarm cluster. Useful for discovering existing networks before creating or attaching.
//	@Tags			networks
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{array}		dto.SwarmNetworkInfo
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		503	{object}	dto.ErrorResponse	"orchestrator unavailable"
//	@Router			/networks/swarm [get]
func (h *NetworkHandler) DiscoverSwarm(c *gin.Context) {
	orch, ok := resolveOrchestrator(c, h.registry)
	if !ok {
		return
	}
	nets, err := orch.ListNetworks(c.Request.Context())
	if err != nil {
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "failed to list swarm networks")
		return
	}
	out := make([]dto.SwarmNetworkInfo, len(nets))
	for i, n := range nets {
		out[i] = dto.SwarmNetworkInfo{
			ID:     n.ID,
			Name:   n.Name,
			Scope:  n.Scope,
			Driver: n.Driver,
			Subnet: n.Subnet,
		}
	}
	c.JSON(http.StatusOK, out)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (h *NetworkHandler) writeNetworkError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domainerrors.ErrNotFound):
		dto.Abort(c, http.StatusNotFound, dto.CodeNotFound, "resource not found")
	case errors.Is(err, domainerrors.ErrConflict), errors.Is(err, network.ErrNetworkInUse), errors.Is(err, application.ErrClusterMismatch):
		dto.Abort(c, http.StatusConflict, dto.CodeConflict, err.Error())
	case errors.Is(err, network.ErrInvalidName):
		dto.Abort(c, http.StatusUnprocessableEntity, dto.CodeUnprocessable, err.Error())
	default:
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "internal error")
	}
}

func toNetworkResponse(n *network.Network) dto.NetworkResponse {
	return dto.NetworkResponse{
		ID:         n.ID.String(),
		ClusterID:  clusterIDString(n.ClusterID),
		Name:       n.Name,
		Driver:     n.Driver,
		Scope:      n.Scope,
		Subnet:     n.Subnet,
		Attachable: n.Attachable,
		External:   n.External,
		SwarmID:    n.SwarmID,
		CreatedAt:  n.CreatedAt,
	}
}
