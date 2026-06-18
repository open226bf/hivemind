package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/orange/hivemind/internal/adapters/api/dto"
	"github.com/orange/hivemind/internal/adapters/api/middleware"
	"github.com/orange/hivemind/internal/application"
	"github.com/orange/hivemind/internal/domain/hive"
	"github.com/orange/hivemind/internal/domain/user"
)

type HiveHandler struct {
	svc *application.HiveService
}

func NewHiveHandler(svc *application.HiveService) *HiveHandler {
	return &HiveHandler{svc: svc}
}

// Register wires hive (project) routes and the service-assignment route.
func (h *HiveHandler) Register(protected *gin.RouterGroup) {
	g := protected.Group("/hives")
	g.GET("", middleware.RequireRole(user.RoleViewer), h.List)
	g.POST("", middleware.RequireRole(user.RoleOperator), h.Create)
	g.GET("/:id", middleware.RequireRole(user.RoleViewer), h.Get)
	g.GET("/:id/services", middleware.RequireRole(user.RoleViewer), h.ListServices)
	g.PUT("/:id", middleware.RequireRole(user.RoleOperator), h.Update)
	g.DELETE("/:id", middleware.RequireRole(user.RoleOperator), h.Delete)

	// Assign/move/unassign a service to a hive.
	protected.PUT("/services/:id/hive", middleware.RequireRole(user.RoleOperator), h.AssignService)
}

// List godoc
//
//	@Summary		List hives (projects)
//	@Tags			hives
//	@Security		BearerAuth
//	@Produce		json
//	@Param			page	query		int	false	"Page number (default 1)"
//	@Param			size	query		int	false	"Page size (default 20, max 100)"
//	@Success		200		{object}	dto.HiveListResponse
//	@Failure		401		{object}	dto.ErrorResponse
//	@Router			/hives [get]
func (h *HiveHandler) List(c *gin.Context) {
	page := parsePage(c)
	items, total, err := h.svc.List(c.Request.Context(), currentCluster(c), page)
	if err != nil {
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "failed to list hives")
		return
	}
	resp := dto.HiveListResponse{
		Items: make([]dto.HiveResponse, len(items)),
		Total: total,
		Page:  page.Number,
		Size:  page.Size,
	}
	for i, s := range items {
		resp.Items[i] = toHiveResponse(s.Hive, s.ServiceCount)
	}
	c.JSON(http.StatusOK, resp)
}

// Create godoc
//
//	@Summary		Create a hive
//	@Tags			hives
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		dto.CreateHiveRequest	true	"Hive definition"
//	@Success		201		{object}	dto.HiveResponse
//	@Failure		400		{object}	dto.ErrorResponse	"validation_error"
//	@Failure		409		{object}	dto.ErrorResponse	"name already taken"
//	@Failure		422		{object}	dto.ErrorResponse	"invalid name or color"
//	@Router			/hives [post]
func (h *HiveHandler) Create(c *gin.Context) {
	var req dto.CreateHiveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}
	hv, err := h.svc.Create(c.Request.Context(), writeCluster(c), application.SaveHiveInput{
		Name: req.Name, Description: req.Description, Color: req.Color,
	})
	if err != nil {
		h.writeHiveError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toHiveResponse(hv, 0))
}

// Get godoc
//
//	@Summary		Get a hive
//	@Tags			hives
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Hive ID (UUID)"
//	@Success		200	{object}	dto.HiveResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/hives/{id} [get]
func (h *HiveHandler) Get(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	hv, err := h.svc.Get(c.Request.Context(), id)
	if err != nil {
		h.writeHiveError(c, err)
		return
	}
	c.JSON(http.StatusOK, toHiveResponse(hv, 0))
}

