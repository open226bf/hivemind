package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/orange/hivemind/internal/adapters/api/dto"
	"github.com/orange/hivemind/internal/adapters/api/middleware"
	"github.com/orange/hivemind/internal/application"
	"github.com/orange/hivemind/internal/domain/deployment"
	"github.com/orange/hivemind/internal/domain/snapshot"
	"github.com/orange/hivemind/internal/domain/user"
	"github.com/orange/hivemind/pkg/domainerrors"
)

type SnapshotHandler struct {
	svc *application.SnapshotService
}

func NewSnapshotHandler(svc *application.SnapshotService) *SnapshotHandler {
	return &SnapshotHandler{svc: svc}
}

// Register wires snapshot routes onto the protected router group.
func (h *SnapshotHandler) Register(protected *gin.RouterGroup) {
	protected.POST("/services/:id/snapshots", middleware.RequireRole(user.RoleOperator), h.Create)
	protected.GET("/services/:id/snapshots", middleware.RequireRole(user.RoleViewer), h.ListForService)
	protected.GET("/snapshots/:id", middleware.RequireRole(user.RoleViewer), h.Get)
	protected.DELETE("/snapshots/:id", middleware.RequireRole(user.RoleOperator), h.Delete)
	protected.POST("/snapshots/:id/rollback", middleware.RequireRole(user.RoleOperator), h.Rollback)
}

// Create godoc
//
//	@Summary		Capture a service snapshot
//	@Description	Captures a complete, point-in-time snapshot of the service and every element it uses (spec, env, networks, secrets, configs, mounts), resolved to their current values. Reusable for a manual rollback.
//	@Tags			snapshots
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string						true	"Service ID (UUID)"
//	@Param			body	body		dto.CreateSnapshotRequest	false	"Optional label"
//	@Success		201		{object}	dto.SnapshotResponse
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Failure		404		{object}	dto.ErrorResponse
//	@Router			/services/{id}/snapshots [post]
func (h *SnapshotHandler) Create(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var req dto.CreateSnapshotRequest
	if c.Request.ContentLength > 0 {
		if err := c.ShouldBindJSON(&req); err != nil {
			dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
			return
		}
	}

	var userID *uuid.UUID
	if claims, ok := middleware.ClaimsFrom(c); ok {
		uid := claims.UserID
		userID = &uid
	}

	snap, err := h.svc.Capture(c.Request.Context(), serviceID, req.Label, userID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toSnapshotResponse(snap, false))
}

// ListForService godoc
//
//	@Summary		List a service's snapshots
//	@Tags			snapshots
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id		path		string	true	"Service ID (UUID)"
//	@Param			page	query		int		false	"Page number (default 1)"
//	@Param			size	query		int		false	"Page size (default 20, max 100)"
//	@Success		200		{object}	dto.SnapshotListResponse
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Failure		404		{object}	dto.ErrorResponse
//	@Router			/services/{id}/snapshots [get]
func (h *SnapshotHandler) ListForService(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	page := parsePage(c)

	items, total, err := h.svc.ListForService(c.Request.Context(), serviceID, page)
	if err != nil {
		h.writeError(c, err)
		return
	}
	resp := dto.SnapshotListResponse{
		Items: make([]dto.SnapshotResponse, len(items)),
		Total: total,
		Page:  page.Number,
		Size:  page.Size,
	}
	for i, s := range items {
		resp.Items[i] = toSnapshotResponse(s, false)
	}
	c.JSON(http.StatusOK, resp)
}

// Get godoc
//
//	@Summary		Get a snapshot (full detail, values masked)
//	@Tags			snapshots
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Snapshot ID (UUID)"
//	@Success		200	{object}	dto.SnapshotResponse
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/snapshots/{id} [get]
func (h *SnapshotHandler) Get(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	snap, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusOK, toSnapshotResponse(snap, true))
}

