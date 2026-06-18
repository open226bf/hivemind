package application

import (
	"context"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/domain/monitoring"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// AlertEngine evaluates the event-driven monitoring rules against each cluster's
// health snapshot and maintains the set of firing alerts. It is the "alert
// management" core: it dedups (one alert per ongoing condition), fires new ones
// and resolves recovered ones through the AlertRouter, and exposes the active
// set for the API. It needs no time-series store — that is phase 2 (CPU/mem
// thresholds). See docs/adr/0002-monitoring-and-alerting.md.
type AlertEngine struct {
	collectors ports.TelemetryCollectorRegistry
	clusters   ports.ClusterRepository
	router     ports.AlertRouter
	log        *slog.Logger
	now        func() time.Time

	mu     sync.Mutex
	active map[string]monitoring.Alert // key: "<clusterID>/<findingKey>"
}

// NewAlertEngine builds the engine. router receives fire/resolve notifications.
func NewAlertEngine(collectors ports.TelemetryCollectorRegistry, clusters ports.ClusterRepository, router ports.AlertRouter, log *slog.Logger) *AlertEngine {
	if log == nil {
		log = slog.Default()
	}
	return &AlertEngine{
		collectors: collectors,
		clusters:   clusters,
		router:     router,
		log:        log,
		now:        time.Now,
		active:     make(map[string]monitoring.Alert),
	}
}

// Run reconciles every cluster on a ticker until ctx is cancelled. Clusters that
// cannot provide telemetry (stub, or agent-mode before the agent collector) are
// skipped quietly.
func (e *AlertEngine) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	e.reconcileAll(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			e.reconcileAll(ctx)
		}
	}
}

func (e *AlertEngine) reconcileAll(ctx context.Context) {
	clusters, _, err := e.clusters.List(ctx, pagination.New(1, pagination.MaxLimit))
	if err != nil {
		e.log.Warn("alert engine: list clusters", "err", err)
		return
	}
	for _, c := range clusters {
		if err := e.ReconcileCluster(ctx, c.ID); err != nil {
			// Telemetry-unsupported / unreachable clusters are expected; debug only.
			e.log.Debug("alert engine: reconcile cluster", "cluster", c.Name, "err", err)
		}
	}
}

// ReconcileCluster collects one cluster's health, evaluates findings, then fires
// new alerts and resolves recovered ones. Idempotent — safe to call repeatedly.
func (e *AlertEngine) ReconcileCluster(ctx context.Context, clusterID uuid.UUID) error {
	col, err := e.collectors.For(ctx, clusterID)
	if err != nil {
		return err
	}
	h, err := col.CollectHealth(ctx)
	if err != nil {
		return err
	}
	e.apply(clusterID, monitoring.Evaluate(*h))
	return nil
}

func (e *AlertEngine) apply(clusterID uuid.UUID, findings []monitoring.Finding) {
	prefix := clusterID.String() + "/"

	e.mu.Lock()
	defer e.mu.Unlock()

	seen := make(map[string]bool, len(findings))
	for _, f := range findings {
		key := prefix + f.Key
		seen[key] = true
		if _, firing := e.active[key]; firing {
			continue // already alerting on this condition
		}
		a := monitoring.Alert{
			ID:          uuid.New(),
			State:       monitoring.AlertFiring,
			Severity:    f.Severity,
			ClusterID:   clusterID,
			ServiceID:   ptrIfSet(f.ServiceID),
			NodeID:      f.NodeID,
			ContainerID: f.ContainerID,
			Summary:     f.Summary,
			Detail:      f.Detail,
			FiredAt:     e.now(),
			Labels:      map[string]string{"kind": string(f.Kind)},
		}
		e.active[key] = a
		e.route(a)
	}

	// Resolve alerts of THIS cluster whose condition is gone.
	for key, a := range e.active {
		if len(key) < len(prefix) || key[:len(prefix)] != prefix || seen[key] {
			continue
		}
		a.State = monitoring.AlertResolved
		t := e.now()
		a.ResolvedAt = &t
		e.route(a)
		delete(e.active, key)
	}
}

func (e *AlertEngine) route(a monitoring.Alert) {
	if e.router == nil {
		return
	}
	if err := e.router.Route(context.Background(), a); err != nil {
		e.log.Warn("alert engine: route alert", "alert", a.Summary, "err", err)
	}
}

// ActiveAlerts returns a snapshot of the currently-firing alerts, newest first.
func (e *AlertEngine) ActiveAlerts() []monitoring.Alert {
	e.mu.Lock()
	defer e.mu.Unlock()

	out := make([]monitoring.Alert, 0, len(e.active))
	for _, a := range e.active {
		out = append(out, a)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].FiredAt.After(out[j].FiredAt) })
	return out
}

func ptrIfSet(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}
