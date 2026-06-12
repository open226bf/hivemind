package user_test

import (
	"testing"
	"time"

	"github.com/orange/hivemind/internal/domain/user"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew_InvalidRole(t *testing.T) {
	_, err := user.New("a@b.c", "hash", user.Role("superuser"))
	assert.ErrorIs(t, err, user.ErrInvalidRole)
}

func TestRoleHierarchy(t *testing.T) {
	admin, _ := user.New("a@b.c", "h", user.RoleAdmin)
	op, _ := user.New("o@b.c", "h", user.RoleOperator)
	viewer, _ := user.New("v@b.c", "h", user.RoleViewer)

	assert.True(t, admin.IsAdmin())
	assert.True(t, admin.IsOperator())
	assert.False(t, op.IsAdmin())
	assert.True(t, op.IsOperator())
	assert.False(t, viewer.IsOperator())
}

func TestLockout_AfterMaxFailures(t *testing.T) {
	u, _ := user.New("a@b.c", "h", user.RoleViewer)
	now := time.Now().UTC()

	for i := 0; i < user.MaxFailedLogins-1; i++ {
		u.RecordFailedLogin(now)
		require.False(t, u.IsLocked(now), "should not lock before threshold (attempt %d)", i+1)
	}

	u.RecordFailedLogin(now) // 5th failure
	assert.True(t, u.IsLocked(now))
	assert.Equal(t, user.MaxFailedLogins, u.FailedLoginAttempts)
}

func TestLockout_Expires(t *testing.T) {
	u, _ := user.New("a@b.c", "h", user.RoleViewer)
	now := time.Now().UTC()
	for i := 0; i < user.MaxFailedLogins; i++ {
		u.RecordFailedLogin(now)
	}
	require.True(t, u.IsLocked(now))

	later := now.Add(user.LockoutDuration + time.Second)
	assert.False(t, u.IsLocked(later), "lock should expire after lockout duration")
}

func TestResetFailedLogins(t *testing.T) {
	u, _ := user.New("a@b.c", "h", user.RoleViewer)
	now := time.Now().UTC()
	for i := 0; i < user.MaxFailedLogins; i++ {
		u.RecordFailedLogin(now)
	}
	require.True(t, u.IsLocked(now))

	u.ResetFailedLogins()
	assert.Equal(t, 0, u.FailedLoginAttempts)
	assert.Nil(t, u.LockedUntil)
	assert.False(t, u.IsLocked(now))
}
