// Package cluster models an orchestration cluster that Hivemind deploys to.
//
// Hivemind started single-cluster (one ambient Docker Swarm). A Cluster makes
// the target explicit and plural: every deployable resource carries a cluster
// id, and the orchestrator registry resolves that id to a live connection.
// Type is a discriminator so the same model can later describe non-Swarm
// backends (Kubernetes, Nomad…) without reshaping the domain.
package cluster

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidName     = errors.New("cluster name must be 1–64 characters")
	ErrInvalidType     = errors.New("cluster type must be one of: swarm")
	ErrInvalidMode     = errors.New("connection mode must be one of: direct, agent")
	ErrClusterNotEmpty = errors.New("cluster still has services or resources attached")
	ErrDefaultCluster  = errors.New("the default cluster cannot be removed")
	ErrNotAgentMode    = errors.New("cluster is not in agent connection mode")
	ErrNoEnrollment    = errors.New("no enrollment token issued for this cluster")
)

const maxNameLen = 64

// Type discriminates the orchestration backend. Only swarm is implemented today;
// the constant set is the seam where future backends are added.
type Type string

const (
	TypeSwarm Type = "swarm"
)

// IsValid reports whether t is a supported orchestration backend.
func (t Type) IsValid() bool {
	switch t {
	case TypeSwarm:
		return true
	default:
		return false
	}
}

// ConnectionMode is how Hivemind reaches the cluster's orchestrator.
//   - direct: Hivemind dials the Docker daemon (ambient env or mTLS over TCP);
//   - agent:  a Hivemind agent deployed on the cluster dials out and carries the
//     Docker API + per-node runtime through a reverse tunnel.
//
// It is just a transport discriminator: the registry resolves either to the same
// Orchestrator contract, so the rest of the system is mode-agnostic.
type ConnectionMode string

const (
	ModeDirect ConnectionMode = "direct"
	ModeAgent  ConnectionMode = "agent"
)

// IsValid reports whether m is a supported connection mode.
func (m ConnectionMode) IsValid() bool {
	switch m {
	case ModeDirect, ModeAgent:
		return true
	default:
		return false
	}
}

// AgentStatus is the last-known liveness of the enrolled agent, derived from its
// heartbeats by the agent hub.
type AgentStatus string

const (
	AgentPending AgentStatus = "pending" // enrolled but never connected yet
	AgentOnline  AgentStatus = "online"
	AgentOffline AgentStatus = "offline"
)

// Status is the last-known reachability of the cluster, refreshed by a
// connectivity probe (see the orchestrator registry).
type Status string

const (
	StatusUnknown     Status = "unknown"
	StatusReachable   Status = "reachable"
	StatusUnreachable Status = "unreachable"
)

// TLS carries the optional mutual-TLS material used to reach a remote Docker
// daemon over TCP. The values are PEM text; they are encrypted at rest by the
// repository and never surfaced through the API.
type TLS struct {
	CACert     string
	ClientCert string
	ClientKey  string
}

// Enabled reports whether any TLS material is configured.
func (t TLS) Enabled() bool {
	return t.CACert != "" || t.ClientCert != "" || t.ClientKey != ""
}

// Cluster is an orchestration target. Endpoint is the daemon address
// (e.g. "tcp://10.0.0.10:2376"); empty means "use the ambient Docker
// environment" — the mode the single-cluster deployment already relied on, kept
// for the seeded default cluster.
type Cluster struct {
	ID             uuid.UUID
	Name           string
	Type           Type
	ConnectionMode ConnectionMode
	Endpoint       string
	IsDefault      bool
	Status         Status
	Labels         map[string]string
	TLS            TLS

	// Agent-mode fields (zero in direct mode).
	AgentID             string      // identifier of the enrolled agent
	AgentStatus         AgentStatus // liveness derived from heartbeats
	AgentLastSeen       *time.Time
	EnrollmentTokenHash string // sha256 hex of the one-time enrollment token; never the plaintext

	CreatedAt time.Time
	UpdatedAt time.Time
}

