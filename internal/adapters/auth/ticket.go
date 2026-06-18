package auth

import (
	"crypto/rand"
	"encoding/base64"
	"sync"
	"time"

	"github.com/google/uuid"
)

// WSTicket is a short-lived, single-use credential authorising one WebSocket
// upgrade. Browsers cannot set an Authorization header on a WebSocket, so an
// access token would otherwise travel in the URL (and into access/proxy logs).
// The client exchanges its bearer token for a ticket over a normal request, then
// opens the socket with the opaque ticket id: a short TTL plus single use bound
// the damage if the URL is ever logged, and the long-lived access token never
// leaves a header.
type WSTicket struct {
	UserID    uuid.UUID
	ServiceID uuid.UUID
}

type ticketEntry struct {
	WSTicket
	expiresAt time.Time
}

// TicketStore issues and redeems WebSocket tickets in memory — single-instance,
// consistent with the agent hub and presence registry.
type TicketStore struct {
	ttl     time.Duration
	mu      sync.Mutex
	entries map[string]ticketEntry
}

// NewTicketStore builds a store whose tickets live for ttl (default 30s).
func NewTicketStore(ttl time.Duration) *TicketStore {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &TicketStore{ttl: ttl, entries: make(map[string]ticketEntry)}
}

// Issue mints a ticket and returns its opaque id and TTL in seconds. Expired
// tickets are swept opportunistically so an unconsumed backlog cannot grow.
func (s *TicketStore) Issue(t WSTicket) (id string, ttlSeconds int) {
	id = randomTicketID()
	s.mu.Lock()
	s.sweep(time.Now())
	s.entries[id] = ticketEntry{WSTicket: t, expiresAt: time.Now().Add(s.ttl)}
	s.mu.Unlock()
	return id, int(s.ttl.Seconds())
}

// Consume redeems a ticket exactly once. It returns false if the id is unknown,
// already used, or expired.
func (s *TicketStore) Consume(id string) (WSTicket, bool) {
	if id == "" {
		return WSTicket{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	e, ok := s.entries[id]
	if !ok {
		return WSTicket{}, false
	}
	delete(s.entries, id) // single use, even when expired
	if time.Now().After(e.expiresAt) {
		return WSTicket{}, false
	}
	return e.WSTicket, true
}

// sweep removes expired entries. Caller must hold s.mu.
func (s *TicketStore) sweep(now time.Time) {
	for id, e := range s.entries {
		if now.After(e.expiresAt) {
			delete(s.entries, id)
		}
	}
}

func randomTicketID() string {
	b := make([]byte, 32)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
