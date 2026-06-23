package acl_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/open226bf/hivemind/internal/domain/acl"
)

func TestVerb_RankAndAtLeast(t *testing.T) {
	assert.Equal(t, 1, acl.VerbRead.Rank())
	assert.Equal(t, 2, acl.VerbWrite.Rank())
	assert.Equal(t, 3, acl.VerbManage.Rank())
	assert.Equal(t, 0, acl.Verb("bogus").Rank())

	assert.True(t, acl.VerbManage.AtLeast(acl.VerbWrite))
	assert.True(t, acl.VerbWrite.AtLeast(acl.VerbWrite))
	assert.False(t, acl.VerbRead.AtLeast(acl.VerbWrite))
	// An unknown verb never satisfies a real minimum.
	assert.False(t, acl.Verb("bogus").AtLeast(acl.VerbRead))
}

func TestMaxVerb(t *testing.T) {
	assert.Equal(t, acl.VerbManage, acl.MaxVerb(acl.VerbRead, acl.VerbManage))
	assert.Equal(t, acl.VerbWrite, acl.MaxVerb(acl.VerbWrite, acl.VerbRead))
	assert.Equal(t, acl.VerbRead, acl.MaxVerb(acl.VerbRead, acl.VerbRead))
}

func TestResourceType_IsValid(t *testing.T) {
	assert.True(t, acl.ResourceCluster.IsValid())
	assert.True(t, acl.ResourceHive.IsValid())
	assert.False(t, acl.ResourceType("service").IsValid())
}

func TestNewGrant_Validation(t *testing.T) {
	now := time.Now()
	subject := uuid.New()
	resource := uuid.New()
	creator := uuid.New()

	t.Run("ok", func(t *testing.T) {
		g, err := acl.NewGrant(subject, acl.ResourceHive, resource, acl.VerbWrite, creator, nil, now)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, g.ID)
		assert.Equal(t, subject, g.SubjectID)
		assert.Equal(t, acl.VerbWrite, g.Verb)
	})

	t.Run("nil subject", func(t *testing.T) {
		_, err := acl.NewGrant(uuid.Nil, acl.ResourceHive, resource, acl.VerbWrite, creator, nil, now)
		assert.ErrorIs(t, err, acl.ErrInvalidSubject)
	})

	t.Run("bad resource type", func(t *testing.T) {
		_, err := acl.NewGrant(subject, acl.ResourceType("nope"), resource, acl.VerbWrite, creator, nil, now)
		assert.ErrorIs(t, err, acl.ErrInvalidResourceType)
	})

	t.Run("nil resource id", func(t *testing.T) {
		_, err := acl.NewGrant(subject, acl.ResourceHive, uuid.Nil, acl.VerbWrite, creator, nil, now)
		assert.ErrorIs(t, err, acl.ErrInvalidResource)
	})

	t.Run("bad verb", func(t *testing.T) {
		_, err := acl.NewGrant(subject, acl.ResourceHive, resource, acl.Verb("x"), creator, nil, now)
		assert.ErrorIs(t, err, acl.ErrInvalidVerb)
	})
}

func TestGrant_Active(t *testing.T) {
	now := time.Now()
	g := &acl.Grant{}
	assert.True(t, g.Active(now), "no expiry → always active")

	past := now.Add(-time.Hour)
	g.ExpiresAt = &past
	assert.False(t, g.Active(now))

	future := now.Add(time.Hour)
	g.ExpiresAt = &future
	assert.True(t, g.Active(now))
}
