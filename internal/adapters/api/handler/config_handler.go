package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/config"
	"github.com/open226bf/hivemind/internal/domain/user"
	"github.com/open226bf/hivemind/pkg/domainerrors"
)

type ConfigHandler struct {
	svc *application.ConfigService
}

func NewConfigHandler(svc *application.ConfigService) *ConfigHandler {
	return &ConfigHandler{svc: svc}
}

// Register wires config CRUD, versioning and service-attachment routes.
func (h *ConfigHandler) Register(protected *gin.RouterGroup) {
	g := protected.Group("/configs")
	g.GET("", middleware.RequireRole(user.RoleViewer), h.List)
	g.POST("", middleware.RequireRole(user.RoleOperator), h.Create)
	g.GET("/:id", middleware.RequireRole(user.RoleViewer), h.Get)
	g.GET("/:id/versions", middleware.RequireRole(user.RoleViewer), h.ListVersions)
	g.POST("/:id/versions", middleware.RequireRole(user.RoleOperator), h.AddVersion)
	g.DELETE("/:id", middleware.RequireRole(user.RoleOperator), h.Delete)

	s := protected.Group("/services/:id/configs")
	s.GET("", middleware.RequireRole(user.RoleViewer), h.ListForService)
	s.POST("", middleware.RequireRole(user.RoleOperator), h.AttachToService)
	s.DELETE("/:configId", middleware.RequireRole(user.RoleOperator), h.DetachFromService)
}

// List godoc
//
//	@Summary		List configs
//	@Tags			configs
//	@Security		BearerAuth
//	@Produce		json
//	@Param			page	query		int	false	"Page number (default 1)"
//	@Param			size	query		int	false	"Page size (default 20, max 100)"
//	@Success		200		{object}	dto.ConfigListResponse
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Router			/configs [get]
func (h *ConfigHandler) List(c *gin.Context) {
	page := parsePage(c)
	items, total, err := h.svc.List(c.Request.Context(), page)
	if err != nil {
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "failed to list configs")
		return
	}

	resp := dto.ConfigListResponse{
		Items: make([]dto.ConfigResponse, len(items)),
		Total: total,
		Page:  page.Number,
		Size:  page.Size,
	}
	for i, cfg := range items {
		resp.Items[i] = toConfigResponse(cfg)
	}
	c.JSON(http.StatusOK, resp)
}

// Create godoc
//
//	@Summary		Create a config
//	@Description	Stores a cleartext config file (UTF-8, max 500 KB) as version 1.
//	@Tags			configs
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		dto.CreateConfigRequest	true	"Config definition"
//	@Success		201		{object}	dto.ConfigResponse
//	@Failure		400		{object}	dto.ErrorResponse	"validation_error"
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Failure		409		{object}	dto.ErrorResponse	"name already taken"
//	@Failure		422		{object}	dto.ErrorResponse	"invalid name, content too large or invalid UTF-8"
//	@Router			/configs [post]
func (h *ConfigHandler) Create(c *gin.Context) {
	var req dto.CreateConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}

	claims, ok := middleware.ClaimsFrom(c)
	if !ok {
		dto.Abort(c, http.StatusUnauthorized, dto.CodeUnauthorized, "authentication required")
		return
	}

	cfg, err := h.svc.Create(c.Request.Context(), application.CreateConfigInput{
		Name:       req.Name,
		TargetPath: req.TargetPath,
		Content:    []byte(req.Content),
		Comment:    req.Comment,
		CreatedBy:  claims.UserID,
	})
	if err != nil {
		h.writeConfigError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toConfigResponse(cfg))
}

// Get godoc
//
//	@Summary		Get a config
//	@Description	Returns config metadata (no content — use the versions endpoint to read content).
//	@Tags			configs
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Config ID (UUID)"
//	@Success		200	{object}	dto.ConfigResponse
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/configs/{id} [get]
func (h *ConfigHandler) Get(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	cfg, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		h.writeConfigError(c, err)
		return
	}
	c.JSON(http.StatusOK, toConfigResponse(cfg))
}

// ListVersions godoc
//
//	@Summary		List config versions
//	@Description	Returns the full version history with content, newest first.
//	@Tags			configs
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Config ID (UUID)"
//	@Success		200	{array}		dto.ConfigVersionResponse
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/configs/{id}/versions [get]
func (h *ConfigHandler) ListVersions(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	versions, err := h.svc.ListVersions(c.Request.Context(), id)
	if err != nil {
		h.writeConfigError(c, err)
		return
	}
	out := make([]dto.ConfigVersionResponse, len(versions))
	for i, v := range versions {
		out[i] = toConfigVersionResponse(v)
	}
	c.JSON(http.StatusOK, out)
}

