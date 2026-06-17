package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/secret"
	"github.com/open226bf/hivemind/internal/domain/user"
	"github.com/open226bf/hivemind/pkg/domainerrors"
)

type SecretHandler struct {
	svc *application.SecretService
}

func NewSecretHandler(svc *application.SecretService) *SecretHandler {
	return &SecretHandler{svc: svc}
}

// Register wires secret CRUD and service-attachment routes.
func (h *SecretHandler) Register(protected *gin.RouterGroup) {
	// Secret catalog management is Admin-only (F-V1-01); attaching an existing
	// secret to a service stays with Operators as part of service management.
	g := protected.Group("/secrets")
	g.GET("", middleware.RequireRole(user.RoleViewer), h.List)
	g.POST("", middleware.RequireRole(user.RoleAdmin), h.Create)
	g.GET("/:id", middleware.RequireRole(user.RoleViewer), h.Get)
	g.POST("/:id/rotate", middleware.RequireRole(user.RoleAdmin), h.Rotate)
	g.DELETE("/:id", middleware.RequireRole(user.RoleAdmin), h.Delete)

	s := protected.Group("/services/:id/secrets")
	s.GET("", middleware.RequireRole(user.RoleViewer), h.ListForService)
	s.POST("", middleware.RequireRole(user.RoleOperator), h.AttachToService)
	s.DELETE("/:secretId", middleware.RequireRole(user.RoleOperator), h.DetachFromService)
}

// List godoc
//
//	@Summary		List secrets
//	@Description	Returns secret metadata only — values are never exposed.
//	@Tags			secrets
//	@Security		BearerAuth
//	@Produce		json
//	@Param			page	query		int	false	"Page number (default 1)"
//	@Param			size	query		int	false	"Page size (default 20, max 100)"
//	@Success		200		{object}	dto.SecretListResponse
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Router			/secrets [get]
func (h *SecretHandler) List(c *gin.Context) {
	page := parsePage(c)
	items, total, err := h.svc.List(c.Request.Context(), currentCluster(c), page)
	if err != nil {
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "failed to list secrets")
		return
	}

	resp := dto.SecretListResponse{
		Items: make([]dto.SecretResponse, len(items)),
		Total: total,
		Page:  page.Number,
		Size:  page.Size,
	}
	for i, s := range items {
		resp.Items[i] = toSecretResponse(s)
	}
	c.JSON(http.StatusOK, resp)
}

// Create godoc
//
//	@Summary		Create a secret
//	@Description	Stores a write-only secret. The value is encrypted at rest and never returned by any endpoint.
//	@Tags			secrets
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		dto.CreateSecretRequest	true	"Secret definition"
//	@Success		201		{object}	dto.SecretResponse
//	@Failure		400		{object}	dto.ErrorResponse	"validation_error"
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Failure		409		{object}	dto.ErrorResponse	"name already taken"
//	@Failure		422		{object}	dto.ErrorResponse	"invalid name or empty value"
//	@Router			/secrets [post]
func (h *SecretHandler) Create(c *gin.Context) {
	var req dto.CreateSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}

	claims, ok := middleware.ClaimsFrom(c)
	if !ok {
		dto.Abort(c, http.StatusUnauthorized, dto.CodeUnauthorized, "authentication required")
		return
	}

	clusterID := currentCluster(c) // active cluster from X-Hivemind-Cluster
	sec, err := h.svc.Create(c.Request.Context(), application.CreateSecretInput{
		Name:       req.Name,
		TargetPath: req.TargetPath,
		Value:      []byte(req.Value),
		CreatedBy:  claims.UserID,
		Cluster:    clusterID,
	})
	if err != nil {
		h.writeSecretError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toSecretResponse(sec))
}

// Get godoc
//
//	@Summary		Get a secret
//	@Description	Returns secret metadata only.
//	@Tags			secrets
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Secret ID (UUID)"
//	@Success		200	{object}	dto.SecretResponse
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/secrets/{id} [get]
func (h *SecretHandler) Get(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	sec, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		h.writeSecretError(c, err)
		return
	}
	c.JSON(http.StatusOK, toSecretResponse(sec))
}