// New builds a cluster definition in direct mode. typ defaults to swarm when empty.
func New(name string, typ Type, endpoint string) (*Cluster, error) {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxNameLen {
		return nil, ErrInvalidName
	}
	if typ == "" {
		typ = TypeSwarm
	}
	if !typ.IsValid() {
		return nil, ErrInvalidType
	}
	now := time.Now().UTC()
	return &Cluster{
		ID:             uuid.New(),
		Name:           name,
		Type:           typ,
		ConnectionMode: ModeDirect,
		Endpoint:       strings.TrimSpace(endpoint),
		Status:         StatusUnknown,
		CreatedAt:      now,
		UpdatedAt:      now,
	}, nil
}

// Rename updates the display name with the same bounds as New.
func (c *Cluster) Rename(name string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > maxNameLen {
		return ErrInvalidName
	}
	c.Name = name
	c.UpdatedAt = time.Now().UTC()
	return nil
}

// SetEndpoint changes the daemon address (and implicitly its TLS material).
func (c *Cluster) SetEndpoint(endpoint string, tls TLS) {
	c.Endpoint = strings.TrimSpace(endpoint)
	c.TLS = tls
	c.UpdatedAt = time.Now().UTC()
}

// MarkStatus records the result of a connectivity probe.
func (c *Cluster) MarkStatus(s Status) {
	c.Status = s
	c.UpdatedAt = time.Now().UTC()
}

// UseAgentMode switches the cluster to agent transport. The Docker endpoint/TLS
// become irrelevant (the agent dials out and carries the API), so they are
// cleared. The agent is pending until it first connects.
func (c *Cluster) UseAgentMode() {
	c.ConnectionMode = ModeAgent
	c.Endpoint = ""
	c.TLS = TLS{}
	c.AgentStatus = AgentPending
	c.UpdatedAt = time.Now().UTC()
}

// UseDirectMode switches the cluster back to a direct Docker connection and
// drops any agent binding.
func (c *Cluster) UseDirectMode(endpoint string, tls TLS) {
	c.ConnectionMode = ModeDirect
	c.Endpoint = strings.TrimSpace(endpoint)
	c.TLS = tls
	c.AgentID = ""
	c.AgentStatus = ""
	c.AgentLastSeen = nil
	c.EnrollmentTokenHash = ""
	c.UpdatedAt = time.Now().UTC()
}

// GenerateEnrollment issues a fresh one-time enrollment token, storing only its
// hash, and returns the plaintext (shown once, to paste into the agent stack).
// Requires agent mode.
func (c *Cluster) GenerateEnrollment() (token string, err error) {
	if c.ConnectionMode != ModeAgent {
		return "", ErrNotAgentMode
	}
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	token = base64.RawURLEncoding.EncodeToString(buf)
	c.EnrollmentTokenHash = hashToken(token)
	c.AgentStatus = AgentPending
	c.UpdatedAt = time.Now().UTC()
	return token, nil
}

// MatchEnrollment reports whether token matches the issued enrollment token
// (constant-time). Returns ErrNoEnrollment if none was issued.
func (c *Cluster) MatchEnrollment(token string) (bool, error) {
	if c.EnrollmentTokenHash == "" {
		return false, ErrNoEnrollment
	}
	want, _ := hex.DecodeString(c.EnrollmentTokenHash)
	got, _ := hex.DecodeString(hashToken(token))
	return subtle.ConstantTimeCompare(want, got) == 1, nil
}

// BindAgent links an enrolled agent and consumes the one-time token.
func (c *Cluster) BindAgent(agentID string) {
	c.AgentID = agentID
	c.EnrollmentTokenHash = "" // consumed
	c.AgentStatus = AgentOnline
	now := time.Now().UTC()
	c.AgentLastSeen = &now
	c.UpdatedAt = now
}

// MarkAgentSeen records a heartbeat from the bound agent.
func (c *Cluster) MarkAgentSeen() {
	now := time.Now().UTC()
	c.AgentStatus = AgentOnline
	c.AgentLastSeen = &now
	c.UpdatedAt = now
}

// MarkAgentOffline flags the agent as disconnected.
func (c *Cluster) MarkAgentOffline() {
	c.AgentStatus = AgentOffline
	c.UpdatedAt = time.Now().UTC()
}

func hashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}
