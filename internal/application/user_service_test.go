package application_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/user"
	"github.com/open226bf/hivemind/pkg/crypto"
)

func makeUser(t *testing.T, email string, role user.Role) *user.User {
	t.Helper()
	hash, err := crypto.HashPassword("password123")
	require.NoError(t, err)
	u, err := user.New(email, hash, role)
	require.NoError(t, err)
	return u
}

func TestUserService_Create(t *testing.T) {
	ctx := context.Background()

	t.Run("rejects short password", func(t *testing.T) {
		svc := application.NewUserService(newFakeUserRepo())
		_, err := svc.Create(ctx, application.CreateUserInput{Email: "a@b.c", Password: "short", Role: user.RoleViewer})
		assert.ErrorIs(t, err, application.ErrWeakPassword)
	})

	t.Run("rejects invalid role", func(t *testing.T) {
		svc := application.NewUserService(newFakeUserRepo())
		_, err := svc.Create(ctx, application.CreateUserInput{Email: "a@b.c", Password: "password123", Role: "root"})
		assert.ErrorIs(t, err, user.ErrInvalidRole)
	})

	t.Run("rejects duplicate email", func(t *testing.T) {
		repo := newFakeUserRepo()
		repo.add(makeUser(t, "dev@b.c", user.RoleOperator))
		svc := application.NewUserService(repo)
		_, err := svc.Create(ctx, application.CreateUserInput{Email: "dev@b.c", Password: "password123", Role: user.RoleViewer})
		assert.Error(t, err)
	})

	t.Run("creates and hashes password", func(t *testing.T) {
		svc := application.NewUserService(newFakeUserRepo())
		u, err := svc.Create(ctx, application.CreateUserInput{Email: "New@B.C", Password: "password123", Role: user.RoleOperator})
		require.NoError(t, err)
		assert.Equal(t, "new@b.c", u.Email, "email is normalised to lowercase")
		assert.NotEqual(t, "password123", u.PasswordHash)
		assert.NoError(t, crypto.CheckPassword(u.PasswordHash, "password123"))
	})
}

func TestUserService_Delete(t *testing.T) {
	ctx := context.Background()

	t.Run("cannot delete own account", func(t *testing.T) {
		repo := newFakeUserRepo()
		admin := makeUser(t, "admin@b.c", user.RoleAdmin)
		repo.add(admin)
		svc := application.NewUserService(repo)
		err := svc.Delete(ctx, admin.ID, admin.ID)
		assert.ErrorIs(t, err, application.ErrSelfDelete)
	})

	t.Run("cannot delete the last admin", func(t *testing.T) {
		repo := newFakeUserRepo()
		admin := makeUser(t, "admin@b.c", user.RoleAdmin)
		other := makeUser(t, "op@b.c", user.RoleOperator)
		repo.add(admin)
		repo.add(other)
		svc := application.NewUserService(repo)
		err := svc.Delete(ctx, other.ID, admin.ID)
		assert.ErrorIs(t, err, application.ErrLastAdmin)
	})

	t.Run("can delete an admin when another remains", func(t *testing.T) {
		repo := newFakeUserRepo()
		a1 := makeUser(t, "a1@b.c", user.RoleAdmin)
		a2 := makeUser(t, "a2@b.c", user.RoleAdmin)
		repo.add(a1)
		repo.add(a2)
		svc := application.NewUserService(repo)
		err := svc.Delete(ctx, a2.ID, a1.ID)
		assert.NoError(t, err)
	})
}

func TestUserService_Update(t *testing.T) {
	ctx := context.Background()
	viewer := user.RoleViewer

	t.Run("cannot demote yourself", func(t *testing.T) {
		repo := newFakeUserRepo()
		a1 := makeUser(t, "a1@b.c", user.RoleAdmin)
		a2 := makeUser(t, "a2@b.c", user.RoleAdmin)
		repo.add(a1)
		repo.add(a2)
		svc := application.NewUserService(repo)
		_, err := svc.Update(ctx, a1.ID, a1.ID, application.UpdateUserInput{Role: &viewer})
		assert.ErrorIs(t, err, application.ErrSelfDemote)
	})

	t.Run("cannot demote the last admin", func(t *testing.T) {
		repo := newFakeUserRepo()
		admin := makeUser(t, "admin@b.c", user.RoleAdmin)
		other := makeUser(t, "op@b.c", user.RoleOperator)
		repo.add(admin)
		repo.add(other)
		svc := application.NewUserService(repo)
		_, err := svc.Update(ctx, other.ID, admin.ID, application.UpdateUserInput{Role: &viewer})
		assert.ErrorIs(t, err, application.ErrLastAdmin)
	})

	t.Run("can demote an admin when another remains", func(t *testing.T) {
		repo := newFakeUserRepo()
		a1 := makeUser(t, "a1@b.c", user.RoleAdmin)
		a2 := makeUser(t, "a2@b.c", user.RoleAdmin)
		repo.add(a1)
		repo.add(a2)
		svc := application.NewUserService(repo)
		updated, err := svc.Update(ctx, a2.ID, a1.ID, application.UpdateUserInput{Role: &viewer})
		require.NoError(t, err)
		assert.Equal(t, user.RoleViewer, updated.Role)
	})
}
