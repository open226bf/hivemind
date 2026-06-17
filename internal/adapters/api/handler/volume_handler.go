package handler

import (
	"encoding/json"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/auditlog"
	"github.com/open226bf/hivemind/internal/domain/user"
	"github.com/open226bf/hivemind/internal/domain/volume"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/domainerrors"
)

type VolumeHandler struct {
	svc      *application.VolumeService
	registry ports.OrchestratorRegistry
	audit    ports.AuditLogRepository
}

func NewVolumeHandler(svc *application.VolumeService, registry ports.OrchestratorRegistry, audit ports.AuditLogRepository) *VolumeHandler {
	return &VolumeHandler{svc: svc, registry: registry, audit: audit}
}

// Register wires volume catalog and service-mount routes.
func (h *VolumeHandler) Register(protected *gin.RouterGroup) {
	// Catalog management is Admin-only, like networks (F-V1-01).
	v := protected.Group("/volumes")
	v.GET("", middleware.RequireRole(user.RoleViewer), h.List)
	v.POST("", middleware.RequireRole(user.RoleAdmin), h.Create)
	v.GET("/swarm", middleware.RequireRole(user.RoleViewer), h.DiscoverSwarm)
	v.GET("/:id", middleware.RequireRole(user.RoleViewer), h.Get)
	v.DELETE("/:id", middleware.RequireRole(user.RoleAdmin), h.Delete)

	// Mounts are part of service management (Operator), but bind mounts are
	// gated to Admin inside SetMounts (F-V2-06).
	m := protected.Group("/services/:id/mounts")
	m.GET("", middleware.RequireRole(user.RoleViewer), h.GetMounts)
	m.PUT("", middleware.RequireRole(user.RoleOperator), h.SetMounts)
}

// List godoc
//
//	@Summary		List named volumes
//	@Tags			volumes
//	@Security		BearerAuth
//	@Produce		json
//	@Param			page	query		int	false	"Page number (default 1)"
//	@Param			size	query		int	false	"Page size (default 20, max 100)"
//	@Success		200		{object}	dto.VolumeListResponse
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Router			/volumes [get]
func (h *VolumeHandler) List(c *gin.Context) {
	page := parsePage(c)
	items, total, err := h.svc.List(c.Request.Context(), queryCluster(c), page)
	if err != nil {
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "failed to list volumes")
		return
	}
	resp := dto.VolumeListResponse{
		Items: make([]dto.VolumeResponse, len(items)),
		Total: total,
		Page:  page.Number,
		Size:  page.Size,
	}
	for i, v := range items {
		resp.Items[i] = toVolumeResponse(v)
	}
	c.JSON(http.StatusOK, resp)
}

// Create godoc
//
//	@Summary		Create a named volume
//	@Description	Registers a named volume in the catalog. Local volumes are materialised per-node by Docker when a service mounting them is deployed.
//	@Tags			volumes
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		dto.CreateVolumeRequest	true	"Volume definition"
//	@Success		201		{object}	dto.VolumeResponse
//	@Failure		400		{object}	dto.ErrorResponse	"validation_error"
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Failure		409		{object}	dto.ErrorResponse	"name already taken"
//	@Failure		422		{object}	dto.ErrorResponse	"invalid name"
//	@Router			/volumes [post]
func (h *VolumeHandler) Create(c *gin.Context) {
	var req dto.CreateVolumeRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}
	clusterID, ok := parseOptionalCluster(c, req.Cluster)
	if !ok {
		return
	}
	v, err := h.svc.Create(c.Request.Context(), application.CreateVolumeInput{Name: req.Name, Driver: req.Driver, Cluster: clusterID})
	if err != nil {
		h.writeVolumeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toVolumeResponse(v))
}

// Get godoc
//
//	@Summary		Get a named volume
//	@Tags			volumes
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Volume ID (UUID)"
//	@Success		200	{object}	dto.VolumeResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/volumes/{id} [get]
func (h *VolumeHandler) Get(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	v, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		h.writeVolumeError(c, err)
		return
	}
	c.JSON(http.StatusOK, toVolumeResponse(v))
}

// Delete godoc
//
//	@Summary		Delete a named volume
//	@Description	Fails if the volume is still mounted by any service.
//	@Tags			volumes
//	@Security		BearerAuth
//	@Param			id	path	string	true	"Volume ID (UUID)"
//	@Success		204
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Failure		409	{object}	dto.ErrorResponse	"volume in use"
//	@Router			/volumes/{id} [delete]
func (h *VolumeHandler) Delete(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		h.writeVolumeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DiscoverSwarm godoc
//
//	@Summary		List named volumes on the Swarm cluster
//	@Tags			volumes
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{array}		dto.SwarmVolumeInfo
//	@Failure		503	{object}	dto.ErrorResponse	"orchestrator unavailable"
//	@Router			/volumes/swarm [get]
func (h *VolumeHandler) DiscoverSwarm(c *gin.Context) {
	orch, ok := resolveOrchestrator(c, h.registry)
	if !ok {
		return
	}
	vols, err := orch.ListVolumes(c.Request.Context())
	if err != nil {
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "failed to list swarm volumes")
		return
	}
	out := make([]dto.SwarmVolumeInfo, len(vols))
	for i, v := range vols {
		out[i] = dto.SwarmVolumeInfo{Name: v.Name, Driver: v.Driver, Mountpoint: v.Mountpoint, Scope: v.Scope}
	}
	c.JSON(http.StatusOK, out)
}

