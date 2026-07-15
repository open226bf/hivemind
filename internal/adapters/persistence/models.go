package persistence

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/open226bf/hivemind/internal/ports"
)

// clusterIDColumn renders a cluster id for storage. The zero UUID (meaning "the
// default cluster") maps to NULL — a uuid column rejects the empty string, and
// NULL is the correct "unset" representation shared with pre-multi-cluster rows.
func clusterIDColumn(id uuid.UUID) *string {
	if id == uuid.Nil {
		return nil
	}
	s := id.String()
	return &s
}

// parseClusterID parses a stored (nullable) cluster id; NULL/invalid values map
// to the zero UUID (the default cluster).
func parseClusterID(s *string) uuid.UUID {
	if s == nil {
		return uuid.Nil
	}
	id, _ := uuid.Parse(*s)
	return id
}

// scopeCluster adds a cluster_id filter to a query when id is non-zero.
func scopeCluster(q *gorm.DB, id uuid.UUID) *gorm.DB {
	if id == uuid.Nil {
		return q
	}
	return q.Where("cluster_id = ?", id.String())
}

// scopeACL narrows a list query to the resources the caller is allowed to see,
// reading the per-request ACL scope from the query's context (ADR 0003). It is
// ANDed with scopeCluster, so a selected cluster still applies. hiveCol is the
// column carrying a per-hive grant — "id" on the hives table, "hive_id" on
// services, and "" on tables with no hive dimension (networks/volumes/…). A nil
// scope (admin / shadow mode) is a no-op; a non-nil scope with nothing allowed
// denies every row (deny-by-default).
func scopeACL(q *gorm.DB, hiveCol string) *gorm.DB {
	s := ports.ACLListScopeFrom(q.Statement.Context)
	if s == nil {
		return q
	}
	hasClusters := len(s.Clusters) > 0
	hasHives := hiveCol != "" && len(s.Hives) > 0

	switch {
	case hasClusters && hasHives:
		return q.Where("cluster_id IN ? OR "+hiveCol+" IN ?", s.Clusters, s.Hives)
	case hasClusters:
		return q.Where("cluster_id IN ?", s.Clusters)
	case hasHives:
		return q.Where(hiveCol+" IN ?", s.Hives)
	default:
		return q.Where("1 = 0")
	}
}

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

// ─── Cluster ────────────────────────────────────────────────────────────────

