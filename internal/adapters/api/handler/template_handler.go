package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/template"
	"github.com/open226bf/hivemind/internal/domain/user"
	"github.com/open226bf/hivemind/pkg/domainerrors"
)

type TemplateHandler struct {
	svc *application.TemplateService
}

func NewTemplateHandler(svc *application.TemplateService) *TemplateHandler {
	return &TemplateHandler{svc: svc}
}

// Register wires template management and instantiation routes (F-V2-07).
func (h *TemplateHandler) Register(protected *gin.RouterGroup) {
	// Templates are managed by Admins; any Operator may instantiate.
	g := protected.Group("/templates")
	g.GET("", middleware.RequireRole(user.RoleViewer), h.List)
	g.POST("", middleware.RequireRole(user.RoleAdmin), h.Create)
	g.GET("/:id", middleware.RequireRole(user.RoleViewer), h.Get)
	g.PUT("/:id", middleware.RequireRole(user.RoleAdmin), h.Update)
	g.DELETE("/:id", middleware.RequireRole(user.RoleAdmin), h.Delete)

	protected.POST("/services/from-template/:templateId", middleware.RequireRole(user.RoleOperator), h.Instantiate)
}

// List godoc
//
//	@Summary		List service templates
//	@Tags			templates
//	@Security		BearerAuth
//	@Produce		json
//	@Param			page	query		int	false	"Page number (default 1)"
//	@Param			size	query		int	false	"Page size (default 20, max 100)"
//	@Success		200		{object}	dto.TemplateListResponse
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Router			/templates [get]
func (h *TemplateHandler) List(c *gin.Context) {
	page := parsePage(c)
	items, total, err := h.svc.List(c.Request.Context(), page)
	if err != nil {
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "failed to list templates")
		return
	}
	resp := dto.TemplateListResponse{
		Items: make([]dto.TemplateResponse, len(items)),
		Total: total,
		Page:  page.Number,
		Size:  page.Size,
	}
	for i, t := range items {
		resp.Items[i] = toTemplateResponse(t)
	}
	c.JSON(http.StatusOK, resp)
}

// Create godoc
//
//	@Summary		Create a service template
//	@Description	Admin-only. Defines defaults (image, scaling, resources, strategy, placement, networks) and optional locked fields.
//	@Tags			templates
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		dto.CreateTemplateRequest	true	"Template definition"
//	@Success		201		{object}	dto.TemplateResponse
//	@Failure		400		{object}	dto.ErrorResponse	"validation_error"
//	@Failure		409		{object}	dto.ErrorResponse	"name already taken"
//	@Failure		422		{object}	dto.ErrorResponse	"invalid template"
//	@Router			/templates [post]
func (h *TemplateHandler) Create(c *gin.Context) {
	var req dto.CreateTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}
	spec, ok := h.specFromDTO(c, req.Spec)
	if !ok {
		return
	}
	claims, _ := middleware.ClaimsFrom(c)
	var createdBy uuid.UUID
	if claims != nil {
		createdBy = claims.UserID
	}

	t, err := h.svc.Create(c.Request.Context(), application.SaveTemplateInput{
		Name:         req.Name,
		Description:  req.Description,
		Spec:         spec,
		LockedFields: req.LockedFields,
		CreatedBy:    createdBy,
	})
	if err != nil {
		h.writeTemplateError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toTemplateResponse(t))
}

// Get godoc
//
//	@Summary		Get a service template
//	@Tags			templates
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Template ID (UUID)"
//	@Success		200	{object}	dto.TemplateResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/templates/{id} [get]
func (h *TemplateHandler) Get(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	t, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		h.writeTemplateError(c, err)
		return
	}
	c.JSON(http.StatusOK, toTemplateResponse(t))
}

// Update godoc
//
//	@Summary		Update a service template
//	@Description	Admin-only. Replaces the spec and locks, bumping the template version. The name is immutable.
//	@Tags			templates
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string						true	"Template ID (UUID)"
//	@Param			body	body		dto.UpdateTemplateRequest	true	"Updated definition"
//	@Success		200		{object}	dto.TemplateResponse
//	@Failure		404		{object}	dto.ErrorResponse
//	@Failure		422		{object}	dto.ErrorResponse
//	@Router			/templates/{id} [put]
func (h *TemplateHandler) Update(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var req dto.UpdateTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}
	spec, ok := h.specFromDTO(c, req.Spec)
	if !ok {
		return
	}
	t, err := h.svc.Update(c.Request.Context(), id, application.SaveTemplateInput{
		Description:  req.Description,
		Spec:         spec,
		LockedFields: req.LockedFields,
	})
	if err != nil {
		h.writeTemplateError(c, err)
		return
	}
	c.JSON(http.StatusOK, toTemplateResponse(t))
}

