package ports

import (
	"context"
	"errors"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/open226bf/hivemind/internal/domain/acl"
	"github.com/open226bf/hivemind/internal/domain/deployment"
	"github.com/open226bf/hivemind/internal/domain/user"
)

// ErrSwarmServiceNotFound is returned by the orchestrator when a previously
// deployed swarm service no longer exists (typically because someone removed
// it directly via `docker service rm`). Used to detect external drift.
var ErrSwarmServiceNotFound = errors.New("swarm service not found")

// TokenService issues and validates authentication tokens (JWT in the
// reference implementation). Kept behind a port so the auth use case stays
// independent of the signing technology.
type TokenService interface {
	// GenerateAccessToken embeds the user's effective ACL scopes so per-request
	// authorization needs no grant lookup (ADR 0003). Pass nil for admins
	// (they bypass scopes via Role).
	GenerateAccessToken(u *user.User, scopes []Scope) (token string, expiresAt time.Time, err error)
	GenerateRefreshToken(u *user.User) (token string, expiresAt time.Time, err error)
	Parse(tokenString string) (*TokenClaims, error)
}

type TokenType string

const (
	TokenTypeAccess  TokenType = "access"
	TokenTypeRefresh TokenType = "refresh"
)

// Scope is one access grant carried in an access token: the verb the user holds
// on a given cluster or hive (after cascade compaction).
type Scope struct {
	Type acl.ResourceType
	ID   uuid.UUID
	Verb acl.Verb
}

// EffectiveVerb resolves the cascade for a resource located at (clusterID,
// hiveID) against a set of scopes: the effective verb is the highest of the
// matching cluster grant and the matching hive grant. A nil/empty result verb
// (rank 0) means no access. hiveID may be uuid.Nil for cluster-level or
// hive-less resources.
func EffectiveVerb(scopes []Scope, clusterID, hiveID uuid.UUID) acl.Verb {
	var best acl.Verb
	for _, s := range scopes {
		switch {
		case s.Type == acl.ResourceCluster && clusterID != uuid.Nil && s.ID == clusterID:
			best = acl.MaxVerb(best, s.Verb)
		case s.Type == acl.ResourceHive && hiveID != uuid.Nil && s.ID == hiveID:
			best = acl.MaxVerb(best, s.Verb)
		}
	}
	return best
}

type TokenClaims struct {
	UserID    uuid.UUID
	Email     string
	Role      string
	TokenType TokenType
	// TokenVer is the revocation epoch the token was minted at; the Auth
	// middleware rejects it once the stored user.TokenVersion moves past it.
	TokenVer int
	// Scopes are the effective ACL grants (empty for admins, who bypass).
	Scopes []Scope
}

// UpdateServiceOptions tweaks how UpdateService applies the spec on Swarm.
// Force triggers task recreation even when the spec is unchanged (Swarm's
// ForceUpdate counter). QueryRegistry asks Swarm to re-resolve the image
// against the registry, which is the only way to pick up a moved tag like
// "mariadb:latest" without changing the spec.
type UpdateServiceOptions struct {
	Force         bool
	QueryRegistry bool
}

