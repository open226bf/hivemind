package auth_test

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/orange/hivemind/internal/adapters/auth"
	"github.com/orange/hivemind/internal/domain/user"
	"github.com/orange/hivemind/internal/ports"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newService(t *testing.T) *auth.TokenService {
	t.Helper()
	key, generated, err := auth.LoadOrGenerateKey("")
	require.NoError(t, err)
	require.True(t, generated)
	return auth.NewTokenService(auth.Config{PrivateKey: key, Issuer: "hivemind"})
}

func TestAccessToken_RoundTrip(t *testing.T) {
	svc := newService(t)
	u, _ := user.New("op@hivemind.local", "h", user.RoleOperator)

	token, exp, err := svc.GenerateAccessToken(u, nil)
	require.NoError(t, err)
	assert.True(t, exp.After(time.Now()))

	claims, err := svc.Parse(token)
	require.NoError(t, err)
	assert.Equal(t, u.ID, claims.UserID)
	assert.Equal(t, "op@hivemind.local", claims.Email)
	assert.Equal(t, "operator", claims.Role)
	assert.Equal(t, ports.TokenTypeAccess, claims.TokenType)
}

func TestRefreshToken_Type(t *testing.T) {
	svc := newService(t)
	u, _ := user.New("a@b.c", "h", user.RoleAdmin)

	token, _, err := svc.GenerateRefreshToken(u)
	require.NoError(t, err)

	claims, err := svc.Parse(token)
	require.NoError(t, err)
	assert.Equal(t, ports.TokenTypeRefresh, claims.TokenType)
}

func TestParse_RejectsGarbage(t *testing.T) {
	svc := newService(t)
	_, err := svc.Parse("not.a.token")
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestParse_RejectsTokenFromAnotherKey(t *testing.T) {
	svc1 := newService(t)
	svc2 := newService(t)
	u, _ := user.New("a@b.c", "h", user.RoleAdmin)

	token, _, _ := svc1.GenerateAccessToken(u, nil)
	_, err := svc2.Parse(token) // different key
	assert.ErrorIs(t, err, auth.ErrInvalidToken)
}

func TestLoadKey_PEMRoundTrip(t *testing.T) {
	genKey, _, err := auth.LoadOrGenerateKey("")
	require.NoError(t, err)

	pemBytes, err := auth.MarshalPrivateKeyPEM(genKey)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "ed25519.pem")
	require.NoError(t, os.WriteFile(path, pemBytes, 0o600))

	loaded, generated, err := auth.LoadOrGenerateKey(path)
	require.NoError(t, err)
	assert.False(t, generated, "loading from file must not generate")
	assert.Equal(t, genKey, loaded)
}

func TestLoadKey_RejectsWrongPEMType(t *testing.T) {
	path := filepath.Join(t.TempDir(), "bad.pem")
	require.NoError(t, os.WriteFile(path, []byte("-----BEGIN RSA PRIVATE KEY-----\nZm9v\n-----END RSA PRIVATE KEY-----\n"), 0o600))

	_, _, err := auth.LoadOrGenerateKey(path)
	assert.Error(t, err)
}

func TestAccessTTL_CappedAt15Min(t *testing.T) {
	key, _, _ := auth.LoadOrGenerateKey("")
	svc := auth.NewTokenService(auth.Config{
		PrivateKey: key,
		AccessTTL:  24 * time.Hour, // request more than allowed
	})
	u, _ := user.New("a@b.c", "h", user.RoleAdmin)

	_, exp, err := svc.GenerateAccessToken(u, nil)
	require.NoError(t, err)
	assert.LessOrEqual(t, time.Until(exp), 15*time.Minute+time.Second)
}
