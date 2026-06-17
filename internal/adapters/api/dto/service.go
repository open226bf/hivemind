package dto

import (
	"time"
)

// ─── Requests ─────────────────────────────────────────────────────────────────

// CreateServiceRequest is the body for POST /services.
type CreateServiceRequest struct {
	Name         string           `json:"name"          binding:"required"`
	Description  string           `json:"description"`
	Image        string           `json:"image"         binding:"required"`
	Tag          string           `json:"tag"`
	Replicas     uint64           `json:"replicas"`
	Command      []string         `json:"command"`
	Entrypoint   []string         `json:"entrypoint"`
	Resources    *ResourcesDTO    `json:"resources"`
	Placement    *PlacementDTO    `json:"placement"`
	UpdateConfig *UpdateConfigDTO `json:"update_config"`
	Hive         string           `json:"hive"`
	// Cluster is the target cluster id. Empty selects the default cluster.
	Cluster string `json:"cluster"`
}

// UpdateServiceRequest is the body for PUT /services/:id.
// All fields are optional; absent (null) fields are left unchanged.
type UpdateServiceRequest struct {
	Description  *string          `json:"description"`
	Image        *string          `json:"image"`
	Tag          *string          `json:"tag"`
	Replicas     *uint64          `json:"replicas"`
	Command      *[]string        `json:"command"`
	Entrypoint   *[]string        `json:"entrypoint"`
	Resources    *ResourcesDTO    `json:"resources"`
	Placement    *PlacementDTO    `json:"placement"`
	UpdateConfig *UpdateConfigDTO `json:"update_config"`
}

// ─── Nested DTOs ──────────────────────────────────────────────────────────────

// ResourcesDTO maps CPU (decimal cores) and memory (bytes) resource constraints.
type ResourcesDTO struct {
	CPUReservation float64 `json:"cpu_reservation" example:"0.25"`
	CPULimit       float64 `json:"cpu_limit"       example:"0.5"`
	MemReservation int64   `json:"mem_reservation" example:"67108864"`  // bytes
	MemLimit       int64   `json:"mem_limit"       example:"134217728"` // bytes
}

// PlacementDTO controls where the scheduler places a service's tasks.
// Constraints are hard filters (e.g. "node.role==worker"); preferences are
// spread descriptors (e.g. "node.labels.zone"); max_replicas_per_node caps how
// many tasks may run on a single node (0 = unlimited).
type PlacementDTO struct {
	Constraints        []string `json:"constraints"        example:"node.role==worker"`
	Preferences        []string `json:"preferences"        example:"node.labels.zone"`
	MaxReplicasPerNode uint64   `json:"max_replicas_per_node" example:"0"`
}

// UpdateConfigDTO controls rolling-update behaviour.
// DelaySeconds and MonitorSeconds are whole seconds.
type UpdateConfigDTO struct {
	Parallelism     uint64  `json:"parallelism"      example:"1"`
	DelaySeconds    int64   `json:"delay_seconds"    example:"10"`
	FailureAction   string  `json:"failure_action"   example:"rollback"` // pause | continue | rollback
	MonitorSeconds  int64   `json:"monitor_seconds"  example:"30"`
	MaxFailureRatio float64 `json:"max_failure_ratio" example:"0"`
	Order           string  `json:"order"            example:"start-first"` // start-first | stop-first
}

// ─── Responses ────────────────────────────────────────────────────────────────

// ServiceResponse is the canonical service representation returned by all endpoints.
type ServiceResponse struct {
	ID             string          `json:"id"`
	ClusterID      string          `json:"cluster_id,omitempty"`
	HiveID         string          `json:"hive_id,omitempty"`
	Name           string          `json:"name"`
	Description    string          `json:"description"`
	Image          string          `json:"image"`
	Tag            string          `json:"tag"`
	FullImage      string          `json:"full_image"`
	Replicas       uint64          `json:"replicas"`
	Command        []string        `json:"command"`
	Entrypoint     []string        `json:"entrypoint"`
	Resources      ResourcesDTO    `json:"resources"`
	Placement      PlacementDTO    `json:"placement"`
	UpdateConfig   UpdateConfigDTO `json:"update_config"`
	Status         string          `json:"status"`
	SwarmServiceID string          `json:"swarm_service_id,omitempty"`
	CreatedAt      time.Time       `json:"created_at"`
	UpdatedAt      time.Time       `json:"updated_at"`
}

// ServiceListResponse wraps a paginated list of services.
type ServiceListResponse struct {
	Items []ServiceResponse `json:"items"`
	Total int64             `json:"total"`
	Page  int               `json:"page"`
	Size  int               `json:"size"`
}
