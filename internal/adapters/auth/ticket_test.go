package auth_test

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/orange/hivemind/internal/adapters/auth"
)

func TestTicket_IssueThenConsumeOnce(t *testing.T) {
	store := auth.NewTicketStore(time.Minute)
	want := auth.WSTicket{UserID: uuid.New(), ServiceID: uuid.New()}

	id, ttl := store.Issue(want)
	require.NotEmpty(t, id)
	assert.Equal(t, 60, ttl)

	got, ok := store.Consume(id)
	require.True(t, ok)
	assert.Equal(t, want, got)

	// Single use: a second redemption fails.
	_, ok = store.Consume(id)
	assert.False(t, ok, "a ticket must not be redeemable twice")
}

func TestTicket_UnknownAndEmpty(t *testing.T) {
	store := auth.NewTicketStore(time.Minute)
	_, ok := store.Consume("does-not-exist")
	assert.False(t, ok)
	_, ok = store.Consume("")
	assert.False(t, ok)
}

func TestTicket_Expires(t *testing.T) {
	store := auth.NewTicketStore(10 * time.Millisecond)
	id, _ := store.Issue(auth.WSTicket{ServiceID: uuid.New()})
	time.Sleep(25 * time.Millisecond)
	_, ok := store.Consume(id)
	assert.False(t, ok, "an expired ticket must not be redeemable")
}

func TestTicket_DefaultTTL(t *testing.T) {
	store := auth.NewTicketStore(0)
	_, ttl := store.Issue(auth.WSTicket{})
	assert.Equal(t, 30, ttl, "zero TTL falls back to 30s")
}
