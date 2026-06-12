package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/orange/hivemind/internal/adapters/api/middleware"
	"github.com/orange/hivemind/internal/adapters/auth"
	"github.com/orange/hivemind/internal/domain/user"
)

func setup(t *testing.T) (*auth.TokenService, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	key, _, err := auth.LoadOrGenerateKey("")
	require.NoError(t, err)
	tokens := auth.NewTokenService(auth.Config{PrivateKey: key, Issuer: "hivemind"})

	r := gin.New()
	r.GET("/protected", middleware.Auth(tokens), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	r.GET("/admin", middleware.Auth(tokens), middleware.RequireRole(user.RoleAdmin), func(c *gin.Context) {
		c.Status(http.StatusOK)
	})
	return tokens, r
}

func tokenFor(t *testing.T, tokens *auth.TokenService, role user.Role) string {
	t.Helper()
	u, _ := user.New("x@b.c", "h", role)
	tok, _, err := tokens.GenerateAccessToken(u)
	require.NoError(t, err)
	return tok
}

func do(r *gin.Engine, path, bearer string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if bearer != "" {
		req.Header.Set("Authorization", "Bearer "+bearer)
	}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestAuth_NoHeader(t *testing.T) {
	_, r := setup(t)
	assert.Equal(t, http.StatusUnauthorized, do(r, "/protected", "").Code)
}

func TestAuth_ValidToken(t *testing.T) {
	tokens, r := setup(t)
	assert.Equal(t, http.StatusOK, do(r, "/protected", tokenFor(t, tokens, user.RoleViewer)).Code)
}

func TestAuth_RefreshTokenRejected(t *testing.T) {
	tokens, r := setup(t)
	u, _ := user.New("x@b.c", "h", user.RoleViewer)
	refresh, _, _ := tokens.GenerateRefreshToken(u)
	assert.Equal(t, http.StatusUnauthorized, do(r, "/protected", refresh).Code)
}

func TestRBAC_AdminAllowed(t *testing.T) {
	tokens, r := setup(t)
	assert.Equal(t, http.StatusOK, do(r, "/admin", tokenFor(t, tokens, user.RoleAdmin)).Code)
}

func TestRBAC_OperatorForbidden(t *testing.T) {
	tokens, r := setup(t)
	assert.Equal(t, http.StatusForbidden, do(r, "/admin", tokenFor(t, tokens, user.RoleOperator)).Code)
}