// GetMounts godoc
//
//	@Summary		List a service's mounts
//	@Tags			volumes
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Service ID (UUID)"
//	@Success		200	{object}	dto.MountsResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/services/{id}/mounts [get]
func (h *VolumeHandler) GetMounts(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	res, err := h.svc.GetServiceMounts(c.Request.Context(), serviceID)
	if err != nil {
		h.writeVolumeError(c, err)
		return
	}
	c.JSON(http.StatusOK, toMountsResponse(res))
}

// SetMounts godoc
//
//	@Summary		Replace a service's mounts
//	@Description	Atomically replaces the full mount set. Volume mounts must reference an existing catalog volume. Bind mounts are restricted to Admins and journaled (F-V2-06).
//	@Tags			volumes
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string					true	"Service ID (UUID)"
//	@Param			body	body		dto.SetMountsRequest	true	"Full mount set"
//	@Success		200		{object}	dto.MountsResponse
//	@Failure		400		{object}	dto.ErrorResponse	"validation_error"
//	@Failure		403		{object}	dto.ErrorResponse	"bind mounts require Admin"
//	@Failure		404		{object}	dto.ErrorResponse
//	@Failure		422		{object}	dto.ErrorResponse	"invalid mount or unknown volume"
//	@Router			/services/{id}/mounts [put]
func (h *VolumeHandler) SetMounts(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var req dto.SetMountsRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}

	mounts := fromMountDTOs(req.Mounts)

	// Bind mounts are an Admin-only privilege (host filesystem access).
	claims, _ := middleware.ClaimsFrom(c)
	if volume.HasBind(mounts) && (claims == nil || claims.Role != string(user.RoleAdmin)) {
		dto.Abort(c, http.StatusForbidden, dto.CodeForbidden, "bind mounts require the Admin role")
		return
	}

	res, err := h.svc.SetServiceMounts(c.Request.Context(), serviceID, mounts)
	if err != nil {
		h.writeVolumeError(c, err)
		return
	}

	// Journal bind-mount usage (security-sensitive).
	if volume.HasBind(mounts) {
		h.journalBindMounts(c, serviceID, mounts)
	}

	c.JSON(http.StatusOK, toMountsResponse(res))
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (h *VolumeHandler) journalBindMounts(c *gin.Context, serviceID uuid.UUID, mounts []volume.Mount) {
	if h.audit == nil {
		return
	}
	binds := make([]string, 0)
	for _, m := range mounts {
		if m.Type == volume.MountBind {
			binds = append(binds, m.Source+"→"+m.Target)
		}
	}
	payload, _ := json.Marshal(map[string]any{"binds": binds})

	var uid *uuid.UUID
	if claims, ok := middleware.ClaimsFrom(c); ok {
		u := claims.UserID
		uid = &u
	}
	entry := auditlog.New(uid, "service_bind_mounts_set", "service", serviceID.String(), payload, c.ClientIP())
	_ = h.audit.Save(c.Request.Context(), entry)
}

func (h *VolumeHandler) writeVolumeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domainerrors.ErrNotFound):
		dto.Abort(c, http.StatusNotFound, dto.CodeNotFound, "resource not found")
	case errors.Is(err, domainerrors.ErrConflict), errors.Is(err, volume.ErrVolumeInUse):
		dto.Abort(c, http.StatusConflict, dto.CodeConflict, err.Error())
	case errors.Is(err, volume.ErrInvalidName),
		errors.Is(err, volume.ErrInvalidMountType),
		errors.Is(err, volume.ErrInvalidMountTarget),
		errors.Is(err, volume.ErrMountSourceRequired),
		errors.Is(err, volume.ErrInvalidBindSource),
		errors.Is(err, volume.ErrTmpfsNoSource),
		errors.Is(err, volume.ErrDuplicateMountTarget),
		errors.Is(err, volume.ErrUnknownVolume):
		dto.Abort(c, http.StatusUnprocessableEntity, dto.CodeUnprocessable, err.Error())
	default:
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "internal error")
	}
}

func toVolumeResponse(v *volume.Volume) dto.VolumeResponse {
	return dto.VolumeResponse{
		ID:        v.ID.String(),
		ClusterID: clusterIDString(v.ClusterID),
		Name:      v.Name,
		Driver:    v.Driver,
		CreatedAt: v.CreatedAt,
	}
}

func toMountsResponse(res *application.MountsResult) dto.MountsResponse {
	out := dto.MountsResponse{
		Mounts:   make([]dto.MountDTO, len(res.Mounts)),
		Warnings: res.Warnings,
	}
	for i, m := range res.Mounts {
		out.Mounts[i] = dto.MountDTO{
			Type:     string(m.Type),
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		}
	}
	if out.Warnings == nil {
		out.Warnings = []string{}
	}
	return out
}

func fromMountDTOs(in []dto.MountDTO) []volume.Mount {
	out := make([]volume.Mount, 0, len(in))
	for _, m := range in {
		out = append(out, volume.Mount{
			Type:     volume.MountType(m.Type),
			Source:   m.Source,
			Target:   m.Target,
			ReadOnly: m.ReadOnly,
		})
	}
	return out
}
