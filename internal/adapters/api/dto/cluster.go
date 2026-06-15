package dto

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