// Rotate godoc
//
//	@Summary		Rotate a secret
//	@Description	Stores a new encrypted version and increments the version counter. Attached services pick up the new version on their next deployment.
//	@Tags			secrets
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string					true	"Secret ID (UUID)"
//	@Param			body	body		dto.RotateSecretRequest	true	"New value"
//	@Success		200		{object}	dto.SecretResponse
//	@Failure		400		{object}	dto.ErrorResponse	"validation_error"
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Failure		404		{object}	dto.ErrorResponse
//	@Failure		422		{object}	dto.ErrorResponse	"empty value"
//	@Router			/secrets/{id}/rotate [post]
func (h *SecretHandler) Rotate(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var req dto.RotateSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}

	sec, err := h.svc.Rotate(c.Request.Context(), id, []byte(req.Value))
	if err != nil {
		h.writeSecretError(c, err)
		return
	}
	c.JSON(http.StatusOK, toSecretResponse(sec))
}

// Delete godoc
//
//	@Summary		Delete a secret
//	@Description	Fails if the secret is still attached to any service.
//	@Tags			secrets
//	@Security		BearerAuth
//	@Param			id	path	string	true	"Secret ID (UUID)"
//	@Success		204
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Failure		409	{object}	dto.ErrorResponse	"secret in use"
//	@Router			/secrets/{id} [delete]
func (h *SecretHandler) Delete(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		h.writeSecretError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ListForService godoc
//
//	@Summary		List secrets attached to a service
//	@Tags			secrets
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Service ID (UUID)"
//	@Success		200	{array}		dto.ServiceSecretResponse
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/services/{id}/secrets [get]
func (h *SecretHandler) ListForService(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	attachments, err := h.svc.ListServiceSecrets(c.Request.Context(), serviceID)
	if err != nil {
		h.writeSecretError(c, err)
		return
	}
	out := make([]dto.ServiceSecretResponse, len(attachments))
	for i, a := range attachments {
		out[i] = dto.ServiceSecretResponse{
			SecretID:   a.Secret.ID.String(),
			Name:       a.Secret.Name,
			TargetPath: a.TargetPath,
		}
	}
	c.JSON(http.StatusOK, out)
}

// AttachToService godoc
//
//	@Summary		Attach a secret to a service
//	@Tags			secrets
//	@Security		BearerAuth
//	@Accept			json
//	@Param			id		path	string					true	"Service ID (UUID)"
//	@Param			body	body	dto.AttachSecretRequest	true	"Secret to attach"
//	@Success		204
//	@Failure		400	{object}	dto.ErrorResponse	"validation_error"
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse	"service or secret not found"
//	@Failure		409	{object}	dto.ErrorResponse	"already attached"
//	@Router			/services/{id}/secrets [post]
func (h *SecretHandler) AttachToService(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var req dto.AttachSecretRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}
	secretID, ok := parseUUIDValue(c, req.SecretID, "secret_id")
	if !ok {
		return
	}

	if err := h.svc.AttachToService(c.Request.Context(), serviceID, secretID, req.TargetPath); err != nil {
		h.writeSecretError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// DetachFromService godoc
//
//	@Summary		Detach a secret from a service
//	@Tags			secrets
//	@Security		BearerAuth
//	@Param			id			path	string	true	"Service ID (UUID)"
//	@Param			secretId	path	string	true	"Secret ID (UUID)"
//	@Success		204
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		404	{object}	dto.ErrorResponse	"attachment not found"
//	@Router			/services/{id}/secrets/{secretId} [delete]
func (h *SecretHandler) DetachFromService(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	secretID, ok := parseUUID(c, "secretId")
	if !ok {
		return
	}
	if err := h.svc.DetachFromService(c.Request.Context(), serviceID, secretID); err != nil {
		h.writeSecretError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (h *SecretHandler) writeSecretError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domainerrors.ErrNotFound):
		dto.Abort(c, http.StatusNotFound, dto.CodeNotFound, "resource not found")
	case errors.Is(err, domainerrors.ErrConflict), errors.Is(err, secret.ErrSecretInUse), errors.Is(err, application.ErrClusterMismatch):
		dto.Abort(c, http.StatusConflict, dto.CodeConflict, err.Error())
	case errors.Is(err, secret.ErrInvalidName), errors.Is(err, secret.ErrEmptyValue):
		dto.Abort(c, http.StatusUnprocessableEntity, dto.CodeUnprocessable, err.Error())
	default:
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "internal error")
	}
}

func toSecretResponse(s *secret.Secret) dto.SecretResponse {
	return dto.SecretResponse{
		ID:             s.ID.String(),
		ClusterID:      clusterIDString(s.ClusterID),
		Name:           s.Name,
		TargetPath:     s.TargetPath,
		CurrentVersion: s.CurrentVersion,
		Checksum:       s.Checksum,
		CreatedBy:      s.CreatedBy.String(),
		CreatedAt:      s.CreatedAt,
		UpdatedAt:      s.UpdatedAt,
	}
}
