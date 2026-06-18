package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/user"
)

type UserHandler struct {
	svc *application.UserService
}

func NewUserHandler(svc *application.UserService) *UserHandler {
	return &UserHandler{svc: svc}
}

// Register wires the user-management routes. All routes are Admin-only (F-V1-01).
func (h *UserHandler) Register(protected *gin.RouterGroup) {
	u := protected.Group("/users")
	u.Use(middleware.RequireRole(user.RoleAdmin))
	u.GET("", h.List)
	u.POST("", h.Create)
	u.PUT("/:id", h.Update)
	u.DELETE("/:id", h.Delete)
}

// List godoc
//
//	@Summary		List users (Admin only)
//	@Tags			users
//	@Security		BearerAuth
//	@Produce		json
//	@Param			page	query		int	false	"Page number (default 1)"
//	@Param			size	query		int	false	"Page size (default 20, max 100)"
//	@Success		200		{object}	dto.UserListResponse
//	@Failure		401		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Router			/users [get]
func (h *UserHandler) List(c *gin.Context) {
	page := parsePage(c)
	items, total, err := h.svc.List(c.Request.Context(), page)
	if err != nil {
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "failed to list users")
		return
	}
	resp := dto.UserListResponse{
		Items: make([]dto.UserResponse, len(items)),
		Total: total,
		Page:  page.Number,
		Size:  page.Size,
	}
	for i, u := range items {
		resp.Items[i] = toUserResponse(u)
	}
	c.JSON(http.StatusOK, resp)
}

// Create godoc
//
//	@Summary		Create a user (Admin only)
//	@Tags			users
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		dto.CreateUserRequest	true	"User"
//	@Success		201		{object}	dto.UserResponse
//	@Failure		400		{object}	dto.ErrorResponse
//	@Failure		403		{object}	dto.ErrorResponse
//	@Failure		409		{object}	dto.ErrorResponse	"email already in use"
//	@Failure		422		{object}	dto.ErrorResponse
//	@Router			/users [post]
func (h *UserHandler) Create(c *gin.Context) {
	var req dto.CreateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}
	u, err := h.svc.Create(c.Request.Context(), application.CreateUserInput{
		Email:    req.Email,
		Password: req.Password,
		Role:     user.Role(req.Role),
	})
	if err != nil {
		h.writeUserError(c, err)
		return
	}
	c.JSON(http.StatusCreated, toUserResponse(u))
}

// Update godoc
//
//	@Summary		Update a user (Admin only)
//	@Tags			users
//	@Security		BearerAuth
//	@Accept			json
//	@Produce		json
//	@Param			id		path		string					true	"User ID"
//	@Param			body	body		dto.UpdateUserRequest	true	"Changes"
//	@Success		200		{object}	dto.UserResponse
//	@Failure		403		{object}	dto.ErrorResponse	"last admin / self-demote"
//	@Failure		404		{object}	dto.ErrorResponse
//	@Failure		422		{object}	dto.ErrorResponse
//	@Router			/users/{id} [put]
func (h *UserHandler) Update(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	var req dto.UpdateUserRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}

	in := application.UpdateUserInput{Active: req.Active, Password: req.Password}
	if req.Role != nil {
		r := user.Role(*req.Role)
		in.Role = &r
	}

	actingID := h.actingUserID(c)
	u, err := h.svc.Update(c.Request.Context(), actingID, id, in)
	if err != nil {
		h.writeUserError(c, err)
		return
	}
	c.JSON(http.StatusOK, toUserResponse(u))
}

// Delete godoc
//
//	@Summary		Delete a user (Admin only)
//	@Tags			users
//	@Security		BearerAuth
//	@Param			id	path	string	true	"User ID"
//	@Success		204	"deleted"
//	@Failure		403	{object}	dto.ErrorResponse	"last admin / self-delete"
//	@Failure		404	{object}	dto.ErrorResponse
//	@Router			/users/{id} [delete]
func (h *UserHandler) Delete(c *gin.Context) {
	id, ok := parseUUID(c, "id")
	if !ok {
		return
	}
	if err := h.svc.Delete(c.Request.Context(), h.actingUserID(c), id); err != nil {
		h.writeUserError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *UserHandler) actingUserID(c *gin.Context) uuid.UUID {
	if claims, ok := middleware.ClaimsFrom(c); ok {
		return claims.UserID
	}
	return uuid.Nil
}

// ─── Error mapping ────────────────────────────────────────────────────────────

func (h *UserHandler) writeUserError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, application.ErrLastAdmin),
		errors.Is(err, application.ErrSelfDelete),
		errors.Is(err, application.ErrSelfDemote):
		dto.Abort(c, http.StatusForbidden, dto.CodeForbidden, err.Error())
	case errors.Is(err, application.ErrWeakPassword),
		errors.Is(err, application.ErrEmailRequired),
		errors.Is(err, user.ErrInvalidRole):
		dto.Abort(c, http.StatusUnprocessableEntity, dto.CodeUnprocessable, err.Error())
	default:
		writeError(c, err, "user not found")
	}
}

func toUserResponse(u *user.User) dto.UserResponse {
	return dto.UserResponse{
		ID:        u.ID.String(),
		Email:     u.Email,
		Role:      string(u.Role),
		Active:    u.Active,
		CreatedAt: u.CreatedAt,
		UpdatedAt: u.UpdatedAt,
	}
}
