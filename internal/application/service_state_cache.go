package application

import (
	"sync"
	"time"

	"github.com/orange/hivemind/internal/ports"
)

// serviceStateTTL bounds how stale a cached orchestrator state may be. The
// cahier des charges (F-MVP-10) allows a short cache with TTL <= 5s so that
// supervising 200 services stays under the 2s response budget and back-to-back
// /status and /tasks calls hit Swarm at most once.
const serviceStateTTL = 5 * time.Second

// stateCache is a tiny TTL cache of orchestrator service states keyed by the
// Swarm service ID. It is safe for concurrent use.
type stateCache struct {
	mu      sync.Mutex
	ttl     time.Duration
	now     func() time.Time
	entries map[string]stateCacheEntry
}

type stateCacheEntry struct {
	state     *ports.ServiceState
	expiresAt time.Time
}

func newStateCache(ttl time.Duration) *stateCache {
	return &stateCache{
		ttl:     ttl,
		now:     time.Now,
		entries: make(map[string]stateCacheEntry),
	}
}

// get returns a cached, non-expired state for key, or (nil, false) on a miss.
func (c *stateCache) get(key string) (*ports.ServiceState, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if !ok || c.now().After(e.expiresAt) {
		return nil, false
	}
	return e.state, true
}

// put stores state for key with the cache TTL.
func (c *stateCache) put(key string, state *ports.ServiceState) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.entries[key] = stateCacheEntry{state: state, expiresAt: c.now().Add(c.ttl)}
}
