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
type swCollector struct {
	h *monitoring.ClusterHealth
	m *[]monitoring.MetricSample // nil = no usage data
}

func (c swCollector) CollectHealth(context.Context) (*monitoring.ClusterHealth, error) {
	return c.h, nil
}
func (c swCollector) CollectMetrics(context.Context) ([]monitoring.MetricSample, error) {
	if c.m == nil {
		return nil, nil
	}
	return *c.m, nil
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

func TestAlertEngine_MetricThreshold(t *testing.T) {
	// A healthy container (no health alert) whose CPU is over the default 85%.
	h := monitoring.ClusterHealth{Nodes: []monitoring.NodeHealth{{
		NodeID: "n1", Reachable: true,
		Containers: []monitoring.ContainerHealth{
			{TaskID: "t1", ContainerID: "c1", ServiceName: "redis", Verdict: monitoring.SeverityOK},
		},
	}}}
	metrics := []monitoring.MetricSample{{ContainerID: "c1", CPUPercent: 95, MemPercent: 10}}

	router := &recordRouter{}
	engine := application.NewAlertEngine(fixedRegistry{col: swCollector{h: &h, m: &metrics}}, nil, router, nil)
	cid := uuid.New()

	// CPU over threshold → one warning alert (no health alert, container is OK).
	require.NoError(t, engine.ReconcileCluster(context.Background(), cid))
	require.Len(t, engine.ActiveAlerts(), 1)
	al := engine.ActiveAlerts()[0]
	assert.Equal(t, monitoring.SeverityWarning, al.Severity)
	assert.Equal(t, "cpu_over", al.Labels["kind"])
	assert.Contains(t, al.Summary, "redis")

	// CPU drops below the threshold → the alert resolves.
	metrics = []monitoring.MetricSample{{ContainerID: "c1", CPUPercent: 5, MemPercent: 10}}
	require.NoError(t, engine.ReconcileCluster(context.Background(), cid))
	assert.Empty(t, engine.ActiveAlerts())
	assert.Len(t, router.resolved, 1)
}