// Orchestrator abstracts Docker Swarm (and future Kubernetes).
type Orchestrator interface {
	DeployService(ctx context.Context, spec ServiceSpec) (swarmServiceID string, err error)
	UpdateService(ctx context.Context, swarmServiceID string, spec ServiceSpec, opts UpdateServiceOptions) error
	RemoveService(ctx context.Context, swarmServiceID string) error
	GetServiceState(ctx context.Context, swarmServiceID string) (*ServiceState, error)

	// ListServices returns every Swarm service visible on the cluster, each with
	// the value of its hivemind.service.id label (empty when the label is absent),
	// so the application layer can classify managed / foreign / orphan services
	// for brownfield discovery and adoption (ADR 0004).
	ListServices(ctx context.Context) ([]SwarmServiceInfo, error)

	// InspectService reconstructs a running service's spec into a ServiceSpec for
	// adoption (ADR 0004). The reconstruction is best-effort: Warnings names every
	// Swarm-native aspect that has no ServiceSpec equivalent and was therefore not
	// carried into the spec (e.g. referenced secrets/configs, mounts, global mode).
	InspectService(ctx context.Context, swarmServiceID string) (*InspectedService, error)

	// SetHivemindLabel stamps the hivemind.service.id label onto an existing Swarm
	// service WITHOUT otherwise changing its spec, sealing Hivemind ownership when
	// a foreign service is adopted (ADR 0004). It is a label-only update, so it
	// does not restart tasks beyond the version bump Swarm always applies.
	SetHivemindLabel(ctx context.Context, swarmServiceID, hivemindServiceID string) error

	// ClearHivemindLabel removes the hivemind.service.id label from a service,
	// leaving it otherwise untouched, when an adopted service is released back to
	// unmanaged (ADR 0004).
	ClearHivemindLabel(ctx context.Context, swarmServiceID string) error
	WaitConvergence(ctx context.Context, swarmServiceID string, timeout time.Duration) error

	// ServiceLogs returns a stream of the service's aggregated container logs
	// (stdout+stderr, demultiplexed). The caller must Close the reader. When
	// opts.Follow is set the stream stays open until the context is cancelled.
	ServiceLogs(ctx context.Context, swarmServiceID string, opts LogOptions) (io.ReadCloser, error)

	// ExecContainer opens an interactive exec session (TTY) in a container.
	// The caller owns the returned stream and must Close it.
	ExecContainer(ctx context.Context, containerID string, opts ExecOptions) (ExecStream, error)

	CreateSecret(ctx context.Context, name string, value []byte) (swarmSecretID string, err error)
	RemoveSecret(ctx context.Context, swarmSecretID string) error

	CreateConfig(ctx context.Context, name string, content []byte) (swarmConfigID string, err error)
	RemoveConfig(ctx context.Context, swarmConfigID string) error

	CreateNetwork(ctx context.Context, name string, opts CreateNetworkOptions) (swarmNetworkID string, err error)
	RemoveNetwork(ctx context.Context, swarmNetworkID string) error
	ListNetworks(ctx context.Context) ([]SwarmNetworkInfo, error)

	// CreateVolume ensures a named volume exists (idempotent on name).
	CreateVolume(ctx context.Context, name, driver string) error
	RemoveVolume(ctx context.Context, name string) error
	// ListVolumes returns the named volumes visible on the cluster nodes.
	ListVolumes(ctx context.Context) ([]SwarmVolumeInfo, error)

	// ClusterInfo returns the nodes composing the orchestration cluster together
	// with their reported capacity and health. Powers the cluster dashboard.
	ClusterInfo(ctx context.Context) (*ClusterInfo, error)
}

// AgentCertIssuer signs client certificates for enrolled agents (their cluster
// id is the common name) and exposes the CA certificate the agent needs to trust
// the hub. Backed by the internal agent CA.
type AgentCertIssuer interface {
	IssueClient(commonName string, ttl time.Duration) (certPEM, keyPEM []byte, serial string, err error)
	CertPEM() []byte // the CA certificate (safe to hand to the agent)
}

// AgentHub manages the reverse-tunnel sessions opened by Hivemind agents (the
// "agent" connection mode). An agent deployed on a cluster dials out to the hub,
// so the cluster needs no inbound exposure. The registry uses the hub to obtain
// an Orchestrator transported over a cluster's agent tunnel.
type AgentHub interface {
	// Orchestrator returns an Orchestrator backed by the agent's tunnel, routing
	// control-plane calls to a manager-resident agent task. Fails when the agent
	// has no live session.
	Orchestrator(ctx context.Context, agentID string) (Orchestrator, error)
	// OrchestratorForNode returns an Orchestrator carried over a specific node's
	// tunnel, for node-scoped operations (that node's local Docker — stats,
	// metrics, logs, exec). Powers cluster-wide per-node metrics in agent mode.
	OrchestratorForNode(ctx context.Context, agentID, nodeID string) (Orchestrator, error)
	// Online reports whether the agent currently holds a live session.
	Online(agentID string) bool
	// ConnectedNodeIDs returns the set of Swarm node ids that currently have a
	// live agent tunnel, used to flag per-node tunnel health on the dashboard.
	ConnectedNodeIDs(agentID string) map[string]bool
	// NodeMetricsByNode returns the latest host-level usage (CPU/memory) per node
	// id, from the agents' heartbeats. Powers real node-usage gauges in agent mode.
	NodeMetricsByNode(agentID string) map[string]NodeMetrics
}

