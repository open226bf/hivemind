package dto

import "time"

// ContainerHealthResponse is one task/container's normalised health on a node.
type ContainerHealthResponse struct {
	TaskID      string    `json:"task_id"`
	ContainerID string    `json:"container_id,omitempty"`
	ServiceID   string    `json:"service_id,omitempty"`
	ServiceName string    `json:"service_name,omitempty"`
	Slot        int       `json:"slot"`
	State       string    `json:"state"`
	Verdict     string    `json:"verdict"` // ok | warning | critical | unknown
	Reason      string    `json:"reason,omitempty"`
	Restarts    int       `json:"restarts"`
	ExitCode    *int      `json:"exit_code,omitempty"`
	Since       time.Time `json:"since"`
}

// NodeHealthResponse groups a node's containers with a verdict rollup. TunnelUp
// is present only for agent-mode clusters.
type NodeHealthResponse struct {
	NodeID      string                    `json:"node_id"`
	Hostname    string                    `json:"hostname,omitempty"`
	Role        string                    `json:"role,omitempty"`
	Reachable   bool                      `json:"reachable"`
	TunnelUp    *bool                     `json:"tunnel_up,omitempty"`
	CPUs        float64                   `json:"cpus"`         // total cores (capacity, not usage)
	MemoryBytes uint64                    `json:"memory_bytes"` // total RAM (capacity, not usage)
	Worst       string                    `json:"worst"`        // highest severity among containers
	OK          int                       `json:"ok"`
	Warning     int                       `json:"warning"`
	Critical    int                       `json:"critical"`
	Containers  []ContainerHealthResponse `json:"containers"`
}

// ClusterHealthResponse is the per-node health snapshot of a cluster.
type ClusterHealthResponse struct {
	ClusterID  string    `json:"cluster_id,omitempty"`
	ObservedAt time.Time `json:"observed_at"`
	// MetricsCoverage reports whether per-container metrics for this cluster are
	// cluster-wide ("cluster", agent mode) or limited to the connected node
	// ("connected-node", direct mode) — so the UI can set expectations.
	MetricsCoverage string               `json:"metrics_coverage"`
	Nodes           []NodeHealthResponse `json:"nodes"`
}

// AlertResponse is a firing alert produced by the event-driven alert engine.
type AlertResponse struct {
	ID          string    `json:"id"`
	Severity    string    `json:"severity"` // ok | warning | critical | unknown
	Kind        string    `json:"kind"`     // task_failed | crash_loop | node_unreachable | …
	ClusterID   string    `json:"cluster_id,omitempty"`
	ServiceID   string    `json:"service_id,omitempty"`
	NodeID      string    `json:"node_id,omitempty"`
	ContainerID string    `json:"container_id,omitempty"`
	Summary     string    `json:"summary"`
	Detail      string    `json:"detail,omitempty"`
	FiredAt     time.Time `json:"fired_at"`
}

// AlertListResponse wraps the currently-firing alerts.
type AlertListResponse struct {
	Items []AlertResponse `json:"items"`
	Total int             `json:"total"`
}
