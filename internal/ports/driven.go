package ports

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/orange/hivemind/internal/domain/deployment"
	"github.com/orange/hivemind/internal/domain/user"
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

	CreateSecret(ctx context.Context, name string, value []byte) (swarmSecretID string, err error)
	RemoveSecret(ctx context.Context, swarmSecretID string) error

	CreateConfig(ctx context.Context, name string, content []byte) (swarmConfigID string, err error)
	RemoveConfig(ctx context.Context, swarmConfigID string) error

	CreateNetwork(ctx context.Context, name string, attachable bool) (swarmNetworkID string, err error)
	RemoveNetwork(ctx context.Context, swarmNetworkID string) error
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
	SwarmSecretID string
	TargetPath    string
}

type ConfigAttachment struct {
	SwarmConfigID string
	TargetPath    string
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
	Node         string
	CurrentState string
	DesiredState string
	ErrorMessage string
	UpdatedAt    time.Time
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