// Delete godoc
//
//	@Summary		Delete a service template
//	@Tags			templates
//	@Security		BearerAuth
//	@Param			id	path	string	true	"Template ID (UUID)"
//	@Success		204
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/templates/{id} [delete]
func (h *TemplateHandler) Delete(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		h.writeTemplateError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// Instantiate godoc
//
//	@Summary		Create a service from a template
//	@Description	Operator-allowed. Pre-fills the new service from the template; locked fields cannot be overridden.
//	@Tags			templates
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			templateId	path		string							true	"Template ID (UUID)"
//	@Param			body		body		dto.InstantiateTemplateRequest	true	"Instance values"
//	@Success		201			{object}	dto.ServiceResponse
//	@Failure		400			{object}	dto.ErrorResponse	"validation_error"
//	@Failure		404			{object}	dto.ErrorResponse	"template not found"
//	@Failure		409			{object}	dto.ErrorResponse	"service name already taken"
//	@Failure		422			{object}	dto.ErrorResponse	"locked field overridden or invalid value"
//	@Router			/services/from-template/{templateId} [post]
func (h *TemplateHandler) Instantiate(c *gin.Context) {
	templateID, ok := parseUUID(c, "templateId")
	if !ok {
		return
	}
	var req dto.InstantiateTemplateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}

	in := application.InstantiateInput{
		Name:             req.Name,
		Description:      req.Description,
		TagOverride:      req.Tag,
		ReplicasOverride: req.Replicas,
		Cluster:          writeCluster(c),
	}
	if req.Resources != nil {
		r := fromResourcesDTO(*req.Resources)
		in.ResourcesOverride = &r
	}

	svc, err := h.svc.Instantiate(c.Request.Context(), templateID, in)
	if err != nil {
		h.writeTemplateError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toServiceResponse(svc))
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

// specFromDTO converts a template spec DTO, validating embedded network UUIDs.
// On a malformed UUID it writes a 400 and returns ok=false.
func (h *TemplateHandler) specFromDTO(c *gin.Context, in dto.TemplateSpecDTO) (template.Spec, bool) {
	netIDs := make([]uuid.UUID, 0, len(in.NetworkIDs))
	for _, raw := range in.NetworkIDs {
		id, err := uuid.Parse(raw)
		if err != nil {
			dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid network id: "+raw)
			return template.Spec{}, false
		}
		netIDs = append(netIDs, id)
	}
	return template.Spec{
		Image:        in.Image,
		Tag:          in.Tag,
		Replicas:     in.Replicas,
		Resources:    fromResourcesDTO(in.Resources),
		UpdateConfig: fromUpdateConfigDTO(in.UpdateConfig),
		Placement:    fromPlacementDTO(in.Placement),
		NetworkIDs:   netIDs,
	}, true
}

func (h *TemplateHandler) writeTemplateError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, domainerrors.ErrNotFound):
		dto.Abort(c, http.StatusNotFound, dto.CodeNotFound, "resource not found")
	case errors.Is(err, domainerrors.ErrConflict):
		dto.Abort(c, http.StatusConflict, dto.CodeConflict, err.Error())
	case errors.Is(err, template.ErrInvalidName),
		errors.Is(err, template.ErrInvalidImage),
		errors.Is(err, template.ErrInvalidLock),
		errors.Is(err, template.ErrFieldLocked),
		errors.Is(err, application.ErrResourceExceedsCluster),
		isValidationError(err):
		dto.Abort(c, http.StatusUnprocessableEntity, dto.CodeUnprocessable, err.Error())
	default:
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "internal error")
	}
}

func toTemplateResponse(t *template.Template) dto.TemplateResponse {
	netIDs := make([]string, len(t.Spec.NetworkIDs))
	for i, id := range t.Spec.NetworkIDs {
		netIDs[i] = id.String()
	}
	return dto.TemplateResponse{
		ID:          t.ID.String(),
		Name:        t.Name,
		Description: t.Description,
		Version:     t.Version,
		Spec: dto.TemplateSpecDTO{
			Image:        t.Spec.Image,
			Tag:          t.Spec.Tag,
			Replicas:     t.Spec.Replicas,
			Resources:    toResourcesDTO(t.Spec.Resources),
			UpdateConfig: toUpdateConfigDTO(t.Spec.UpdateConfig),
			Placement:    toPlacementDTO(t.Spec.Placement),
			NetworkIDs:   netIDs,
		},
		LockedFields: nullSafeStrings(t.LockedFields),
		CreatedAt:    t.CreatedAt,
		UpdatedAt:    t.UpdatedAt,
	}
}
