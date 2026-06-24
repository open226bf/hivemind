package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/application"
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

// Register wires the read-only discovery route. Listing is Viewer-level, in line
// with the other Swarm-discovery endpoints (e.g. GET /networks/swarm).
func (h *DiscoveryHandler) Register(protected *gin.RouterGroup) {
	protected.GET("/discovered-services", middleware.RequireRole(user.RoleViewer), h.List)
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
