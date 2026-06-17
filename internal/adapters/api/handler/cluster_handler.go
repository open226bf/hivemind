package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/orange/hivemind/internal/adapters/api/dto"
	"github.com/orange/hivemind/internal/adapters/api/middleware"
	"github.com/orange/hivemind/internal/application"
	"github.com/orange/hivemind/internal/domain/cluster"
	"github.com/orange/hivemind/internal/domain/user"
	"github.com/orange/hivemind/pkg/domainerrors"
)

type ClusterHandler struct {
	svc *application.ClusterService
}

func NewClusterHandler(svc *application.ClusterService) *ClusterHandler {
	return &ClusterHandler{svc: svc}
}

// Register wires the cluster dashboard and management routes. The aggregated
// overview is viewer-readable; cluster CRUD is Admin-only (F-V1-01).
func (h *ClusterHandler) Register(protected *gin.RouterGroup) {
	g := protected.Group("/cluster")
	g.GET("/overview", middleware.RequireRole(user.RoleViewer), h.Overview)

	cs := protected.Group("/clusters")
	cs.GET("", middleware.RequireRole(user.RoleViewer), h.List)
	cs.GET("/:id", middleware.RequireRole(user.RoleViewer), h.Get)
	cs.GET("/:id/overview", middleware.RequireRole(user.RoleViewer), h.OverviewForCluster)
	cs.POST("", middleware.RequireRole(user.RoleAdmin), h.Create)
	cs.PATCH("/:id", middleware.RequireRole(user.RoleAdmin), h.Update)
	cs.DELETE("/:id", middleware.RequireRole(user.RoleAdmin), h.Delete)
	cs.PUT("/:id/default", middleware.RequireRole(user.RoleAdmin), h.SetDefault)
	cs.POST("/:id/test", middleware.RequireRole(user.RoleAdmin), h.Test)
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
	c.JSON(http.StatusOK, toOverviewResponse(ov))
}

// OverviewForCluster godoc
//
//	@Summary		Cluster dashboard overview for a specific cluster
//	@Description	Same payload as /cluster/overview but with node health scoped to the given cluster.
//	@Tags			cluster
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Cluster ID"
//	@Success		200	{object}	dto.ClusterOverviewResponse
//	@Failure		400	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/clusters/{id}/overview [get]
func (h *ClusterHandler) OverviewForCluster(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	ov, err := h.svc.OverviewForCluster(c.Request.Context(), id)
	if err != nil {
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "failed to build cluster overview")
		return
	}
	c.JSON(http.StatusOK, toOverviewResponse(ov))
}

func toOverviewResponse(ov *application.Overview) dto.ClusterOverviewResponse {
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
			ID:             n.ID,
			Hostname:       n.Hostname,
			Role:           n.Role,
			Leader:         n.Leader,
			Availability:   n.Availability,
			State:          n.State,
			Addr:           n.Addr,
			EngineVersion:  n.EngineVersion,
			Cpus:           n.CPUs,
			MemoryBytes:    n.MemoryBytes,
			Platform:       n.Platform,
			AgentConnected: n.AgentConnected,
		}
	}
	return resp
}

// ─── Cluster management ───────────────────────────────────────────────────────

// List godoc
//
//	@Summary	List clusters
//	@Tags		cluster
//	@Security	BearerAuth
//	@Produce	json
//	@Success	200	{object}	dto.ClusterListResponse
//	@Router		/clusters [get]
func (h *ClusterHandler) List(c *gin.Context) {
	page := parsePage(c)
	items, total, err := h.svc.ListClusters(c.Request.Context(), page)
	if err != nil {
		h.writeClusterError(c, err)
		return
	}
	out := make([]dto.ClusterResponse, len(items))
	for i, cl := range items {
		out[i] = toClusterResponse(cl)
	}
	c.JSON(http.StatusOK, dto.ClusterListResponse{Items: out, Total: total, Page: page.Number, Size: page.Size})
}

// Get godoc
//
//	@Summary	Get a cluster
//	@Tags		cluster
//	@Security	BearerAuth
//	@Produce	json
//	@Param		id	path		string	true	"Cluster ID"
//	@Success	200	{object}	dto.ClusterResponse
//	@Failure	404	{object}	dto.ErrorResponse
//	@Router		/clusters/{id} [get]
func (h *ClusterHandler) Get(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	cl, err := h.svc.GetCluster(c.Request.Context(), id)
	if err != nil {
		h.writeClusterError(c, err)
		return
	}
	c.JSON(http.StatusOK, toClusterResponse(cl))
}

// Create godoc
//
//	@Summary	Register a cluster
//	@Tags		cluster
//	@Security	BearerAuth
//	@Accept		json
//	@Produce	json
//	@Param		request	body		dto.CreateClusterRequest	true	"Cluster"
//	@Success	201		{object}	dto.ClusterResponse
//	@Failure	409		{object}	dto.ErrorResponse
//	@Failure	422		{object}	dto.ErrorResponse
//	@Router		/clusters [post]
func (h *ClusterHandler) Create(c *gin.Context) {
	var req dto.CreateClusterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}
	cl, err := h.svc.CreateCluster(c.Request.Context(), application.CreateClusterInput{
		Name:     req.Name,
		Type:     req.Type,
		Endpoint: req.Endpoint,
		Labels:   req.Labels,
		TLS:      application.ClusterTLSInput{CACert: req.CACert, ClientCert: req.ClientCert, ClientKey: req.ClientKey},
	})
	if err != nil {
		h.writeClusterError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toClusterResponse(cl))
}

