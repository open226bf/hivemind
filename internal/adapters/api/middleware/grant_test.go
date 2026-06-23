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

	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/adapters/auth"
	"github.com/open226bf/hivemind/internal/domain/acl"
	"github.com/open226bf/hivemind/internal/domain/user"
	"github.com/open226bf/hivemind/internal/ports"
)

// fakeResolver maps any hive id to a fixed (cluster, hive) pair.
type fakeResolver struct {
	cluster uuid.UUID
	hive    uuid.UUID
}

func (r fakeResolver) Resolve(_ context.Context, target middleware.Target, id uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	if target == middleware.TargetCluster {
		return id, uuid.Nil, nil
	}
	return r.cluster, r.hive, nil
}

func tokenWithScopes(t *testing.T, tokens *auth.TokenService, role user.Role, scopes []ports.Scope) string {
	t.Helper()
	u, _ := user.New("s@b.c", "h", role)
	tok, _, err := tokens.GenerateAccessToken(u, scopes)
	require.NoError(t, err)
	return tok
}

func TestRequireVerb_HiveCascade(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key, _, err := auth.LoadOrGenerateKey("")
	require.NoError(t, err)
	tokens := auth.NewTokenService(auth.Config{PrivateKey: key, Issuer: "hivemind"})

	clusterID := uuid.New()
	hiveID := uuid.New()
	resolver := fakeResolver{cluster: clusterID, hive: hiveID}
	cfg := middleware.ACLConfig{Enforced: true}

	newRouter := func() *gin.Engine {
		r := gin.New()
		r.GET("/hives/:id",
			middleware.Auth(tokens),
			middleware.RequireVerb(middleware.TargetHive, "id", acl.VerbWrite, resolver, cfg),
			func(c *gin.Context) { c.Status(http.StatusOK) })
		return r
	}

	call := func(bearer string) int {
		req := httptest.NewRequest(http.MethodGet, "/hives/"+hiveID.String(), nil)
		req.Header.Set("Authorization", "Bearer "+bearer)
		w := httptest.NewRecorder()
		newRouter().ServeHTTP(w, req)
		return w.Code
	}

	// Admin bypasses regardless of scopes.
	assert.Equal(t, http.StatusOK, call(tokenWithScopes(t, tokens, user.RoleAdmin, nil)))

	// Cluster write cascades to the hive → allowed.
	clusterWrite := []ports.Scope{{Type: acl.ResourceCluster, ID: clusterID, Verb: acl.VerbWrite}}
	assert.Equal(t, http.StatusOK, call(tokenWithScopes(t, tokens, user.RoleOperator, clusterWrite)))

	// Hive-specific write → allowed.
	hiveWrite := []ports.Scope{{Type: acl.ResourceHive, ID: hiveID, Verb: acl.VerbWrite}}
	assert.Equal(t, http.StatusOK, call(tokenWithScopes(t, tokens, user.RoleOperator, hiveWrite)))

	// Only read on the cluster → forbidden for a write route.
	clusterRead := []ports.Scope{{Type: acl.ResourceCluster, ID: clusterID, Verb: acl.VerbRead}}
	assert.Equal(t, http.StatusForbidden, call(tokenWithScopes(t, tokens, user.RoleOperator, clusterRead)))

	// No scopes → forbidden (deny-by-default).
	assert.Equal(t, http.StatusForbidden, call(tokenWithScopes(t, tokens, user.RoleOperator, nil)))
}

func TestRequireVerb_ShadowModeNeverBlocks(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key, _, err := auth.LoadOrGenerateKey("")
	require.NoError(t, err)
	tokens := auth.NewTokenService(auth.Config{PrivateKey: key, Issuer: "hivemind"})

	clusterID := uuid.New()
	hiveID := uuid.New()
	resolver := fakeResolver{cluster: clusterID, hive: hiveID}
	cfg := middleware.ACLConfig{Enforced: false} // shadow

	r := gin.New()
	r.GET("/hives/:id",
		middleware.Auth(tokens),
		middleware.RequireVerb(middleware.TargetHive, "id", acl.VerbManage, resolver, cfg),
		func(c *gin.Context) { c.Status(http.StatusOK) })

	// A user with no scopes would be denied when enforced, but shadow lets through.
	req := httptest.NewRequest(http.MethodGet, "/hives/"+hiveID.String(), nil)
	req.Header.Set("Authorization", "Bearer "+tokenWithScopes(t, tokens, user.RoleOperator, nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	assert.Equal(t, http.StatusOK, w.Code)
}
