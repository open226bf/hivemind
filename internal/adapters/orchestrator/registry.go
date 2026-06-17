package orchestrator

import (
	"context"
	"fmt"
	"sync"

	"github.com/google/uuid"

	"github.com/orange/hivemind/internal/domain/cluster"
	"github.com/orange/hivemind/internal/ports"
)

// closer is the optional cleanup an orchestrator may expose (SwarmOrchestrator
// holds a Docker client that must be closed on invalidation).
type closer interface{ Close() error }

// Registry resolves a cluster id to a live Orchestrator and caches the
// connection. It is the single component aware that Hivemind is multi-cluster:
// it reads the cluster definition from the repository, dispatches on its Type to
// build the matching backend, and reuses the result on later lookups. The zero
// UUID resolves to the default cluster.
type Registry struct {
	clusters ports.ClusterRepository

	mu    sync.Mutex
	cache map[uuid.UUID]ports.Orchestrator
}

// NewRegistry builds a registry backed by the cluster repository.
func NewRegistry(clusters ports.ClusterRepository) *Registry {
	return &Registry{
		clusters: clusters,
		cache:    make(map[uuid.UUID]ports.Orchestrator),
	}
}

// For returns the orchestrator for a cluster, building and caching it on first
// use. The zero UUID resolves to the default cluster.
func (r *Registry) For(ctx context.Context, clusterID uuid.UUID) (ports.Orchestrator, error) {
	if clusterID == uuid.Nil {
		return r.Default(ctx)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if orch, ok := r.cache[clusterID]; ok {
		return orch, nil
	}

	c, err := r.clusters.FindByID(ctx, clusterID)
	if err != nil {
		return nil, fmt.Errorf("resolve cluster %s: %w", clusterID, err)
	}
	return r.build(ctx, c)
}

// Default resolves the cluster flagged as default.
func (r *Registry) Default(ctx context.Context) (ports.Orchestrator, error) {
	c, err := r.clusters.FindDefault(ctx)
	if err != nil {
		return nil, fmt.Errorf("resolve default cluster: %w", err)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if orch, ok := r.cache[c.ID]; ok {
		return orch, nil
	}
	return r.build(ctx, c)
}

// Invalidate drops and closes the cached connection for a cluster.
func (r *Registry) Invalidate(clusterID uuid.UUID) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if orch, ok := r.cache[clusterID]; ok {
		if c, ok := orch.(closer); ok {
			_ = c.Close()
		}
		delete(r.cache, clusterID)
	}
}

// build constructs the orchestrator for a cluster according to its type and
// caches it. The caller must hold r.mu.
func (r *Registry) build(ctx context.Context, c *cluster.Cluster) (ports.Orchestrator, error) {
	switch c.Type {
	case cluster.TypeSwarm:
		orch, err := NewSwarmOrchestratorFromSpec(ctx, ConnSpec{
			Host:       c.Endpoint,
			CACert:     []byte(c.TLS.CACert),
			ClientCert: []byte(c.TLS.ClientCert),
			ClientKey:  []byte(c.TLS.ClientKey),
		})
		if err != nil {
			return nil, fmt.Errorf("connect cluster %q: %w", c.Name, err)
		}
		r.cache[c.ID] = orch
		return orch, nil
	default:
		return nil, fmt.Errorf("unsupported cluster type %q", c.Type)
	}
}

// StaticRegistry serves a single orchestrator for every cluster id. It backs the
// stub deployment mode (ORCHESTRATOR=stub) and unit tests, where there is one
// simulated backend and no real per-cluster connections to manage.
type StaticRegistry struct {
	orch ports.Orchestrator
}

// NewStaticRegistry wraps a single orchestrator. A nil orchestrator yields a
// registry whose lookups fail, mirroring the "no orchestrator configured" path.
func NewStaticRegistry(orch ports.Orchestrator) *StaticRegistry {
	return &StaticRegistry{orch: orch}
}

func (s *StaticRegistry) For(context.Context, uuid.UUID) (ports.Orchestrator, error) {
	if s.orch == nil {
		return nil, fmt.Errorf("orchestrator not configured")
	}
	return s.orch, nil
}

func (s *StaticRegistry) Default(ctx context.Context) (ports.Orchestrator, error) {
	return s.For(ctx, uuid.Nil)
}

func (s *StaticRegistry) Invalidate(uuid.UUID) {}
