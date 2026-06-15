package application_test

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/user"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/clock"
	"github.com/open226bf/hivemind/pkg/crypto"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// ─── Fakes ────────────────────────────────────────────────────────────────────

type fakeUserRepo struct {
	byEmail map[string]*user.User
	byID    map[uuid.UUID]*user.User
	updated int
}

func newFakeUserRepo() *fakeUserRepo {
	return &fakeUserRepo{byEmail: map[string]*user.User{}, byID: map[uuid.UUID]*user.User{}}
}

func (r *fakeUserRepo) add(u *user.User) {
	r.byEmail[u.Email] = u
	r.byID[u.ID] = u
}

func (r *fakeUserRepo) Save(_ context.Context, u *user.User) error { r.add(u); return nil }

func (r *fakeUserRepo) FindByID(_ context.Context, id uuid.UUID) (*user.User, error) {
	if u, ok := r.byID[id]; ok {
		return u, nil
	}
	return nil, domainerrors.ErrNotFound
}

func (r *fakeUserRepo) FindByEmail(_ context.Context, email string) (*user.User, error) {
	if u, ok := r.byEmail[email]; ok {
		return u, nil
	}
	return nil, domainerrors.ErrNotFound
}

func (r *fakeUserRepo) Update(_ context.Context, u *user.User) error {
	r.add(u)
	r.updated++
	return nil
}

func (r *fakeUserRepo) List(context.Context, pagination.Page) ([]*user.User, int64, error) {
	return nil, 0, nil
}
func (r *fakeUserRepo) Delete(context.Context, uuid.UUID) error { return nil }

func (r *fakeUserRepo) CountActiveAdmins(context.Context) (int64, error) {
	var n int64
	for _, u := range r.byID {
		if u.Role == user.RoleAdmin && u.Active {
			n++
		}
	}
	return n, nil
}

// fakeTokens returns deterministic tokens encoding the user id + type.
type fakeTokens struct{}

func (fakeTokens) GenerateAccessToken(u *user.User) (string, time.Time, error) {
	return "access:" + u.ID.String(), time.Now().Add(time.Minute), nil
}
func (fakeTokens) GenerateRefreshToken(u *user.User) (string, time.Time, error) {
	return "refresh:" + u.ID.String(), time.Now().Add(time.Hour), nil
}
func (fakeTokens) Parse(s string) (*ports.TokenClaims, error) {
	// format: "<type>:<uuid>"
	if len(s) < 8 {
		return nil, application.ErrInvalidToken
	}
	var typ ports.TokenType
	var raw string
	switch {
	case s[:7] == "access:":
		typ, raw = ports.TokenTypeAccess, s[7:]
	case s[:8] == "refresh:":
		typ, raw = ports.TokenTypeRefresh, s[8:]
	default:
		return nil, application.ErrInvalidToken
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, application.ErrInvalidToken
	}
	return &ports.TokenClaims{UserID: id, TokenType: typ}, nil
}

// ─── Helpers ──────────────────────────────────────────────────────────────────

func mkUser(t *testing.T, email, password string, role user.Role) *user.User {
	t.Helper()
	hash, err := crypto.HashPassword(password)
	require.NoError(t, err)
	u, err := user.New(email, hash, role)
	require.NoError(t, err)
	return u
}

func fixedClock() ports.Clock {
	return clock.Fixed{T: time.Date(2026, 6, 12, 12, 0, 0, 0, time.UTC)}
}

// ─── Tests ────────────────────────────────────────────────────────────────────

func TestLogin_Success(t *testing.T) {
	repo := newFakeUserRepo()
	repo.add(mkUser(t, "a@b.c", "secret123", user.RoleOperator))
	svc := application.NewAuthService(repo, fakeTokens{}, fixedClock())

	pair, err := svc.Login(context.Background(), "a@b.c", "secret123")
	require.NoError(t, err)
	assert.NotEmpty(t, pair.AccessToken)
	assert.NotEmpty(t, pair.RefreshToken)
	assert.Equal(t, "Bearer", pair.TokenType)
}

func TestLogin_WrongPassword(t *testing.T) {
	repo := newFakeUserRepo()
	repo.add(mkUser(t, "a@b.c", "secret123", user.RoleOperator))
	svc := application.NewAuthService(repo, fakeTokens{}, fixedClock())

	_, err := svc.Login(context.Background(), "a@b.c", "wrong")
	assert.ErrorIs(t, err, application.ErrInvalidCredentials)
}

func TestLogin_UnknownEmail_DoesNotLeak(t *testing.T) {
	svc := application.NewAuthService(newFakeUserRepo(), fakeTokens{}, fixedClock())
	_, err := svc.Login(context.Background(), "ghost@b.c", "whatever")
	assert.ErrorIs(t, err, application.ErrInvalidCredentials)
}

func TestLogin_LocksAfterFiveFailures(t *testing.T) {
	repo := newFakeUserRepo()
	repo.add(mkUser(t, "a@b.c", "secret123", user.RoleOperator))
	svc := application.NewAuthService(repo, fakeTokens{}, fixedClock())

	for i := 0; i < user.MaxFailedLogins; i++ {
		_, _ = svc.Login(context.Background(), "a@b.c", "wrong")
	}
	// Even with the correct password, the account is now locked.
	_, err := svc.Login(context.Background(), "a@b.c", "secret123")
	assert.ErrorIs(t, err, application.ErrAccountLocked)
}

func TestLogin_InactiveUser(t *testing.T) {
	repo := newFakeUserRepo()
	u := mkUser(t, "a@b.c", "secret123", user.RoleOperator)
	u.Active = false
	repo.add(u)
	svc := application.NewAuthService(repo, fakeTokens{}, fixedClock())

	_, err := svc.Login(context.Background(), "a@b.c", "secret123")
	assert.ErrorIs(t, err, application.ErrInactiveUser)
}

func TestRefresh_Success(t *testing.T) {
	repo := newFakeUserRepo()
	u := mkUser(t, "a@b.c", "secret123", user.RoleOperator)
	repo.add(u)
	svc := application.NewAuthService(repo, fakeTokens{}, fixedClock())

	pair, err := svc.Refresh(context.Background(), "refresh:"+u.ID.String())
	require.NoError(t, err)
	assert.NotEmpty(t, pair.AccessToken)
}

func TestRefresh_RejectsAccessToken(t *testing.T) {
	repo := newFakeUserRepo()
	u := mkUser(t, "a@b.c", "secret123", user.RoleOperator)
	repo.add(u)
	svc := application.NewAuthService(repo, fakeTokens{}, fixedClock())

	_, err := svc.Refresh(context.Background(), "access:"+u.ID.String())
	assert.ErrorIs(t, err, application.ErrInvalidToken)
}

func TestEnsureAdmin_CreatesThenIdempotent(t *testing.T) {
	repo := newFakeUserRepo()

	created, err := application.EnsureAdmin(context.Background(), repo, "admin@b.c", "pw")
	require.NoError(t, err)
	assert.True(t, created)

	created2, err := application.EnsureAdmin(context.Background(), repo, "admin@b.c", "pw")
	require.NoError(t, err)
	assert.False(t, created2, "second call must be a no-op")
}