// AgentNode is a node identity reported by an agent task (transport-neutral).
type AgentNode struct {
	NodeID        string
	Hostname      string
	Role          string // "manager" | "worker"
	IsManager     bool
	IsLeader      bool
	EngineVersion string
	// Metrics is the node's host-level resource usage from the agent's last
	// heartbeat. Nil for tunnel attaches (which don't carry it).
	Metrics *NodeMetrics
}

// NodeMetrics is a node's host-level resource usage (the agent reads it from
// /proc), reported with the heartbeat — real node usage, not just its containers.
type NodeMetrics struct {
	CPUPercent    float64
	MemUsedBytes  uint64
	MemTotalBytes uint64
	CPUCount      int
}

// AgentPresence records the liveness of agents from their heartbeats. It is the
// write side of the agent hub used by the enrollment/heartbeat use case.
type AgentPresence interface {
	MarkSeen(agentID string, node AgentNode)
	Forget(agentID string)
	Online(agentID string) bool
}

// OrchestratorRegistry resolves a cluster id to a live Orchestrator. It is the
// single place that knows the platform is multi-cluster: every application
// service holds a registry instead of a single orchestrator and resolves the
// backend from the resource's ClusterID. The zero UUID resolves to the default
// cluster, which keeps pre-multi-cluster resources (and tests) working without a
// backfill.
type OrchestratorRegistry interface {
	For(ctx context.Context, clusterID uuid.UUID) (Orchestrator, error)
	Default(ctx context.Context) (Orchestrator, error)
	// Invalidate drops (and closes) any cached connection for a cluster, so the
	// next For rebuilds it. Call after a cluster's endpoint changes or it is
	// removed.
	Invalidate(clusterID uuid.UUID)
}

// ClusterInfo is a snapshot of the orchestration cluster's nodes.
type ClusterInfo struct {
	Nodes []NodeInfo
}

// NodeInfo describes a single cluster node and its reported capacity.
type NodeInfo struct {
	ID            string
	Hostname      string
	Role          string  // "manager" | "worker"
	Leader        bool    // true for the manager currently holding leadership
	Availability  string  // "active" | "pause" | "drain"
	State         string  // "ready" | "down" | "unknown" | …
	Addr          string  // node IP as seen by the manager
	EngineVersion string  // Docker engine version
	CPUs          float64 // logical cores (NanoCPUs / 1e9)
	MemoryBytes   int64   // total memory reported by the node
	Platform      string  // "os/arch", e.g. "linux/x86_64"
	// AgentConnected is true (agent-mode clusters only) when this node currently
	// has a live agent tunnel. Always false for direct clusters.
	AgentConnected bool
}

// CreateNetworkOptions controls overlay network creation on Swarm.
type CreateNetworkOptions struct {
	Attachable bool
	Subnet     string // IPAM subnet; empty = Docker default
}

// SwarmNetworkInfo is a lightweight snapshot of an overlay network on Swarm.
type SwarmNetworkInfo struct {
	ID     string
	Name   string
	Scope  string
	Driver string
	Subnet string
}

// SwarmVolumeInfo is a lightweight snapshot of a named volume on a cluster node.
type SwarmVolumeInfo struct {
	Name       string
	Driver     string
	Mountpoint string
	Scope      string
}

// SwarmServiceInfo is a lightweight snapshot of a Swarm service as it actually
// runs on the cluster, used by brownfield discovery (ADR 0004). HivemindLabel
// carries the value of the hivemind.service.id label ("" when the service was
// not created or adopted by Hivemind), so the application layer can classify it
// as managed / foreign / orphan.
type SwarmServiceInfo struct {
	SwarmServiceID string
	Name           string
	Image          string
	Replicas       uint64
	HivemindLabel  string
	CreatedAt      time.Time
}