// AddVersion godoc
//
//	@Summary		Add a config version
//	@Description	Stores new content as the next version. Attached services pick it up on their next deployment.
//	@Tags			configs
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string						true	"Config ID (UUID)"
//	@Param			body	body		dto.AddConfigVersionRequest	true	"New content"
//	@Success		200		{object}	dto.ConfigResponse
//	@Failure		400		{object}	dto.ErrorResponse	"validation_error"
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Failure		404		{object}	dto.ErrorResponse
//	@Failure		422		{object}	dto.ErrorResponse	"content too large or invalid UTF-8"
//	@Router			/configs/{id}/versions [post]
func (h *ConfigHandler) AddVersion(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var req dto.AddConfigVersionRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}

	claims, ok := middleware.ClaimsFrom(c)
	if !ok {
		dto.Abort(c, http.StatusUnauthorized, dto.CodeUnauthorized, "authentication required")
		return
	}

	cfg, err := h.svc.AddVersion(c.Request.Context(), id, []byte(req.Content), req.Comment, claims.UserID)
	if err != nil {
		h.writeConfigError(c, err)
		return
	}
	c.JSON(http.StatusOK, toConfigResponse(cfg))
}

// Delete godoc
//
//	@Summary		Delete a config
//	@Description	Fails if the config is still attached to any service.
//	@Tags			configs
//	@Security		BearerAuth
//	@Param			id	path	string	true	"Config ID (UUID)"
//	@Success		204
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Failure		409	{object}	dto.ErrorResponse	"config in use"
//	@Router			/configs/{id} [delete]
func (h *ConfigHandler) Delete(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		h.writeConfigError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListForService godoc
//
//	@Summary		List configs attached to a service
//	@Tags			configs
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Service ID (UUID)"
//	@Success		200	{array}		dto.ServiceConfigResponse
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/services/{id}/configs [get]
func (h *ConfigHandler) ListForService(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	attachments, err := h.svc.ListServiceConfigs(c.Request.Context(), serviceID)
	if err != nil {
		h.writeConfigError(c, err)
		return
	}
	out := make([]dto.ServiceConfigResponse, len(attachments))
	for i, a := range attachments {
		out[i] = dto.ServiceConfigResponse{
			ConfigID:   a.Config.ID.String(),
			Name:       a.Config.Name,
			TargetPath: a.TargetPath,
		}
	}
	c.JSON(http.StatusOK, out)
}

// AttachToService godoc
//
//	@Summary		Attach a config to a service
//	@Tags			configs
//	@Security		BearerAuth
//	@Accept			json
//	@Param			id		path	string					true	"Service ID (UUID)"
//	@Param			body	body	dto.AttachConfigRequest	true	"Config to attach"
//	@Success		204
//	@Failure		400	{object}	dto.ErrorResponse	"validation_error"
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse	"service or config not found"
//	@Failure		409	{object}	dto.ErrorResponse	"already attached"
//	@Router			/services/{id}/configs [post]
func (h *ConfigHandler) AttachToService(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var req dto.AttachConfigRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}
	configID, ok := parseUUIDValue(c, req.ConfigID, "config_id")
	if !ok {
		return
	}

	if err := h.svc.AttachToService(c.Request.Context(), serviceID, configID, req.TargetPath); err != nil {
		h.writeConfigError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DetachFromService godoc
//
//	@Summary		Detach a config from a service
//	@Tags			configs
//	@Security		BearerAuth
//	@Param			id			path	string	true	"Service ID (UUID)"
//	@Param			configId	path	string	true	"Config ID (UUID)"
//	@Success		204
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse	"attachment not found"
//	@Router			/services/{id}/configs/{configId} [delete]
func (h *ConfigHandler) DetachFromService(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	configID, ok := parseUUID(c, "configId")
	if !ok {
		return
	}
	if err := h.svc.DetachFromService(c.Request.Context(), serviceID, configID); err != nil {
		h.writeConfigError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (h *ConfigHandler) writeConfigError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domainerrors.ErrNotFound):
		dto.Abort(c, http.StatusNotFound, dto.CodeNotFound, "resource not found")
	case errors.Is(err, domainerrors.ErrConflict), errors.Is(err, config.ErrConfigInUse):
		dto.Abort(c, http.StatusConflict, dto.CodeConflict, err.Error())
	case errors.Is(err, config.ErrInvalidName),
		errors.Is(err, config.ErrContentTooLarge),
		errors.Is(err, config.ErrInvalidUTF8):
		dto.Abort(c, http.StatusUnprocessableEntity, dto.CodeUnprocessable, err.Error())
	default:
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "internal error")
	}
}

func toConfigResponse(c *config.Config) dto.ConfigResponse {
	return dto.ConfigResponse{
		ID:             c.ID.String(),
		Name:           c.Name,
		TargetPath:     c.TargetPath,
		CurrentVersion: c.CurrentVersion,
		CreatedAt:      c.CreatedAt,
		UpdatedAt:      c.UpdatedAt,
	}
}

func toConfigVersionResponse(v *config.ConfigVersion) dto.ConfigVersionResponse {
	createdBy := ""
	if v.CreatedBy != uuid.Nil {
		createdBy = v.CreatedBy.String()
	}
	return dto.ConfigVersionResponse{
		Version:   v.Version,
		Content:   string(v.Content),
		Comment:   v.Comment,
		CreatedBy: createdBy,
		CreatedAt: v.CreatedAt,
	}
}
