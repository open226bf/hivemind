package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/orange/hivemind/internal/adapters/api/dto"
	"github.com/orange/hivemind/internal/adapters/api/middleware"
	"github.com/orange/hivemind/internal/application"
	"github.com/orange/hivemind/internal/domain/user"
)

type ClusterHandler struct {
	svc *application.ClusterService
}

func NewClusterHandler(svc *application.ClusterService) *ClusterHandler {
	return &ClusterHandler{svc: svc}
}

// Register wires the cluster dashboard route.
func (h *ClusterHandler) Register(protected *gin.RouterGroup) {
	g := protected.Group("/cluster")
	g.GET("/overview", middleware.RequireRole(user.RoleViewer), h.Overview)
}

// Overview godoc
//
//	@Summary		Cluster dashboard overview
//	@Description	Aggregated health and capacity of the orchestration cluster (nodes, CPU/memory) combined with catalog and deployment-activity counts. Cluster health is best-effort: when the orchestrator is unreachable the counts are still returned with cluster.reachable=false.
//	@Tags			cluster
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{object}	dto.ClusterOverviewResponse
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		500	{object}	dto.ErrorResponse
//	@Router			/cluster/overview [get]
func (h *ClusterHandler) Overview(c *gin.Context) {
	ov, err := h.svc.Overview(c.Request.Context())
	if err != nil {
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "failed to build cluster overview")
		return
	}

	resp := dto.ClusterOverviewResponse{
		Cluster: dto.ClusterSummaryDTO{
			Reachable:     ov.Cluster.Reachable,
			NodeTotal:     ov.Cluster.NodeTotal,
			Managers:      ov.Cluster.Managers,
			Workers:       ov.Cluster.Workers,
			ReadyNodes:    ov.Cluster.ReadyNodes,
			TotalCpus:     ov.Cluster.TotalCPUs,
			TotalMemory:   ov.Cluster.TotalMemory,
			LeaderHost:    ov.Cluster.LeaderHost,
			EngineVersion: ov.Cluster.EngineVersion,
		},
		Nodes: make([]dto.NodeDTO, len(ov.Nodes)),
		Services: dto.ServiceSummaryDTO{
			Total:    ov.Services.Total,
			Draft:    ov.Services.Draft,
			Deployed: ov.Services.Deployed,
			Removed:  ov.Services.Removed,
		},
		Activity: dto.ActivitySummaryDTO{
			TotalDeployments: ov.Activity.TotalDeployments,
			InProgress:       ov.Activity.InProgress,
			Succeeded:        ov.Activity.Succeeded,
			Failed:           ov.Activity.Failed,
		},
		Catalog: dto.CatalogSummaryDTO{
			Networks: ov.Catalog.Networks,
			Secrets:  ov.Catalog.Secrets,
			Configs:  ov.Catalog.Configs,
		},
	}
	for i, n := range ov.Nodes {
		resp.Nodes[i] = dto.NodeDTO{
			ID:            n.ID,
			Hostname:      n.Hostname,
			Role:          n.Role,
			Leader:        n.Leader,
			Availability:  n.Availability,
			State:         n.State,
			Addr:          n.Addr,
			EngineVersion: n.EngineVersion,
			Cpus:          n.CPUs,
			MemoryBytes:   n.MemoryBytes,
			Platform:      n.Platform,
		}
	}
	c.JSON(http.StatusOK, resp)
}
