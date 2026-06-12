package persistence

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"
)

// stringSlice serialises []string as a JSON text column.
type stringSlice []string

func (s stringSlice) Value() (driver.Value, error) {
	b, err := json.Marshal([]string(s))
	if err != nil {
		return nil, err
	}
	return string(b), nil
}

func (s *stringSlice) Scan(value any) error {
	if value == nil {
		*s = stringSlice{}
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case string:
		b = []byte(v)
	case []byte:
		b = v
	default:
		return fmt.Errorf("stringSlice: unsupported type %T", value)
	}
	return json.Unmarshal(b, s)
}

// ─── User ─────────────────────────────────────────────────────────────────────

type userModel struct {
	ID                  string     `gorm:"type:uuid;primaryKey;column:id"`
	Email               string     `gorm:"uniqueIndex;not null;column:email"`
	PasswordHash        string     `gorm:"not null;column:password_hash"`
	Role                string     `gorm:"not null;column:role"`
	Active              bool       `gorm:"default:true;column:active"`
	FailedLoginAttempts int        `gorm:"default:0;column:failed_login_attempts"`
	LockedUntil         *time.Time `gorm:"column:locked_until"`
	CreatedAt           time.Time  `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt           time.Time  `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (userModel) TableName() string { return "users" }

// ─── Service ──────────────────────────────────────────────────────────────────

type serviceModel struct {
	ID          string      `gorm:"type:uuid;primaryKey;column:id"`
	Name        string      `gorm:"uniqueIndex;not null;column:name"`
	Description string      `gorm:"column:description"`
	Image       string      `gorm:"not null;column:image"`
	Tag         string      `gorm:"column:tag"`
	Replicas    uint64      `gorm:"column:replicas"`
	Command     stringSlice `gorm:"type:text;column:command"`
	Entrypoint  stringSlice `gorm:"type:text;column:entrypoint"`

	CPUReservation float64 `gorm:"column:cpu_reservation"`
	CPULimit       float64 `gorm:"column:cpu_limit"`
	MemReservation int64   `gorm:"column:mem_reservation"`
	MemLimit       int64   `gorm:"column:mem_limit"`

	UpdateParallelism     uint64  `gorm:"column:update_parallelism"`
	UpdateDelay           int64   `gorm:"column:update_delay"` // nanoseconds
	UpdateFailureAction   string  `gorm:"column:update_failure_action"`
	UpdateMonitor         int64   `gorm:"column:update_monitor"` // nanoseconds
	UpdateMaxFailureRatio float64 `gorm:"column:update_max_failure_ratio"`
	UpdateOrder           string  `gorm:"column:update_order"`

	Status         string    `gorm:"column:status"`
	SwarmServiceID string    `gorm:"column:swarm_service_id"`
	CreatedAt      time.Time `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt      time.Time `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (serviceModel) TableName() string { return "services" }

// ─── EnvVar ───────────────────────────────────────────────────────────────────

type envVarModel struct {
	ID        string `gorm:"type:uuid;primaryKey;column:id"`
	ServiceID string `gorm:"type:uuid;not null;index;column:service_id"`
	Key       string `gorm:"not null;column:key"`
	Value     string `gorm:"column:value"`
	IsSecret  bool   `gorm:"column:is_secret"`
}

func (envVarModel) TableName() string { return "env_vars" }

// ─── Network ──────────────────────────────────────────────────────────────────

type networkModel struct {
	ID         string    `gorm:"type:uuid;primaryKey;column:id"`
	Name       string    `gorm:"uniqueIndex;not null;column:name"`
	Driver     string    `gorm:"column:driver"`
	Scope      string    `gorm:"column:scope"`
	Attachable bool      `gorm:"column:attachable"`
	External   bool      `gorm:"column:external"`
	SwarmID    string    `gorm:"column:swarm_id"`
	CreatedAt  time.Time `gorm:"column:created_at;autoCreateTime:false"`
}

func (networkModel) TableName() string { return "networks" }

// ─── ServiceNetwork (junction) ────────────────────────────────────────────────

type serviceNetworkModel struct {
	ServiceID string `gorm:"type:uuid;primaryKey;column:service_id"`
	NetworkID string `gorm:"type:uuid;primaryKey;column:network_id"`
}

func (serviceNetworkModel) TableName() string { return "service_networks" }

// ─── Secret ───────────────────────────────────────────────────────────────────

type secretModel struct {
	ID             string    `gorm:"type:uuid;primaryKey;column:id"`
	Name           string    `gorm:"uniqueIndex;not null;column:name"`
	CurrentVersion int       `gorm:"column:current_version"`
	TargetPath     string    `gorm:"column:target_path"`
	Checksum       string    `gorm:"column:checksum"`
	CreatedBy      string    `gorm:"type:uuid;column:created_by"`
	CreatedAt      time.Time `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt      time.Time `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (secretModel) TableName() string { return "secrets" }

// ─── SecretVersion ────────────────────────────────────────────────────────────

type secretVersionModel struct {
	ID            string    `gorm:"type:uuid;primaryKey;column:id"`
	SecretID      string    `gorm:"type:uuid;not null;index;column:secret_id"`
	Version       int       `gorm:"column:version"`
	SwarmSecretID string    `gorm:"column:swarm_secret_id"`
	Checksum      string    `gorm:"column:checksum"`
	CreatedAt     time.Time `gorm:"column:created_at;autoCreateTime:false"`
}

func (secretVersionModel) TableName() string { return "secret_versions" }

// ─── ServiceSecret (junction) ─────────────────────────────────────────────────

type serviceSecretModel struct {
	ServiceID  string `gorm:"type:uuid;primaryKey;column:service_id"`
	SecretID   string `gorm:"type:uuid;primaryKey;column:secret_id"`
	TargetPath string `gorm:"column:target_path"`
}

func (serviceSecretModel) TableName() string { return "service_secrets" }

// ─── Config ───────────────────────────────────────────────────────────────────

type configModel struct {
	ID             string    `gorm:"type:uuid;primaryKey;column:id"`
	Name           string    `gorm:"uniqueIndex;not null;column:name"`
	TargetPath     string    `gorm:"column:target_path"`
	CurrentVersion int       `gorm:"column:current_version"`
	CreatedAt      time.Time `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt      time.Time `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (configModel) TableName() string { return "configs" }

// ─── ConfigVersion ────────────────────────────────────────────────────────────

type configVersionModel struct {
	ID            string    `gorm:"type:uuid;primaryKey;column:id"`
	ConfigID      string    `gorm:"type:uuid;not null;index;column:config_id"`
	Version       int       `gorm:"column:version"`
	Content       []byte    `gorm:"column:content"`
	SwarmConfigID string    `gorm:"column:swarm_config_id"`
	Comment       string    `gorm:"column:comment"`
	CreatedBy     string    `gorm:"type:uuid;column:created_by"`
	CreatedAt     time.Time `gorm:"column:created_at;autoCreateTime:false"`
}

func (configVersionModel) TableName() string { return "config_versions" }

// ─── ServiceConfig (junction) ─────────────────────────────────────────────────

type serviceConfigModel struct {
	ServiceID  string `gorm:"type:uuid;primaryKey;column:service_id"`
	ConfigID   string `gorm:"type:uuid;primaryKey;column:config_id"`
	TargetPath string `gorm:"column:target_path"`
	UID        string `gorm:"column:uid"`
	GID        string `gorm:"column:gid"`
	Mode       uint32 `gorm:"column:mode"`
}

func (serviceConfigModel) TableName() string { return "service_configs" }

// ─── Deployment ───────────────────────────────────────────────────────────────

type deploymentModel struct {
	ID             string     `gorm:"type:uuid;primaryKey;column:id"`
	ServiceID      string     `gorm:"type:uuid;not null;index;column:service_id"`
	UserID         *string    `gorm:"type:uuid;column:user_id"`
	ImageTag       string     `gorm:"column:image_tag"`
	Trigger        string     `gorm:"column:trigger"`
	Status         string     `gorm:"column:status"`
	ErrorMessage   string     `gorm:"column:error_message"`
	ConfigSnapshot []byte     `gorm:"type:jsonb;column:config_snapshot"`
	StartedAt      time.Time  `gorm:"column:started_at;autoCreateTime:false"`
	FinishedAt     *time.Time `gorm:"column:finished_at"`
}

func (deploymentModel) TableName() string { return "deployments" }

// ─── AuditLog ─────────────────────────────────────────────────────────────────

type auditLogModel struct {
	ID           string    `gorm:"type:uuid;primaryKey;column:id"`
	UserID       *string   `gorm:"type:uuid;column:user_id"`
	Action       string    `gorm:"not null;column:action"`
	ResourceType string    `gorm:"column:resource_type"`
	ResourceID   string    `gorm:"column:resource_id"`
	Payload      []byte    `gorm:"type:jsonb;column:payload"`
	IP           string    `gorm:"column:ip"`
	CreatedAt    time.Time `gorm:"column:created_at;autoCreateTime:false;index"`
}

func (auditLogModel) TableName() string { return "audit_logs" }
