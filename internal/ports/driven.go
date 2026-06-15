package ports

import (
	"context"
	"io"
	"time"

	"github.com/google/uuid"
	"github.com/open226bf/hivemind/internal/domain/deployment"
	"github.com/open226bf/hivemind/internal/domain/user"
)

// TokenService issues and validates authentication tokens (JWT in the
// reference implementation). Kept behind a port so the auth use case stays
// independent of the signing technology.
type TokenService interface {
	GenerateAccessToken(u *user.User) (token string, expiresAt time.Time, err error)
	GenerateRefreshToken(u *user.User) (token string, expiresAt time.Time, err error)
	Parse(tokenString string) (*TokenClaims, error)
}

type TokenType string

const (
	TokenTypeAccess  TokenType = "access"
	TokenTypeRefresh TokenType = "refresh"
)

type TokenClaims struct {
	UserID    uuid.UUID
	Email     string
	Role      string
	TokenType TokenType
}

// Orchestrator abstracts Docker Swarm (and future Kubernetes).
type Orchestrator interface {
	DeployService(ctx context.Context, spec ServiceSpec) (swarmServiceID string, err error)
	UpdateService(ctx context.Context, swarmServiceID string, spec ServiceSpec) error
	RemoveService(ctx context.Context, swarmServiceID string) error
	GetServiceState(ctx context.Context, swarmServiceID string) (*ServiceState, error)
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

	// ClusterInfo returns the nodes composing the orchestration cluster together
	// with their reported capacity and health. Powers the cluster dashboard.
	ClusterInfo(ctx context.Context) (*ClusterInfo, error)
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

type ServiceSpec struct {
	Name         string
	Image        string
	Replicas     uint64
	Command      []string
	Entrypoint   []string
	Env          map[string]string
	Resources    ResourceSpec
	UpdateConfig UpdateConfigSpec
	Networks     []NetworkAttachment
	Secrets      []SecretAttachment
	Configs      []ConfigAttachment
	Labels       map[string]string
}

type ResourceSpec struct {
	CPUReservation float64
	CPULimit       float64
	MemReservation int64
	MemLimit       int64
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
	TargetPath      string
}

type ConfigAttachment struct {
	SwarmConfigID   string
	SwarmConfigName string
	TargetPath      string
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
	Tasks    []TaskState
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