// Delete godoc
//
//	@Summary		Delete a snapshot
//	@Tags			snapshots
//	@Security		BearerAuth
//	@Param			id	path	string	true	"Snapshot ID (UUID)"
//	@Success		204
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/snapshots/{id} [delete]
func (h *SnapshotHandler) Delete(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		h.writeError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Rollback godoc
//
//	@Summary		Roll back a service to a snapshot
//	@Description	Restores the service definition from the snapshot (spec, env, mounts, attachments) and triggers a new deployment (trigger=rollback). Returns the deployment plus any non-fatal warnings (e.g. a secret was recreated or has drifted since capture).
//	@Tags			snapshots
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Snapshot ID (UUID)"
//	@Success		202	{object}	dto.RollbackResponse
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Failure		409	{object}	dto.ErrorResponse	"a deployment is already in progress"
//	@Failure		503	{object}	dto.ErrorResponse	"deployment engine not configured"
//	@Router			/snapshots/{id}/rollback [post]
func (h *SnapshotHandler) Rollback(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var userID *uuid.UUID
	if claims, ok := middleware.ClaimsFrom(c); ok {
		uid := claims.UserID
		userID = &uid
	}

	res, err := h.svc.Rollback(c.Request.Context(), id, userID)
	if err != nil {
		h.writeError(c, err)
		return
	}
	c.JSON(http.StatusAccepted, dto.RollbackResponse{
		Deployment: toDeploymentResponse(res.Deployment),
		Warnings:   res.Warnings,
	})
}

// ─── Error mapping ────────────────────────────────────────────────────────────

func (h *SnapshotHandler) writeError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domainerrors.ErrNotFound):
		dto.Abort(c, http.StatusNotFound, dto.CodeNotFound, "not found")
	case errors.Is(err, deployment.ErrAlreadyInProgress):
		dto.Abort(c, http.StatusConflict, dto.CodeConflict, err.Error())
	case errors.Is(err, application.ErrOrchestratorUnavailable):
		dto.Abort(c, http.StatusServiceUnavailable, dto.CodeInternal, err.Error())
	case errors.Is(err, snapshot.ErrSchemaUnknown), errors.Is(err, snapshot.ErrEmptyPayload):
		dto.Abort(c, http.StatusUnprocessableEntity, dto.CodeUnprocessable, err.Error())
	default:
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "internal error")
	}
}

// ─── DTO converters ───────────────────────────────────────────────────────────

func toSnapshotResponse(s *snapshot.ServiceSnapshot, detail bool) dto.SnapshotResponse {
	p := s.Payload
	fullImage := p.Image
	if p.Tag != "" {
		fullImage = p.Image + ":" + p.Tag
	}
	resp := dto.SnapshotResponse{
		ID:            s.ID.String(),
		ServiceID:     s.ServiceID.String(),
		Label:         s.Label,
		SchemaVersion: s.SchemaVersion,
		CreatedAt:     s.CreatedAt,
		Summary: dto.SnapshotSummary{
			FullImage:    fullImage,
			Replicas:     p.Replicas,
			EnvCount:     len(p.EnvVars),
			NetworkCount: len(p.Networks),
			SecretCount:  len(p.Secrets),
			ConfigCount:  len(p.Configs),
			MountCount:   len(p.Mounts),
		},
	}
	if s.CreatedBy != nil {
		resp.CreatedBy = s.CreatedBy.String()
	}
	if !detail {
		return resp
	}

	d := &dto.SnapshotDetail{
		Name:        p.Name,
		Description: p.Description,
		Image:       p.Image,
		Tag:         p.Tag,
		Replicas:    p.Replicas,
		Command:     nullSafeStrings(p.Command),
		Entrypoint:  nullSafeStrings(p.Entrypoint),
		HiveID:      p.HiveID,
		EnvVars:     make([]dto.SnapshotEnvVar, 0, len(p.EnvVars)),
		Networks:    make([]dto.SnapshotNetwork, 0, len(p.Networks)),
		Secrets:     make([]dto.SnapshotSecretRef, 0, len(p.Secrets)),
		Configs:     make([]dto.SnapshotConfigRef, 0, len(p.Configs)),
		Mounts:      make([]dto.SnapshotMount, 0, len(p.Mounts)),
	}
	for _, e := range p.EnvVars {
		value := e.Value
		if e.IsSecret {
			value = "" // masked — never echo secret values
		}
		d.EnvVars = append(d.EnvVars, dto.SnapshotEnvVar{Key: e.Key, Value: value, IsSecret: e.IsSecret})
	}
	for _, n := range p.Networks {
		d.Networks = append(d.Networks, dto.SnapshotNetwork{Name: n.Name, Subnet: n.Subnet})
	}
	for _, sec := range p.Secrets {
		d.Secrets = append(d.Secrets, dto.SnapshotSecretRef{Name: sec.Name, Version: sec.Version, TargetPath: sec.TargetPath, Checksum: sec.Checksum})
	}
	for _, cfg := range p.Configs {
		d.Configs = append(d.Configs, dto.SnapshotConfigRef{Name: cfg.Name, Version: cfg.Version, TargetPath: cfg.TargetPath, Checksum: cfg.Checksum})
	}
	for _, m := range p.Mounts {
		d.Mounts = append(d.Mounts, dto.SnapshotMount{Type: m.Type, Source: m.Source, Target: m.Target, ReadOnly: m.ReadOnly})
	}
	resp.Detail = d
	return resp
}
