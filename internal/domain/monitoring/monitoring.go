// Package monitoring models the platform's observability surface: the health of
// every container/task across a cluster's nodes, per-container resource samples,
// and the alerts derived from them.
//
// It is deliberately transport- and backend-neutral. The same model is produced
// by two collectors (see ports.TelemetryCollector): a direct Docker-API path for
// agentless clusters and an agent-tunnel path for agent clusters. Health is
// cluster-wide in both modes (built from the Swarm task list); per-container
// resource metrics are full in agent mode and partial in direct mode (only the
// connected daemon's node) — see docs/adr/0002-monitoring-and-alerting.md.
package monitoring

import (
	"time"

	"github.com/google/uuid"
)

// Severity is the normalised verdict attached to a container, a node, or an
// alert, independent of the raw Docker/Swarm state strings.
type Severity string

const (
	SeverityOK       Severity = "ok"       // running as desired
	SeverityWarning  Severity = "warning"  // degraded but not down (pending, restarting, unhealthy healthcheck)
	SeverityCritical Severity = "critical" // failed, rejected, node or tunnel down
	SeverityUnknown  Severity = "unknown"  // state not observable (e.g. unreachable node in direct mode)
)

// IsValid reports whether s is a recognised severity.
func (s Severity) IsValid() bool {
	switch s {
	case SeverityOK, SeverityWarning, SeverityCritical, SeverityUnknown:
		return true
	}
	return false
}

// ContainerHealth is the normalised health of a single task/container instance
// on a node. It is derived from a swarm TaskState: Verdict + Reason classify the
// raw CurrentState/ErrorMessage so the UI and the alert engine don't have to
// repeat the Swarm taxonomy. This is the unit of "which containers are
// struggling, and where".
type ContainerHealth struct {
	TaskID      string
	ContainerID string
	ServiceID   uuid.UUID
	ServiceName string
	NodeID      string
	Slot        int
	State       string   // raw swarm current state, e.g. "running", "failed"
	Verdict     Severity // normalised classification of State
	Reason      string   // human-readable cause (error message, "crashloop", "unschedulable"…)
	Restarts    int      // task restarts observed in the recent window (crashloop signal)
	ExitCode    *int     // nil when not exited
	Since       time.Time
}

// NodeHealth groups the containers running on one node with a verdict rollup.
// TunnelUp is set only in agent mode (per-node reverse-tunnel liveness, from
// AgentHub.ConnectedNodeIDs); nil means "not applicable" (agentless cluster).
type NodeHealth struct {
	NodeID    string
	Hostname  string
	Role      string // "manager" | "worker"
	Reachable bool
	TunnelUp  *bool

	// Capacity advertised by the node — total resources, not live usage. CPUs is
	// whole cores, MemoryBytes is total RAM.
	CPUs        float64
	MemoryBytes uint64

	// HostUsage is the node's real host-level usage (CPU/memory of the whole
	// node, not just its containers), reported by the agent from /proc. Set only
	// in agent mode with a recent heartbeat; nil otherwise.
	HostUsage *HostUsage

	Containers []ContainerHealth

	// Rollup counts over Containers, by verdict — lets the UI badge a node
	// without re-scanning the list.
	OK       int
	Warning  int
	Critical int
}

// HostUsage is a node's real host-level resource usage (the whole node, from the
// agent reading /proc) — distinct from the per-container metric rollup.
type HostUsage struct {
	CPUPercent    float64 // 0..100, whole-node CPU utilisation
	MemUsedBytes  uint64  // used = total - available (excludes reclaimable cache)
	MemTotalBytes uint64
}

// ClusterHealth is the full per-node health snapshot of a cluster — the data
// behind "what is struggling and on which node", available in both connection
// modes.
type ClusterHealth struct {
	ClusterID  uuid.UUID
	Nodes      []NodeHealth
	ObservedAt time.Time
}

// MetricSample is one point of per-container resource usage, produced as a
// stream. CPUPercent and MemPercent are normalised to 0..100. Full coverage in
// agent mode; in direct mode only the connected node's containers (see ADR 0002).
type MetricSample struct {
	ContainerID   string
	ServiceID     uuid.UUID
	NodeID        string
	At            time.Time
	CPUPercent    float64
	MemUsedBytes  uint64
	MemLimitBytes uint64
	MemPercent    float64
	NetRxBytes    uint64
	NetTxBytes    uint64
}

// ─── Alerting ────────────────────────────────────────────────────────────────

// AlertState is the lifecycle of an alert instance.
type AlertState string

const (
	AlertFiring   AlertState = "firing"
	AlertResolved AlertState = "resolved"
)

// RuleKind enumerates the conditions the alert engine can evaluate. Event-driven
// kinds (replicas/tasks/node/tunnel) need no time-series store and fire from the
// health snapshot; threshold kinds (cpu/mem) evaluate a metric sustained over a
// window (AlertRule.For) and therefore require the metrics stream.
type RuleKind string

const (
	RuleReplicasBelowDesired RuleKind = "replicas_below_desired"
	RuleTaskFailed           RuleKind = "task_failed"
	RuleCrashLoop            RuleKind = "crash_loop"
	RuleNodeUnreachable      RuleKind = "node_unreachable"
	RuleTunnelDown           RuleKind = "tunnel_down" // agent mode only
	RuleCPUOver              RuleKind = "cpu_over"
	RuleMemOver              RuleKind = "mem_over"
)

// NeedsMetrics reports whether evaluating this kind requires the metrics stream
// (vs. just the health snapshot). Drives whether a rule is available on a
// direct-mode cluster without an exporter.
func (k RuleKind) NeedsMetrics() bool {
	return k == RuleCPUOver || k == RuleMemOver
}

// AlertRule is an operator-defined condition. Scope narrows it to a cluster and
// optionally a single service (nil ServiceID = whole cluster).
type AlertRule struct {
	ID        uuid.UUID
	Name      string
	Kind      RuleKind
	Severity  Severity
	ClusterID uuid.UUID
	ServiceID *uuid.UUID    // nil = cluster-wide
	Threshold float64       // meaning depends on Kind (e.g. CPU %, restart count)
	For       time.Duration // sustain window before firing (0 = immediate)
	Enabled   bool
}

// Alert is a fired instance of a rule, routed to notification channels by the
// ports.AlertRouter.
type Alert struct {
	ID          uuid.UUID
	RuleID      uuid.UUID
	State       AlertState
	Severity    Severity
	ClusterID   uuid.UUID
	ServiceID   *uuid.UUID
	NodeID      string
	ContainerID string
	Summary     string
	Detail      string
	FiredAt     time.Time
	ResolvedAt  *time.Time
	Labels      map[string]string
}