// Update godoc
//
//	@Summary	Patch a cluster
//	@Tags		cluster
//	@Security	BearerAuth
//	@Accept		json
//	@Produce	json
//	@Param		id		path		string						true	"Cluster ID"
//	@Param		request	body		dto.UpdateClusterRequest	true	"Patch"
//	@Success	200		{object}	dto.ClusterResponse
//	@Failure	404		{object}	dto.ErrorResponse
//	@Router		/clusters/{id} [patch]
func (h *ClusterHandler) Update(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var req dto.UpdateClusterRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}
	in := application.UpdateClusterInput{Name: req.Name, Endpoint: req.Endpoint, Labels: req.Labels}
	if req.CACert != nil || req.ClientCert != nil || req.ClientKey != nil {
		in.TLS = &application.ClusterTLSInput{
			CACert:     derefString(req.CACert),
			ClientCert: derefString(req.ClientCert),
			ClientKey:  derefString(req.ClientKey),
		}
	}
	cl, err := h.svc.UpdateCluster(c.Request.Context(), id, in)
	if err != nil {
		h.writeClusterError(c, err)
		return
	}
	c.JSON(http.StatusOK, toClusterResponse(cl))
}

// Delete godoc
//
//	@Summary	Remove a cluster
//	@Tags		cluster
//	@Security	BearerAuth
//	@Param		id	path	string	true	"Cluster ID"
//	@Success	204	"No Content"
//	@Failure	409	{object}	dto.ErrorResponse	"default or non-empty cluster"
//	@Router		/clusters/{id} [delete]
func (h *ClusterHandler) Delete(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.DeleteCluster(c.Request.Context(), id); err != nil {
		h.writeClusterError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// SetDefault godoc
//
//	@Summary	Promote a cluster to default
//	@Tags		cluster
//	@Security	BearerAuth
//	@Produce	json
//	@Param		id	path		string	true	"Cluster ID"
//	@Success	200	{object}	dto.ClusterResponse
//	@Router		/clusters/{id}/default [put]
func (h *ClusterHandler) SetDefault(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	cl, err := h.svc.SetDefaultCluster(c.Request.Context(), id)
	if err != nil {
		h.writeClusterError(c, err)
		return
	}
	c.JSON(http.StatusOK, toClusterResponse(cl))
}

// Test godoc
//
//	@Summary	Probe cluster connectivity
//	@Tags		cluster
//	@Security	BearerAuth
//	@Produce	json
//	@Param		id	path		string	true	"Cluster ID"
//	@Success	200	{object}	dto.ClusterResponse
//	@Failure	503	{object}	dto.ErrorResponse	"unreachable"
//	@Router		/clusters/{id}/test [post]
func (h *ClusterHandler) Test(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	cl, err := h.svc.TestCluster(c.Request.Context(), id)
	if errors.Is(err, application.ErrOrchestratorUnavailable) {
		// Report the recorded status with a 503 — the cluster row still updated.
		c.JSON(http.StatusServiceUnavailable, toClusterResponse(cl))
		return
	}
	if err != nil {
		h.writeClusterError(c, err)
		return
	}
	c.JSON(http.StatusOK, toClusterResponse(cl))
}

func (h *ClusterHandler) writeClusterError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domainerrors.ErrNotFound):
		dto.Abort(c, http.StatusNotFound, dto.CodeNotFound, "cluster not found")
	case errors.Is(err, domainerrors.ErrConflict),
		errors.Is(err, cluster.ErrDefaultCluster),
		errors.Is(err, cluster.ErrClusterNotEmpty):
		dto.Abort(c, http.StatusConflict, dto.CodeConflict, err.Error())
	case errors.Is(err, cluster.ErrInvalidName), errors.Is(err, cluster.ErrInvalidType):
		dto.Abort(c, http.StatusUnprocessableEntity, dto.CodeUnprocessable, err.Error())
	default:
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "internal error")
	}
}

func toClusterResponse(cl *cluster.Cluster) dto.ClusterResponse {
	return dto.ClusterResponse{
		ID:             cl.ID.String(),
		Name:           cl.Name,
		Type:           string(cl.Type),
		ConnectionMode: string(cl.ConnectionMode),
		Endpoint:       cl.Endpoint,
		IsDefault:      cl.IsDefault,
		Status:         string(cl.Status),
		Labels:         cl.Labels,
		TLSEnabled:     cl.TLS.Enabled(),
		AgentStatus:    string(cl.AgentStatus),
		AgentLastSeen:  cl.AgentLastSeen,
		CreatedAt:      cl.CreatedAt,
		UpdatedAt:      cl.UpdatedAt,
	}
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