// ListServices godoc
//
//	@Summary		List a hive's services
//	@Tags			hives
//	@Security		BearerAuth
//	@Produce		json
//	@Param			id	path		string	true	"Hive ID (UUID)"
//	@Success		200	{array}		dto.ServiceResponse
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/hives/{id}/services [get]
func (h *HiveHandler) ListServices(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	services, err := h.svc.ListServices(c.Request.Context(), id)
	if err != nil {
		h.writeHiveError(c, err)
		return
	}
	out := make([]dto.ServiceResponse, len(services))
	for i, s := range services {
		out[i] = toServiceResponse(s)
	}
	c.JSON(http.StatusOK, out)
}

// Update godoc
//
//	@Summary		Update a hive
//	@Tags			hives
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string					true	"Hive ID (UUID)"
//	@Param			body	body		dto.UpdateHiveRequest	true	"Updated fields"
//	@Success		200		{object}	dto.HiveResponse
//	@Failure		404		{object}	dto.ErrorResponse
//	@Failure		422		{object}	dto.ErrorResponse
//	@Router			/hives/{id} [put]
func (h *HiveHandler) Update(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var req dto.UpdateHiveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}
	hv, err := h.svc.Update(c.Request.Context(), id, application.SaveHiveInput{
		Name: req.Name, Description: req.Description, Color: req.Color,
	})
	if err != nil {
		h.writeHiveError(c, err)
		return
	}
	c.JSON(http.StatusOK, toHiveResponse(hv, 0))
}

// Delete godoc
//
//	@Summary		Delete a hive
//	@Description	Fails if the hive still contains services.
//	@Tags			hives
//	@Security		BearerAuth
//	@Param			id	path	string	true	"Hive ID (UUID)"
//	@Success		204
//	@Failure		404	{object}	dto.ErrorResponse
//	@Failure		409	{object}	dto.ErrorResponse	"hive not empty"
//	@Router			/hives/{id} [delete]
func (h *HiveHandler) Delete(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), id); err != nil {
		h.writeHiveError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

// AssignService godoc
//
//	@Summary		Assign a service to a hive
//	@Description	Moves the service into a hive, or removes it from its hive when hive_id is null.
//	@Tags			hives
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string					true	"Service ID (UUID)"
//	@Param			body	body		dto.AssignHiveRequest	true	"Target hive (null to unassign)"
//	@Success		200		{object}	dto.ServiceResponse
//	@Failure		400		{object}	dto.ErrorResponse
//	@Failure		404		{object}	dto.ErrorResponse	"service or hive not found"
//	@Router			/services/{id}/hive [put]
func (h *HiveHandler) AssignService(c *gin.Context) {
	serviceID, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var req dto.AssignHiveRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}

	var hiveID *uuid.UUID
	if req.HiveID != nil && *req.HiveID != "" {
		id, err := uuid.Parse(*req.HiveID)
		if err != nil {
			dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid hive_id")
			return
		}
		hiveID = &id
	}

	svc, err := h.svc.MoveService(c.Request.Context(), serviceID, hiveID)
	if err != nil {
		h.writeHiveError(c, err)
		return
	}
	c.JSON(http.StatusOK, toServiceResponse(svc))
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func (h *HiveHandler) writeHiveError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, hive.ErrHiveNotEmpty):
		dto.Abort(c, http.StatusConflict, dto.CodeConflict, err.Error())
	case errors.Is(err, hive.ErrInvalidName), errors.Is(err, hive.ErrInvalidColor):
		dto.Abort(c, http.StatusUnprocessableEntity, dto.CodeUnprocessable, err.Error())
	default:
		writeError(c, err, "resource not found")
	}
}

func toHiveResponse(h *hive.Hive, serviceCount int64) dto.HiveResponse {
	return dto.HiveResponse{
		ID:           h.ID.String(),
		ClusterID:    clusterIDString(h.ClusterID),
		Name:         h.Name,
		Description:  h.Description,
		Color:        h.Color,
		ServiceCount: serviceCount,
		CreatedAt:    h.CreatedAt,
		UpdatedAt:    h.UpdatedAt,
	}
}
