package application

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/domain/snapshot"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// Brownfield-discovery classes (ADR 0004). A live Swarm service is classified by
// cross-referencing its hivemind.service.id label against the persisted services
// of the same cluster.
const (
	// ClassManaged: the service carries a hivemind.service.id label that resolves
	// to an existing Service record — a first-class Hivemind service.
	ClassManaged = "managed"
	// ClassForeign: no hivemind.service.id label — created out-of-band and never
	// adopted (e.g. `docker service create`, `docker stack deploy`).
	ClassForeign = "foreign"
	// ClassOrphan: a hivemind.service.id label is present but resolves to no known
	// Service record (the record was deleted, or belongs to another cluster).
	ClassOrphan = "orphan"
)

// DiscoveredService is one live Swarm service as seen by brownfield discovery,
// annotated with its ownership class. ServiceID and HiveID are set only for
// managed services (the matching Hivemind record).
type DiscoveredService struct {
	SwarmServiceID string
	Name           string
	Image          string
	Replicas       uint64
	Class          string
	ServiceID      *uuid.UUID
	HiveID         *uuid.UUID
	CreatedAt      time.Time
}

// SnapshotCapturer records a point-in-time snapshot of a service. It is the
// subset of SnapshotService that adoption needs, kept as an interface so the
// dependency stays optional (nil = skip) and testable.
type SnapshotCapturer interface {
	Capture(ctx context.Context, serviceID uuid.UUID, label string, userID *uuid.UUID) (*snapshot.ServiceSnapshot, error)
}

// DiscoveryService merges the live Swarm service inventory with Hivemind's
// persisted services and classifies each as managed / foreign / orphan, so
// operators can see and adopt services that already run on a cluster (ADR 0004).
// Discovery is read-only; adoption/release write the DB and seal/clear the
// hivemind.service.id label on the live service.
type DiscoveryService struct {
	registry  ports.OrchestratorRegistry
	services  ports.ServiceRepository
	snapshots SnapshotCapturer // optional: nil skips the initial adoption snapshot
}

func NewDiscoveryService(registry ports.OrchestratorRegistry, services ports.ServiceRepository, snapshots SnapshotCapturer) *DiscoveryService {
	return &DiscoveryService{registry: registry, services: services, snapshots: snapshots}
}

// orchFor resolves the live orchestrator for a cluster, mapping a nil registry
// or an unreachable backend to ErrOrchestratorUnavailable.
func (s *DiscoveryService) orchFor(ctx context.Context, clusterID uuid.UUID) (ports.Orchestrator, error) {
	if s.registry == nil {
		return nil, ErrOrchestratorUnavailable
	}
	orch, err := s.registry.For(ctx, clusterID)
	if err != nil || orch == nil {
		return nil, ErrOrchestratorUnavailable
	}
	return orch, nil
}

// Discover lists every Swarm service on the cluster and classifies it. The
// returned slice mirrors the live cluster order. A nil registry or an
// unreachable orchestrator surfaces as ErrOrchestratorUnavailable.
func (s *DiscoveryService) Discover(ctx context.Context, clusterID uuid.UUID) ([]DiscoveredService, error) {
	orch, err := s.orchFor(ctx, clusterID)
	if err != nil {
		return nil, err
	}

	live, err := orch.ListServices(ctx)
	if err != nil {
		return nil, fmt.Errorf("list swarm services: %w", err)
	}

	// Index the cluster's persisted services by their id (the value carried in
	// the hivemind.service.id label). A high page size fetches them in one shot,
	// matching the existing internal-listing convention (see HiveService.List).
	known, err := s.knownByID(ctx, clusterID)
	if err != nil {
		return nil, err
	}

	out := make([]DiscoveredService, 0, len(live))
	for _, l := range live {
		d := DiscoveredService{
			SwarmServiceID: l.SwarmServiceID,
			Name:           l.Name,
			Image:          l.Image,
			Replicas:       l.Replicas,
			CreatedAt:      l.CreatedAt,
		}
		if l.HivemindLabel == "" {
			d.Class = ClassForeign
		} else if rec, ok := known[l.HivemindLabel]; ok {
			d.Class = ClassManaged
			id := rec.id
			d.ServiceID = &id
			d.HiveID = rec.hiveID
		} else {
			d.Class = ClassOrphan
		}
		out = append(out, d)
	}
	return out, nil
}

type knownService struct {
	id     uuid.UUID
	hiveID *uuid.UUID
}

// pageAll fetches an internal listing in one shot, matching the existing
// convention for non-paginated internal reads (see HiveService.List).
func pageAll() pagination.Page { return pagination.Page{Number: 1, Size: 1000} }

func (s *DiscoveryService) knownByID(ctx context.Context, clusterID uuid.UUID) (map[string]knownService, error) {
	filter := ports.ServiceFilter{ClusterID: &clusterID}
	items, _, err := s.services.List(ctx, filter, pageAll())
	if err != nil {
		return nil, fmt.Errorf("list known services: %w", err)
	}
	known := make(map[string]knownService, len(items))
	for _, svc := range items {
		known[svc.ID.String()] = knownService{id: svc.ID, hiveID: svc.HiveID}
	}
	return known, nil
}
