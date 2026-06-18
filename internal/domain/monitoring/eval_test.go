package monitoring_test

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/orange/hivemind/internal/domain/monitoring"
)

func TestEvaluate(t *testing.T) {
	h := monitoring.ClusterHealth{
		Nodes: []monitoring.NodeHealth{
			{
				NodeID: "n1", Hostname: "alpha", Reachable: true,
				Containers: []monitoring.ContainerHealth{
					{TaskID: "t1", ServiceName: "web", Slot: 1, Verdict: monitoring.SeverityOK},      // ignored
					{TaskID: "t2", ServiceName: "web", Slot: 2, Verdict: monitoring.SeverityWarning}, // ignored
					{TaskID: "t3", ServiceName: "api", Slot: 1, Verdict: monitoring.SeverityCritical, Reason: "exited (1)", ServiceID: uuid.New()},
					{TaskID: "t4", ServiceName: "db", Slot: 1, Verdict: monitoring.SeverityCritical, Restarts: monitoring.CrashLoopThreshold, Reason: "crash-looping (3 restarts)"},
				},
			},
			{NodeID: "n2", Hostname: "bravo", Reachable: false}, // unreachable
		},
	}

	findings := monitoring.Evaluate(h)
	require.Len(t, findings, 3) // api critical + db crashloop + node down (2 OK/warning ignored)

	byKind := map[monitoring.RuleKind]monitoring.Finding{}
	for _, f := range findings {
		byKind[f.Kind] = f
		assert.Equal(t, monitoring.SeverityCritical, f.Severity)
		assert.NotEmpty(t, f.Key)
		assert.NotEmpty(t, f.Summary)
	}

	require.Contains(t, byKind, monitoring.RuleTaskFailed)
	require.Contains(t, byKind, monitoring.RuleCrashLoop)
	require.Contains(t, byKind, monitoring.RuleNodeUnreachable)

	assert.Equal(t, "n2", byKind[monitoring.RuleNodeUnreachable].NodeID)
	assert.Contains(t, byKind[monitoring.RuleCrashLoop].Key, "db")
}

func TestEvaluate_StableKeyAcrossRestarts(t *testing.T) {
	// Two snapshots of the same crash-looping slot with different (ephemeral)
	// task ids must yield the same Finding Key, so the engine sees one ongoing
	// alert, not two.
	mk := func(taskID string) monitoring.ClusterHealth {
		return monitoring.ClusterHealth{Nodes: []monitoring.NodeHealth{{
			NodeID: "n1", Reachable: true,
			Containers: []monitoring.ContainerHealth{
				{TaskID: taskID, ServiceName: "web", Slot: 3, Verdict: monitoring.SeverityCritical, Reason: "boom"},
			},
		}}}
	}
	a := monitoring.Evaluate(mk("task-aaa"))
	b := monitoring.Evaluate(mk("task-bbb"))
	require.Len(t, a, 1)
	require.Len(t, b, 1)
	assert.Equal(t, a[0].Key, b[0].Key)
}

func TestEvaluate_Empty(t *testing.T) {
	healthy := monitoring.ClusterHealth{Nodes: []monitoring.NodeHealth{
		{NodeID: "n1", Reachable: true, Containers: []monitoring.ContainerHealth{
			{TaskID: "t", Verdict: monitoring.SeverityOK},
		}},
	}}
	assert.Empty(t, monitoring.Evaluate(healthy))
}
