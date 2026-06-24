package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/service"
	"github.com/open226bf/hivemind/internal/domain/user"
)

// DiscoveryHandler exposes brownfield service discovery (ADR 0004): the live
// Swarm services on the active cluster, classified as managed / foreign / orphan.
type DiscoveryHandler struct {
	svc *application.DiscoveryService
}

func NewDiscoveryHandler(svc *application.DiscoveryService) *DiscoveryHandler {
	return &DiscoveryHandler{svc: svc}
}

// Register wires discovery routes. Listing is Viewer-level (like other
// Swarm-discovery endpoints, e.g. GET /networks/swarm); adopt/release mutate
// state and require Operator.
func (h *DiscoveryHandler) Register(protected *gin.RouterGroup) {
	g := protected.Group("/discovered-services")
	g.GET("", middleware.RequireRole(user.RoleViewer), h.List)
	g.POST("/:swarmId/adopt", middleware.RequireRole(user.RoleOperator), h.Adopt)
	g.POST("/:swarmId/release", middleware.RequireRole(user.RoleOperator), h.Release)
}

// List godoc
//
//	@Summary		List services running on the cluster (brownfield discovery)
//	@Description	Returns every Swarm service on the active cluster, each classified as managed (owned by Hivemind), foreign (created out-of-band), or orphan (labelled but unknown). Read-only; nothing is mutated.
//	@Tags			discovery
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{array}		dto.DiscoveredService
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		503	{object}	dto.ErrorResponse	"orchestrator unavailable"
//	@Router			/discovered-services [get]
func (h *DiscoveryHandler) List(c *gin.Context) {
	items, err := h.svc.Discover(c.Request.Context(), currentCluster(c))
	if err != nil {
		writeError(c, err, "cluster not found")
		return
	}
	out := make([]dto.DiscoveredService, len(items))
	for i, d := range items {
		out[i] = dto.DiscoveredService{
			SwarmServiceID: d.SwarmServiceID,
			Name:           d.Name,
			Image:          d.Image,
			Replicas:       d.Replicas,
			Class:          d.Class,
			CreatedAt:      d.CreatedAt,
		}
		if d.ServiceID != nil {
			out[i].ServiceID = d.ServiceID.String()
		}
		if d.HiveID != nil {
			out[i].HiveID = d.HiveID.String()
		}
	}
	c.JSON(http.StatusOK, out)
}

// Adopt godoc
//
//	@Summary		Adopt a foreign service
//	@Description	Takes over a service running on the cluster: reconstructs its spec, creates a deployed Hivemind service (optionally in a hive), seals the ownership label, and snapshots it. The live service keeps running. Returns warnings for anything not fully reconstructed.
//	@Tags			discovery
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			swarmId	path		string						true	"Swarm service ID"
//	@Param			body	body		dto.AdoptServiceRequest		false	"Target hive"
//	@Success		201		{object}	dto.AdoptServiceResponse
//	@Failure		400		{object}	dto.ErrorResponse
//	@Failure		409		{object}	dto.ErrorResponse	"already managed"
//	@Failure		422		{object}	dto.ErrorResponse	"spec cannot be adopted (e.g. invalid name)"
//	@Failure		503		{object}	dto.ErrorResponse	"orchestrator unavailable"
//	@Router			/discovered-services/{swarmId}/adopt [post]
func (h *DiscoveryHandler) Adopt(c *gin.Context) {
	swarmID := c.Param("swarmId")
	var req dto.AdoptServiceRequest
	if err := c.ShouldBindJSON(&req); err != nil && err.Error() != "EOF" {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}

	var hiveID *uuid.UUID
	if req.HiveID != "" {
		id, ok := parseUUIDValue(c, req.HiveID, "hive_id")
		if !ok {
			return
		}
		hiveID = &id
	}

	in := application.AdoptInput{
		ClusterID:      writeCluster(c),
		SwarmServiceID: swarmID,
		HiveID:         hiveID,
	}
	if claims, ok := middleware.ClaimsFrom(c); ok {
		uid := claims.UserID
		in.UserID = &uid
	}

	res, err := h.svc.Adopt(c.Request.Context(), in)
	if err != nil {
		h.writeAdoptError(c, err)
		return
	}
	c.JSON(http.StatusCreated, dto.AdoptServiceResponse{
		ServiceID: res.ServiceID.String(),
		Warnings:  res.Warnings,
	})
}

// Release godoc
//
//	@Summary		Release an adopted service
//	@Description	Hands an adopted service back to unmanaged: clears the Hivemind ownership label and deletes the Hivemind record. The live service keeps running untouched.
//	@Tags			discovery
//	@Security		BearerAuth
//	@Param			swarmId	path	string	true	"Swarm service ID"
//	@Success		204
//	@Failure		404	{object}	dto.ErrorResponse	"no managed service owns this Swarm service"
//	@Failure		503	{object}	dto.ErrorResponse	"orchestrator unavailable"
//	@Router			/discovered-services/{swarmId}/release [post]
func (h *DiscoveryHandler) Release(c *gin.Context) {
	if err := h.svc.Release(c.Request.Context(), writeCluster(c), c.Param("swarmId")); err != nil {
		if errors.Is(err, application.ErrServiceNotAdopted) {
			dto.Abort(c, http.StatusNotFound, dto.CodeNotFound, err.Error())
			return
		}
		writeError(c, err, "service not found")
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *DiscoveryHandler) writeAdoptError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, application.ErrAlreadyManaged):
		dto.Abort(c, http.StatusConflict, dto.CodeConflict, err.Error())
	case errors.Is(err, service.ErrInvalidName), errors.Is(err, service.ErrInvalidImage):
		dto.Abort(c, http.StatusUnprocessableEntity, dto.CodeUnprocessable, err.Error())
	default:
		writeError(c, err, "service not found")
	}
}
