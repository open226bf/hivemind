package handler

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/application"
)

type AuthHandler struct {
	auth *application.AuthService
}

func NewAuthHandler(auth *application.AuthService) *AuthHandler {
	return &AuthHandler{auth: auth}
}

// Register wires auth routes. publicGroup is unauthenticated; protected requires a valid token.
func (h *AuthHandler) Register(public, protected *gin.RouterGroup) {
	public.POST("/auth/login", h.Login)
	public.POST("/auth/refresh", h.Refresh)
	protected.POST("/auth/logout", h.Logout)
	protected.GET("/auth/me", h.Me)
}

// Login godoc
//
//	@Summary		Authenticate by email/password
//	@Description	Returns an access token (15 min) and a refresh token (7 days).
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		dto.LoginRequest	true	"Credentials"
//	@Success		200		{object}	dto.TokenResponse
//	@Failure		400		{object}	dto.ErrorResponse	"validation_error"
//	@Failure		401		{object}	dto.ErrorResponse	"invalid email or password"
//	@Failure		403		{object}	dto.ErrorResponse	"account inactive"
//	@Failure		429		{object}	dto.ErrorResponse	"account temporarily locked"
//	@Router			/auth/login [post]
func (h *AuthHandler) Login(c *gin.Context) {
	var req dto.LoginRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}

	pair, err := h.auth.Login(c.Request.Context(), req.Email, req.Password)
	if err != nil {
		h.writeAuthError(c, err)
		return
	}
	c.JSON(http.StatusOK, toTokenResponse(pair))
}

// Refresh godoc
//
//	@Summary		Exchange a refresh token for a new token pair
//	@Description	The old refresh token is implicitly invalidated on the client side (stateless MVP).
//	@Tags			auth
//	@Accept			json
//	@Produce		json
//	@Param			body	body		dto.RefreshRequest	true	"Refresh token"
//	@Success		200		{object}	dto.TokenResponse
//	@Failure		400		{object}	dto.ErrorResponse	"validation_error"
//	@Failure		401		{object}	dto.ErrorResponse	"invalid or expired token"
//	@Router			/auth/refresh [post]
func (h *AuthHandler) Refresh(c *gin.Context) {
	var req dto.RefreshRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid request body", err.Error())
		return
	}

	pair, err := h.auth.Refresh(c.Request.Context(), req.RefreshToken)
	if err != nil {
		h.writeAuthError(c, err)
		return
	}
	c.JSON(http.StatusOK, toTokenResponse(pair))
}

// Logout godoc
//
//	@Summary		Log out
//	@Description	Stateless JWT: the client discards both tokens. A server-side blocklist will be added post-MVP.
//	@Tags			auth
//	@Security		BearerAuth
//	@Success		204
//	@Failure		401	{object}	dto.ErrorResponse
//	@Router			/auth/logout [post]
func (h *AuthHandler) Logout(c *gin.Context) {
	// Stateless JWT: nothing to revoke at the MVP. A token blocklist arrives later.
	c.Status(http.StatusNoContent)
}

// Me godoc
//
//	@Summary		Return the authenticated user
//	@Description	Returns id, email and role of the user owning the access token.
//	@Tags			auth
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{object}	dto.MeResponse
//	@Failure		401	{object}	dto.ErrorResponse
//	@Router			/auth/me [get]
func (h *AuthHandler) Me(c *gin.Context) {
	claims, ok := middleware.ClaimsFrom(c)
	if !ok {
		dto.Abort(c, http.StatusUnauthorized, dto.CodeUnauthorized, "authentication required")
		return
	}

	u, err := h.auth.Me(c.Request.Context(), claims)
	if err != nil {
		dto.Abort(c, http.StatusUnauthorized, dto.CodeUnauthorized, "user no longer exists")
		return
	}

	c.JSON(http.StatusOK, dto.MeResponse{
		ID:    u.ID.String(),
		Email: u.Email,
		Role:  string(u.Role),
	})
}

func (h *AuthHandler) writeAuthError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, application.ErrAccountLocked):
		dto.Abort(c, http.StatusTooManyRequests, dto.CodeForbidden, "account temporarily locked")
	case errors.Is(err, application.ErrInactiveUser):
		dto.Abort(c, http.StatusForbidden, dto.CodeForbidden, "account is inactive")
	case errors.Is(err, application.ErrInvalidCredentials), errors.Is(err, application.ErrInvalidToken):
		dto.Abort(c, http.StatusUnauthorized, dto.CodeUnauthorized, err.Error())
	default:
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "internal error")
	}
}

func toTokenResponse(p *application.TokenPair) dto.TokenResponse {
	return dto.TokenResponse{
		AccessToken:     p.AccessToken,
		RefreshToken:    p.RefreshToken,
		TokenType:       p.TokenType,
		AccessExpiresAt: p.AccessExpiresAt,
	}
}
