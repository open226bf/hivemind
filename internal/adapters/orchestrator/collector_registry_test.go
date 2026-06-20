package orchestrator_test

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/open226bf/hivemind/internal/adapters/orchestrator"
	"github.com/open226bf/hivemind/internal/domain/monitoring"
	"github.com/open226bf/hivemind/internal/ports"
)

// stubCollector is a no-op ports.TelemetryCollector that records the cluster id
// it was built for.
type stubCollector struct{ id uuid.UUID }

func (s stubCollector) CollectHealth(context.Context) (*monitoring.ClusterHealth, error) {
	return &monitoring.ClusterHealth{ClusterID: s.id}, nil
}
func (stubCollector) CollectMetrics(context.Context) ([]monitoring.MetricSample, error) {
	return nil, nil
}
func (stubCollector) Capabilities() ports.CollectorCapabilities { return ports.CollectorCapabilities{} }

// providerOrch is an orchestrator that also provides telemetry. The embedded nil
// ports.Orchestrator satisfies the interface; only Collector is exercised.
type providerOrch struct{ ports.Orchestrator }

func (providerOrch) Collector(id uuid.UUID) ports.TelemetryCollector { return stubCollector{id: id} }

// plainOrch implements only ports.Orchestrator (no telemetry).
type plainOrch struct{ ports.Orchestrator }

type fakeOrchRegistry struct {
	orch ports.Orchestrator
	err  error
}

func (f fakeOrchRegistry) For(context.Context, uuid.UUID) (ports.Orchestrator, error) {
	return f.orch, f.err
}
func (f fakeOrchRegistry) Default(context.Context) (ports.Orchestrator, error) {
	return f.orch, f.err
}
func (fakeOrchRegistry) Invalidate(uuid.UUID) {}

func TestCollectorRegistry_For_Provider(t *testing.T) {
	reg := orchestrator.NewCollectorRegistry(fakeOrchRegistry{orch: providerOrch{}}, nil, nil)

	id := uuid.New()
	col, err := reg.For(context.Background(), id)
	require.NoError(t, err)
	require.NotNil(t, col)

	// The collector is stamped with the requested cluster id.
	h, err := col.CollectHealth(context.Background())
	require.NoError(t, err)
	assert.Equal(t, id, h.ClusterID)
}

func TestCollectorRegistry_For_Unsupported(t *testing.T) {
	reg := orchestrator.NewCollectorRegistry(fakeOrchRegistry{orch: plainOrch{}}, nil, nil)

	col, err := reg.For(context.Background(), uuid.New())
	assert.Nil(t, col)
	assert.ErrorIs(t, err, orchestrator.ErrTelemetryUnsupported)
}

func TestCollectorRegistry_For_PropagatesResolveError(t *testing.T) {
	boom := errors.New("cluster unreachable")
	reg := orchestrator.NewCollectorRegistry(fakeOrchRegistry{err: boom}, nil, nil)

	col, err := reg.For(context.Background(), uuid.New())
	assert.Nil(t, col)
	assert.ErrorIs(t, err, boom)
}

func TestCollectorRegistry_Default(t *testing.T) {
	reg := orchestrator.NewCollectorRegistry(fakeOrchRegistry{orch: providerOrch{}}, nil, nil)

	col, err := reg.Default(context.Background())
	require.NoError(t, err)
	h, err := col.CollectHealth(context.Background())
	require.NoError(t, err)
	assert.Equal(t, uuid.Nil, h.ClusterID) // default cluster → zero UUID
}