// clusterModel persists an orchestration target. The TLS material columns hold
// AES-256-GCM ciphertext (via Cipher), so private keys are never at rest in
// plaintext; they are decrypted only when building a connection.
type clusterModel struct {
	ID                  string      `gorm:"type:uuid;primaryKey;column:id"`
	Name                string      `gorm:"uniqueIndex;not null;column:name"`
	Type                string      `gorm:"not null;column:type"`
	ConnectionMode      string      `gorm:"column:connection_mode;default:direct"`
	Endpoint            string      `gorm:"column:endpoint"`
	IsDefault           bool        `gorm:"index;column:is_default"`
	Status              string      `gorm:"column:status"`
	Labels              stringSlice `gorm:"type:text;column:labels"` // "key=value" entries
	EncryptedCACert     string      `gorm:"type:text;column:encrypted_ca_cert"`
	EncryptedClientCert string      `gorm:"type:text;column:encrypted_client_cert"`
	EncryptedClientKey  string      `gorm:"type:text;column:encrypted_client_key"`
	AgentID             string      `gorm:"column:agent_id"`
	AgentStatus         string      `gorm:"column:agent_status"`
	AgentLastSeen       *time.Time  `gorm:"column:agent_last_seen"`
	EnrollmentTokenHash string      `gorm:"column:enrollment_token_hash"`
	AgentCertSerial     string      `gorm:"column:agent_cert_serial"`
	CreatedAt           time.Time   `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt           time.Time   `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (clusterModel) TableName() string { return "clusters" }

// agentCAModel persists the single internal CA used to sign agent client certs
// and the hub server cert. The private key is stored as AES-GCM ciphertext.
type agentCAModel struct {
	ID              string    `gorm:"primaryKey;column:id"` // always "ca"
	CertPEM         string    `gorm:"type:text;column:cert_pem"`
	EncryptedKeyPEM string    `gorm:"type:text;column:encrypted_key_pem"`
	CreatedAt       time.Time `gorm:"column:created_at;autoCreateTime:false"`
}

func (agentCAModel) TableName() string { return "agent_ca" }

// ─── User ─────────────────────────────────────────────────────────────────────

type userModel struct {
	ID                  string     `gorm:"type:uuid;primaryKey;column:id"`
	Email               string     `gorm:"uniqueIndex;not null;column:email"`
	PasswordHash        string     `gorm:"not null;column:password_hash"`
	Role                string     `gorm:"not null;column:role"`
	Active              bool       `gorm:"default:true;column:active"`
	FailedLoginAttempts int        `gorm:"default:0;column:failed_login_attempts"`
	LockedUntil         *time.Time `gorm:"column:locked_until"`
	TokenVersion        int        `gorm:"default:0;not null;column:token_version"`
	CreatedAt           time.Time  `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt           time.Time  `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (userModel) TableName() string { return "users" }

// ─── Service ──────────────────────────────────────────────────────────────────

type serviceModel struct {
	ID          string      `gorm:"type:uuid;primaryKey;column:id"`
	ClusterID   *string     `gorm:"type:uuid;uniqueIndex:idx_services_cluster_name,priority:1;column:cluster_id"`
	HiveID      *string     `gorm:"type:uuid;index;column:hive_id"`
	Name        string      `gorm:"uniqueIndex:idx_services_cluster_name,priority:2;not null;column:name"`
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

	PlacementConstraints stringSlice `gorm:"type:text;column:placement_constraints"`
	PlacementPreferences stringSlice `gorm:"type:text;column:placement_preferences"`
	PlacementMaxReplicas uint64      `gorm:"column:placement_max_replicas"`

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

// ─── Hive (project) ───────────────────────────────────────────────────────────

type hiveModel struct {
	ID          string    `gorm:"type:uuid;primaryKey;column:id"`
	ClusterID   *string   `gorm:"type:uuid;uniqueIndex:idx_hives_cluster_name,priority:1;column:cluster_id"`
	Name        string    `gorm:"uniqueIndex:idx_hives_cluster_name,priority:2;not null;column:name"`
	Description string    `gorm:"column:description"`
	Color       string    `gorm:"column:color"`
	CreatedAt   time.Time `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt   time.Time `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (hiveModel) TableName() string { return "hives" }

// hiveEnvVarModel holds a hive-scoped ("global") env var, applied to every
// service in the hive at deploy time. Secret values are stored encrypted.
type hiveEnvVarModel struct {
	ID       string `gorm:"type:uuid;primaryKey;column:id"`
	HiveID   string `gorm:"type:uuid;not null;index;column:hive_id"`
	Key      string `gorm:"not null;column:key"`
	Value    string `gorm:"column:value"`
	IsSecret bool   `gorm:"column:is_secret"`
}

func (hiveEnvVarModel) TableName() string { return "hive_env_vars" }

// ─── Volume ───────────────────────────────────────────────────────────────────

type volumeModel struct {
	ID        string    `gorm:"type:uuid;primaryKey;column:id"`
	ClusterID *string   `gorm:"type:uuid;uniqueIndex:idx_volumes_cluster_name,priority:1;column:cluster_id"`
	Name      string    `gorm:"uniqueIndex:idx_volumes_cluster_name,priority:2;not null;column:name"`
	Driver    string    `gorm:"column:driver"`
	CreatedAt time.Time `gorm:"column:created_at;autoCreateTime:false"`
}

func (volumeModel) TableName() string { return "volumes" }

// ─── ServiceMount ─────────────────────────────────────────────────────────────

type serviceMountModel struct {
	ID        string `gorm:"type:uuid;primaryKey;column:id"`
	ServiceID string `gorm:"type:uuid;not null;index;column:service_id"`
	Type      string `gorm:"column:type"` // volume | bind | tmpfs
	Source    string `gorm:"column:source"`
	Target    string `gorm:"column:target"`
	ReadOnly  bool   `gorm:"column:read_only"`
	Position  int    `gorm:"column:position"` // preserves declared order
}

func (serviceMountModel) TableName() string { return "service_mounts" }

type servicePortModel struct {
	ID            string `gorm:"type:uuid;primaryKey;column:id"`
	ServiceID     string `gorm:"type:uuid;not null;index;column:service_id"`
	TargetPort    uint32 `gorm:"column:target_port"`
	PublishedPort uint32 `gorm:"column:published_port"`
	Protocol      string `gorm:"column:protocol"` // tcp | udp | sctp
	Mode          string `gorm:"column:mode"`     // ingress | host
	Position      int    `gorm:"column:position"` // preserves declared order
}

func (servicePortModel) TableName() string { return "service_ports" }

// ─── Network ──────────────────────────────────────────────────────────────────

type networkModel struct {
	ID         string    `gorm:"type:uuid;primaryKey;column:id"`
	ClusterID  *string   `gorm:"type:uuid;uniqueIndex:idx_networks_cluster_name,priority:1;column:cluster_id"`
	Name       string    `gorm:"uniqueIndex:idx_networks_cluster_name,priority:2;not null;column:name"`
	Driver     string    `gorm:"column:driver"`
	Scope      string    `gorm:"column:scope"`
	Subnet     string    `gorm:"column:subnet"`
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
	ClusterID      *string   `gorm:"type:uuid;uniqueIndex:idx_secrets_cluster_name,priority:1;column:cluster_id"`
	Name           string    `gorm:"uniqueIndex:idx_secrets_cluster_name,priority:2;not null;column:name"`
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
	ID            string `gorm:"type:uuid;primaryKey;column:id"`
	SecretID      string `gorm:"type:uuid;not null;index;column:secret_id"`
	Version       int    `gorm:"column:version"`
	SwarmSecretID string `gorm:"column:swarm_secret_id"`
	Checksum      string `gorm:"column:checksum"`
	// EncryptedValue holds the AES-256-GCM ciphertext of the secret value.
	// It is decrypted only at deploy time to create the Swarm secret and is
	// never exposed through the API.
	EncryptedValue string    `gorm:"column:encrypted_value"`
	CreatedAt      time.Time `gorm:"column:created_at;autoCreateTime:false"`
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
	ClusterID      *string   `gorm:"type:uuid;uniqueIndex:idx_configs_cluster_name,priority:1;column:cluster_id"`
	Name           string    `gorm:"uniqueIndex:idx_configs_cluster_name,priority:2;not null;column:name"`
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

// ─── Template ─────────────────────────────────────────────────────────────────

type templateModel struct {
	ID           string      `gorm:"type:uuid;primaryKey;column:id"`
	Name         string      `gorm:"uniqueIndex;not null;column:name"`
	Description  string      `gorm:"column:description"`
	Version      int         `gorm:"column:version"`
	SpecJSON     []byte      `gorm:"type:jsonb;column:spec"`
	LockedFields stringSlice `gorm:"type:text;column:locked_fields"`
	CreatedBy    string      `gorm:"type:uuid;column:created_by"`
	CreatedAt    time.Time   `gorm:"column:created_at;autoCreateTime:false"`
	UpdatedAt    time.Time   `gorm:"column:updated_at;autoUpdateTime:false"`
}

func (templateModel) TableName() string { return "templates" }

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

// ─── ServiceSnapshot ──────────────────────────────────────────────────────────

// serviceSnapshotModel stores a complete point-in-time capture of a service.
// EncryptedPayload holds the AES-256-GCM ciphertext of the JSON payload (which
// embeds secret values and config contents), so nothing sensitive is ever at
// rest in plaintext. Metadata columns stay clear for listing/filtering.
type serviceSnapshotModel struct {
	ID               string    `gorm:"type:uuid;primaryKey;column:id"`
	ServiceID        string    `gorm:"type:uuid;not null;index;column:service_id"`
	Label            string    `gorm:"column:label"`
	CreatedBy        *string   `gorm:"type:uuid;column:created_by"`
	SchemaVersion    int       `gorm:"column:schema_version"`
	EncryptedPayload string    `gorm:"type:text;column:encrypted_payload"`
	CreatedAt        time.Time `gorm:"column:created_at;autoCreateTime:false;index"`
}

func (serviceSnapshotModel) TableName() string { return "service_snapshots" }

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

// ─── ACL grant ────────────────────────────────────────────────────────────────

// aclGrantModel persists a fine-grained access grant (ADR 0003). The unique
// index keeps one grant per (subject, resource); the resource index answers
// "who has access to this resource"; the expires_at index prunes/filters
// time-bound grants.
type aclGrantModel struct {
	ID           string     `gorm:"type:uuid;primaryKey;column:id"`
	SubjectID    string     `gorm:"type:uuid;not null;uniqueIndex:idx_acl_unique,priority:1;column:subject_id"`
	ResourceType string     `gorm:"not null;uniqueIndex:idx_acl_unique,priority:2;index:idx_acl_resource,priority:1;column:resource_type"`
	ResourceID   string     `gorm:"type:uuid;not null;uniqueIndex:idx_acl_unique,priority:3;index:idx_acl_resource,priority:2;column:resource_id"`
	Verb         string     `gorm:"not null;column:verb"`
	CreatedBy    string     `gorm:"type:uuid;column:created_by"`
	CreatedAt    time.Time  `gorm:"column:created_at;autoCreateTime:false"`
	ExpiresAt    *time.Time `gorm:"index;column:expires_at"`
}

func (aclGrantModel) TableName() string { return "acl_grants" }
