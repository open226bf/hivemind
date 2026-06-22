package dto

import "time"

// ─── Cluster management ───────────────────────────────────────────────────────

// CreateClusterRequest registers a new orchestration target. Endpoint is the
// Docker daemon address (e.g. "tcp://10.0.0.10:2376"); empty uses the server's
// ambient Docker environment. TLS material is write-only.
type CreateClusterRequest struct {
	Name       string            `json:"name"     binding:"required" example:"prod-eu"`
	Type       string            `json:"type"     example:"swarm"`
	Endpoint   string            `json:"endpoint" example:"tcp://10.0.0.10:2376"`
	Labels     map[string]string `json:"labels"`
	CACert     string            `json:"ca_cert"`
	ClientCert string            `json:"client_cert"`
	ClientKey  string            `json:"client_key"`
}

// UpdateClusterRequest patches a cluster. Omitted fields are left unchanged.
// Supplying any TLS field replaces the whole TLS material.
type UpdateClusterRequest struct {
	Name       *string           `json:"name"`
	Endpoint   *string           `json:"endpoint"`
	Labels     map[string]string `json:"labels"`
	CACert     *string           `json:"ca_cert"`
	ClientCert *string           `json:"client_cert"`
	ClientKey  *string           `json:"client_key"`
}

// ClusterResponse is the canonical cluster representation. TLS material is never
// returned; tls_enabled signals whether mutual TLS is configured.
type ClusterResponse struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	ConnectionMode string            `json:"connection_mode"`
	Endpoint       string            `json:"endpoint,omitempty"`
	IsDefault      bool              `json:"is_default"`
	Status         string            `json:"status"`
	Labels         map[string]string `json:"labels,omitempty"`
	TLSEnabled     bool              `json:"tls_enabled"`
	AgentStatus    string            `json:"agent_status,omitempty"`
	AgentLastSeen  *time.Time        `json:"agent_last_seen,omitempty"`
	CreatedAt      time.Time         `json:"created_at"`
	UpdatedAt      time.Time         `json:"updated_at"`
}

// EnrollClusterResponse returns the one-time enrollment token plus a ready-to-run
// deploy command for the agent stack. The token and TLS material are shown only
// once. When the CA is configured, the mTLS fields carry the agent client
// certificate (the production path).
type EnrollClusterResponse struct {
	ClusterID   string `json:"cluster_id"`
	ClusterName string `json:"cluster_name"`
	Token       string `json:"token"`
	Command     string `json:"command"`
	// InstallCommand is the one-liner to paste on a manager (curl … | sh).
	InstallCommand string `json:"install_command"`
	// mTLS material (empty in token/dev mode).
	HubAddr    string `json:"hub_addr,omitempty"`
	ClientCert string `json:"client_cert,omitempty"`
	ClientKey  string `json:"client_key,omitempty"`
	CACert     string `json:"ca_cert,omitempty"`
}

// ─── Agent handshake (agent-facing) ───────────────────────────────────────────

// AgentNodeDTO is the node identity an agent reports.
type AgentNodeDTO struct {
	NodeID        string `json:"node_id"`
	Hostname      string `json:"hostname"`
	Role          string `json:"role"`
	IsManager     bool   `json:"is_manager"`
	IsLeader      bool   `json:"is_leader"`
	EngineVersion string `json:"engine_version"`
	SwarmID       string `json:"swarm_id"`
}

// AgentRegisterRequest is the agent enrollment / reconnection payload.
type AgentRegisterRequest struct {
	EnrollToken string       `json:"enroll_token"`
	AgentID     string       `json:"agent_id"`
	Node        AgentNodeDTO `json:"node"`
}

// AgentRegisterResponse returns the assigned agent identity and its cluster.
type AgentRegisterResponse struct {
	AgentID     string `json:"agent_id"`
	ClusterID   string `json:"cluster_id"`
	ClusterName string `json:"cluster_name"`
}

// NodeMetricsDTO is the host-level resource usage an agent reports with its
// heartbeat (read from /proc on the node).
type NodeMetricsDTO struct {
	MemTotalBytes uint64  `json:"mem_total_bytes"`
	MemUsedBytes  uint64  `json:"mem_used_bytes"`
	CPUPercent    float64 `json:"cpu_percent"`
	CPUCount      int     `json:"cpu_count"`
}

// AgentHeartbeatRequest reports liveness, the current node role, and host usage.
type AgentHeartbeatRequest struct {
	AgentID string          `json:"agent_id" binding:"required"`
	Node    AgentNodeDTO    `json:"node"`
	Metrics *NodeMetricsDTO `json:"metrics,omitempty"`
}

// ClusterListResponse wraps a paginated list of clusters.
type ClusterListResponse struct {
	Items []ClusterResponse `json:"items"`
	Total int64             `json:"total"`
	Page  int               `json:"page"`
	Size  int               `json:"size"`
}

// ClusterOverviewResponse is the aggregated dashboard payload returned by
// GET /cluster/overview.
type ClusterOverviewResponse struct {
	Cluster  ClusterSummaryDTO  `json:"cluster"`
	Nodes    []NodeDTO          `json:"nodes"`
	Services ServiceSummaryDTO  `json:"services"`
	Activity ActivitySummaryDTO `json:"activity"`
	Catalog  CatalogSummaryDTO  `json:"catalog"`
}

// ClusterSummaryDTO holds cluster-wide aggregates.
type ClusterSummaryDTO struct {
	Reachable     bool    `json:"reachable"`
	NodeTotal     int     `json:"node_total"`
	Managers      int     `json:"managers"`
	Workers       int     `json:"workers"`
	ReadyNodes    int     `json:"ready_nodes"`
	TotalCpus     float64 `json:"total_cpus"`
	TotalMemory   int64   `json:"total_memory_bytes"`
	LeaderHost    string  `json:"leader_host"`
	EngineVersion string  `json:"engine_version"`
}

// NodeDTO describes a single cluster node.
type NodeDTO struct {
	ID            string  `json:"id"`
	Hostname      string  `json:"hostname"`
	Role          string  `json:"role"`
	Leader        bool    `json:"leader"`
	Availability  string  `json:"availability"`
	State         string  `json:"state"`
	Addr          string  `json:"addr"`
	EngineVersion string  `json:"engine_version"`
	Cpus          float64 `json:"cpus"`
	MemoryBytes   int64   `json:"memory_bytes"`
	Platform      string  `json:"platform"`
	// AgentConnected is true (agent clusters only) when the node has a live tunnel.
	AgentConnected bool `json:"agent_connected"`
}

// ServiceSummaryDTO breaks the service catalog down by status.
type ServiceSummaryDTO struct {
	Total    int64 `json:"total"`
	Draft    int64 `json:"draft"`
	Deployed int64 `json:"deployed"`
	Removed  int64 `json:"removed"`
}

// ActivitySummaryDTO counts deployments by status.
type ActivitySummaryDTO struct {
	TotalDeployments int64 `json:"total_deployments"`
	InProgress       int64 `json:"in_progress"`
	Succeeded        int64 `json:"succeeded"`
	Failed           int64 `json:"failed"`
}

// CatalogSummaryDTO counts the managed resource catalogs.
type CatalogSummaryDTO struct {
	Networks int64 `json:"networks"`
	Secrets  int64 `json:"secrets"`
	Configs  int64 `json:"configs"`
}
