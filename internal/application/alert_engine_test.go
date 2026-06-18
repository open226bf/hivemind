package application_test

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/monitoring"
	"github.com/open226bf/hivemind/internal/ports"
)

// swCollector returns whatever ClusterHealth its pointer currently holds, so a
// test can flip a cluster from broken to healthy between reconciles.
type swCollector struct{ h *monitoring.ClusterHealth }

func (c swCollector) CollectHealth(context.Context) (*monitoring.ClusterHealth, error) {
	return c.h, nil
}
func (swCollector) StreamMetrics(context.Context, ports.MetricStreamOptions) (<-chan monitoring.MetricSample, error) {
	return nil, nil
}
func (swCollector) Capabilities() ports.CollectorCapabilities { return ports.CollectorCapabilities{} }

type fixedRegistry struct{ col ports.TelemetryCollector }

func (f fixedRegistry) For(context.Context, uuid.UUID) (ports.TelemetryCollector, error) {
	return f.col, nil
}
func (f fixedRegistry) Default(context.Context) (ports.TelemetryCollector, error) {
	return f.col, nil
}

type recordRouter struct{ fired, resolved []monitoring.Alert }

func (r *recordRouter) Route(_ context.Context, a monitoring.Alert) error {
	if a.State == monitoring.AlertResolved {
		r.resolved = append(r.resolved, a)
	} else {
		r.fired = append(r.fired, a)
	}
	return nil
}

func brokenHealth() monitoring.ClusterHealth {
	return monitoring.ClusterHealth{Nodes: []monitoring.NodeHealth{
		{
			NodeID: "n1", Reachable: true,
			Containers: []monitoring.ContainerHealth{
				{TaskID: "t1", ServiceName: "api", Slot: 1, Verdict: monitoring.SeverityCritical, Reason: "exited (1)"},
			},
		},
		{NodeID: "n2", Reachable: false}, // unreachable
	}}
}

func healthy() monitoring.ClusterHealth {
	return monitoring.ClusterHealth{Nodes: []monitoring.NodeHealth{
		{NodeID: "n1", Reachable: true, Containers: []monitoring.ContainerHealth{
			{TaskID: "t1", ServiceName: "api", Slot: 1, Verdict: monitoring.SeverityOK},
		}},
		{NodeID: "n2", Reachable: true},
	}}
}

func TestAlertEngine_FireDedupResolve(t *testing.T) {
	h := brokenHealth()
	router := &recordRouter{}
	engine := application.NewAlertEngine(fixedRegistry{col: swCollector{h: &h}}, nil, router, nil)

	cid := uuid.New()

	// First reconcile: 2 conditions fire (critical container + unreachable node).
	require.NoError(t, engine.ReconcileCluster(context.Background(), cid))
	assert.Len(t, router.fired, 2)
	assert.Len(t, engine.ActiveAlerts(), 2)

	// Second reconcile, same state: dedup — no new fires, no resolves.
	require.NoError(t, engine.ReconcileCluster(context.Background(), cid))
	assert.Len(t, router.fired, 2)
	assert.Empty(t, router.resolved)
	assert.Len(t, engine.ActiveAlerts(), 2)

	// Cluster recovers: both alerts resolve and the active set empties.
	h = healthy()
	require.NoError(t, engine.ReconcileCluster(context.Background(), cid))
	assert.Len(t, router.resolved, 2)
	assert.Empty(t, engine.ActiveAlerts())
}

func TestAlertEngine_PerClusterIsolation(t *testing.T) {
	h := brokenHealth()
	router := &recordRouter{}
	engine := application.NewAlertEngine(fixedRegistry{col: swCollector{h: &h}}, nil, router, nil)

	a, b := uuid.New(), uuid.New()
	require.NoError(t, engine.ReconcileCluster(context.Background(), a))
	require.NoError(t, engine.ReconcileCluster(context.Background(), b))
	// Same conditions, two clusters → 4 distinct active alerts.
	assert.Len(t, engine.ActiveAlerts(), 4)

	for _, al := range engine.ActiveAlerts() {
		assert.Contains(t, []uuid.UUID{a, b}, al.ClusterID)
		assert.Equal(t, monitoring.AlertFiring, al.State)
	}
}