// InspectedService is a running service's spec reconstructed for adoption
// (ADR 0004), together with warnings about anything that could not be mapped.
type InspectedService struct {
	Spec     ServiceSpec
	Warnings []string
}

type ServiceSpec struct {
	Name         string
	Image        string
	Replicas     uint64
	Command      []string
	Entrypoint   []string
	Env          map[string]string
	Resources    ResourceSpec
	Placement    PlacementSpec
	UpdateConfig UpdateConfigSpec
	Networks     []NetworkAttachment
	Secrets      []SecretAttachment
	Configs      []ConfigAttachment
	Mounts       []MountSpec
	Ports        []PortSpec
	Labels       map[string]string
}

// PortSpec is one published-port mapping applied to a service's endpoint.
type PortSpec struct {
	TargetPort    uint32 // container port
	PublishedPort uint32 // host/ingress port (0 = auto-assigned)
	Protocol      string // tcp | udp | sctp
	Mode          string // ingress | host
}

// MountSpec is one filesystem mount applied to a service's tasks (F-V2-06).
type MountSpec struct {
	Type     string // volume | bind | tmpfs
	Source   string
	Target   string
	ReadOnly bool
}

type ResourceSpec struct {
	CPUReservation float64
	CPULimit       float64
	MemReservation int64
	MemLimit       int64
}

type PlacementSpec struct {
	Constraints []string
	Preferences []string // spread descriptors
	MaxReplicas uint64
}

type UpdateConfigSpec struct {
	Parallelism     uint64
	Delay           time.Duration
	FailureAction   string
	Monitor         time.Duration
	MaxFailureRatio float64
	Order           string
}

type NetworkAttachment struct {
	SwarmNetworkID string
}

type SecretAttachment struct {
	SwarmSecretID   string
	SwarmSecretName string
	// Name is the stable secret name (no _vN suffix). Used as the mount filename
	// under /run/secrets when TargetPath is empty, so the file stays
	// /run/secrets/<name> across rotations instead of moving to <name>_v<n>.
	Name       string
	TargetPath string
}

type ConfigAttachment struct {
	SwarmConfigID   string
	SwarmConfigName string
	// Name is the stable config name (no _vN suffix); mount-filename fallback when
	// TargetPath is empty (see SecretAttachment.Name).
	Name       string
	TargetPath string
}

// LogOptions controls a service log stream (F-V2-01).
type LogOptions struct {
	Follow     bool
	Tail       string // number of lines, or "all"; empty means a sensible default
	Timestamps bool
	Since      string // RFC3339 timestamp or Go duration (e.g. "10m")
}

// ExecOptions configures an interactive container exec session.
type ExecOptions struct {
	Cmd []string // command to run; empty means a sensible default shell
	Tty bool
}

// ExecStream is a bidirectional, resizable exec session. Read returns process
// output; Write feeds process stdin. With a TTY the bytes are unframed.
type ExecStream interface {
	io.ReadWriteCloser
	Resize(ctx context.Context, height, width uint) error
}

type ServiceState struct {
	Running  int
	Desired  int
	Pending  int
	Failed   int
	Updating bool
	// ExternallyRemoved is true when the swarm service was deleted out-of-band
	// (e.g. `docker service rm`). Set by the application layer after it detects
	// the drift and reconciles the persisted status to "removed".
	ExternallyRemoved bool
	Tasks             []TaskState
}

type TaskState struct {
	ID           string
	ContainerID  string
	Node         string
	Image        string
	Slot         int
	CurrentState string
	DesiredState string
	Message      string
	ErrorMessage string
	ExitCode     *int
	PID          int
	Networks     []TaskNetwork
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

type TaskNetwork struct {
	Name    string
	Address string // CIDR, e.g. "10.0.1.5/24"
}

// Clock abstracts time (useful for testing).
type Clock interface {
	Now() time.Time
}

// Notifier sends notifications (Slack, email, webhooks).
type Notifier interface {
	Notify(ctx context.Context, event NotificationEvent) error
}

type NotificationEvent struct {
	ServiceID    uuid.UUID
	ServiceName  string
	DeploymentID uuid.UUID
	Status       deployment.Status
	ImageTag     string
	Trigger      deployment.Trigger
	ErrorMessage string
}
