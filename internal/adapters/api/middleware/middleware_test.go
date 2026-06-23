package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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
	tok, _, err := tokens.GenerateAccessToken(u, nil)
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

type fakeDefaults struct{ id uuid.UUID }

func (f fakeDefaults) DefaultClusterID(context.Context) (uuid.UUID, error) { return f.id, nil }

// clusterCtxResult runs ClusterContext for a request and returns the scope and
// write cluster it stashed.
func clusterCtxResult(method, header string, defaults middleware.DefaultClusterResolver) (scope, write uuid.UUID) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.Handle(method, "/x", middleware.ClusterContext(defaults), func(c *gin.Context) {
		scope = c.MustGet(middleware.ClusterContextKey).(uuid.UUID)
		write = c.MustGet(middleware.ClusterWriteContextKey).(uuid.UUID)
		c.Status(http.StatusOK)
	})
	req := httptest.NewRequest(method, "/x", nil)
	if header != "" {
		req.Header.Set(middleware.ClusterHeader, header)
	}
	r.ServeHTTP(httptest.NewRecorder(), req)
	return scope, write
}

func TestClusterContext_HeaderScopesBoth(t *testing.T) {
	sel := uuid.New()
	def := fakeDefaults{id: uuid.New()}
	scope, write := clusterCtxResult(http.MethodPost, sel.String(), def)
	assert.Equal(t, sel, scope, "scope follows the header")
	assert.Equal(t, sel, write, "write follows the header (default not consulted)")
}

func TestClusterContext_HeaderlessWriteFallsBackToDefault(t *testing.T) {
	def := fakeDefaults{id: uuid.New()}
	scope, write := clusterCtxResult(http.MethodPost, "", def)
	assert.Equal(t, uuid.Nil, scope, "no header → scope is the zero UUID (lists aggregate all)")
	assert.Equal(t, def.id, write, "no header on a write → resource lands on the default cluster, never NULL")
}

func TestClusterContext_HeaderlessReadStaysUnscoped(t *testing.T) {
	def := fakeDefaults{id: uuid.New()}
	scope, write := clusterCtxResult(http.MethodGet, "", def)
	assert.Equal(t, uuid.Nil, scope)
	assert.Equal(t, uuid.Nil, write, "reads don't resolve the default (no extra lookup), they aggregate all")
}
