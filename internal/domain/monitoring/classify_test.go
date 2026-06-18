package monitoring_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/orange/hivemind/internal/domain/monitoring"
)

func TestClassify(t *testing.T) {
	cases := []struct {
		name             string
		current, desired string
		errMsg           string
		restarts         int
		want             monitoring.Severity
		reasonContains   string
	}{
		{name: "running is ok", current: "running", desired: "running", want: monitoring.SeverityOK},
		{name: "complete is ok", current: "complete", desired: "running", want: monitoring.SeverityOK},
		{name: "failed is critical with message", current: "failed", desired: "running", errMsg: "exited (137)", want: monitoring.SeverityCritical, reasonContains: "137"},
		{name: "failed without message falls back", current: "failed", desired: "running", want: monitoring.SeverityCritical, reasonContains: "failed"},
		{name: "rejected is critical", current: "rejected", desired: "running", errMsg: "no suitable node", want: monitoring.SeverityCritical, reasonContains: "no suitable node"},
		{name: "orphaned is critical", current: "orphaned", desired: "running", want: monitoring.SeverityCritical, reasonContains: "orphaned"},
		{name: "pending while converging is warning", current: "pending", desired: "running", want: monitoring.SeverityWarning, reasonContains: "starting"},
		{name: "pending with error is unschedulable critical", current: "pending", desired: "running", errMsg: "no suitable node (insufficient memory)", want: monitoring.SeverityCritical, reasonContains: "insufficient memory"},
		{name: "starting is warning", current: "starting", desired: "running", want: monitoring.SeverityWarning},
		{name: "shutdown while desired running is warning", current: "shutdown", desired: "running", want: monitoring.SeverityWarning},
		{name: "desired shutdown is expected churn", current: "running", desired: "shutdown", want: monitoring.SeverityOK},
		{name: "desired remove is expected churn", current: "failed", desired: "remove", want: monitoring.SeverityOK},
		{name: "crashloop overrides to critical", current: "starting", desired: "running", restarts: monitoring.CrashLoopThreshold, want: monitoring.SeverityCritical, reasonContains: "crash-looping"},
		{name: "unknown state is unknown", current: "weird", desired: "running", want: monitoring.SeverityUnknown, reasonContains: "weird"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, reason := monitoring.Classify(tc.current, tc.desired, tc.errMsg, tc.restarts)
			assert.Equal(t, tc.want, got)
			if tc.reasonContains != "" {
				assert.Contains(t, reason, tc.reasonContains)
			}
		})
	}
}

func TestClassify_DesiredShutdownBeatsCrashloop(t *testing.T) {
	// A task being intentionally torn down must not look like a crash loop.
	got, _ := monitoring.Classify("failed", "shutdown", "", monitoring.CrashLoopThreshold+2)
	assert.Equal(t, monitoring.SeverityOK, got)
}

func sampleNode() monitoring.NodeHealth {
	return monitoring.NodeHealth{
		NodeID: "n1",
		Containers: []monitoring.ContainerHealth{
			{TaskID: "t1", Verdict: monitoring.SeverityOK},
			{TaskID: "t2", Verdict: monitoring.SeverityWarning},
			{TaskID: "t3", Verdict: monitoring.SeverityCritical},
			{TaskID: "t4", Verdict: monitoring.SeverityCritical},
		},
	}
}

func TestNodeHealth_RecountAndWorst(t *testing.T) {
	n := sampleNode()
	n.Recount()
	assert.Equal(t, 1, n.OK)
	assert.Equal(t, 1, n.Warning)
	assert.Equal(t, 2, n.Critical)
	assert.Equal(t, monitoring.SeverityCritical, n.Worst())

	healthy := monitoring.NodeHealth{Containers: []monitoring.ContainerHealth{{Verdict: monitoring.SeverityOK}}}
	healthy.Recount()
	assert.Equal(t, monitoring.SeverityOK, healthy.Worst())

	empty := monitoring.NodeHealth{}
	empty.Recount()
	assert.Equal(t, monitoring.SeverityUnknown, empty.Worst())
}

func TestClusterHealth_Struggling(t *testing.T) {
	ch := monitoring.ClusterHealth{
		Nodes: []monitoring.NodeHealth{
			sampleNode(),
			{NodeID: "n2", Containers: []monitoring.ContainerHealth{{TaskID: "t5", Verdict: monitoring.SeverityOK}}},
		},
	}
	struggling := ch.Struggling()
	assert.Len(t, struggling, 3) // 1 warning + 2 critical, the OK ones excluded
	for _, c := range struggling {
		assert.NotEqual(t, monitoring.SeverityOK, c.Verdict)
	}
}
