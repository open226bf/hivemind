package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/domain/cluster"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/domainerrors"
)

// clientCertTTL is how long an issued agent client certificate is valid.
const clientCertTTL = 365 * 24 * time.Hour

var (
	// ErrInvalidEnrollment is returned when an agent presents a bad or expired
	// enrollment token.
	ErrInvalidEnrollment = errors.New("invalid or expired enrollment token")
	// ErrAgentNotRegistered is returned when a heartbeat references an unknown agent.
	ErrAgentNotRegistered = errors.New("agent is not registered")
	// ErrCertRejected is returned when a tunnel client certificate does not match
	// the cluster's current (non-revoked) certificate.
	ErrCertRejected = errors.New("client certificate rejected")
)

// AgentService drives the agent handshake: an admin enrolls a cluster (issuing a
// one-time token), the agent registers (consuming the token, getting an agent
// id) and then heartbeats. Cluster liveness is mirrored into the in-memory
// presence so the registry/UI know whether the agent is online.
type AgentService struct {
	clusters ports.ClusterRepository
	presence ports.AgentPresence
	registry ports.OrchestratorRegistry // optional; invalidated on mode/binding change
	certs    ports.AgentCertIssuer      // optional; nil disables mTLS (dev/token mode)
	hubAddr  string                     // advertised agent-hub address (mTLS endpoint)
}

func NewAgentService(clusters ports.ClusterRepository, presence ports.AgentPresence, registry ports.OrchestratorRegistry, certs ports.AgentCertIssuer, hubAddr string) *AgentService {
	return &AgentService{clusters: clusters, presence: presence, registry: registry, certs: certs, hubAddr: hubAddr}
}

// Enrollment is the result of issuing an enrollment token (admin side). When the
// CA is configured it also carries the client certificate material the agent
// uses to authenticate the mTLS tunnel.
type Enrollment struct {
	ClusterID   uuid.UUID
	ClusterName string
	Token       string // plaintext, shown once
	// mTLS material (empty when the CA is not configured — token/dev mode).
	HubAddr       string
	ClientCertPEM string
	ClientKeyPEM  string
	CACertPEM     string
}

// Enroll switches a cluster to agent mode (if needed) and issues a fresh
// one-time enrollment token. Admin-only at the handler layer.
func (s *AgentService) Enroll(ctx context.Context, clusterID uuid.UUID) (*Enrollment, error) {
	c, err := s.clusters.FindByID(ctx, clusterID)
	if err != nil {
		return nil, err
	}
	if c.ConnectionMode != cluster.ModeAgent {
		c.UseAgentMode()
	}
	token, err := c.GenerateEnrollment()
	if err != nil {
		return nil, err
	}

	out := &Enrollment{ClusterID: c.ID, ClusterName: c.Name, Token: token}

	// When the CA is configured, also issue a client certificate. The agent then
	// authenticates the tunnel with mTLS; the cluster id is the certificate CN
	// and the agent id, and the serial gates revocation.
	if s.certs != nil {
		certPEM, keyPEM, serial, err := s.certs.IssueClient(c.ID.String(), clientCertTTL)
		if err != nil {
			return nil, fmt.Errorf("issue client cert: %w", err)
		}
		c.AgentID = c.ID.String()
		c.AgentCertSerial = serial
		out.HubAddr = s.hubAddr
		out.ClientCertPEM = string(certPEM)
		out.ClientKeyPEM = string(keyPEM)
		out.CACertPEM = string(s.certs.CertPEM())
	}

	if err := s.clusters.Update(ctx, c); err != nil {
		return nil, err
	}
	s.invalidate(c.ID)
	return out, nil
}

// AuthorizeTunnel validates a tunnel client certificate: the CN is the cluster
// id and the serial must match the cluster's current certificate (mismatch =
// revoked / rotated). Returns the agent id to attach the session under, and
// records presence.
func (s *AgentService) AuthorizeTunnel(ctx context.Context, clusterID uuid.UUID, certSerial string, node ports.AgentNode) (string, error) {
	c, err := s.clusters.FindByID(ctx, clusterID)
	if err != nil {
		return "", ErrCertRejected
	}
	if c.ConnectionMode != cluster.ModeAgent || c.AgentCertSerial == "" || c.AgentCertSerial != certSerial {
		return "", ErrCertRejected
	}
	c.MarkAgentSeen()
	if err := s.clusters.Update(ctx, c); err != nil {
		return "", err
	}
	if c.AgentID != "" {
		s.presence.MarkSeen(c.AgentID, node)
	}
	return c.AgentID, nil
}

// RegisterInput is what an agent presents on (re)connection.
type RegisterInput struct {
	EnrollToken string
	AgentID     string
	Node        ports.AgentNode
}

// Registration is returned to a successfully registered agent.
type Registration struct {
	AgentID     string
	ClusterID   uuid.UUID
	ClusterName string
}

// Register enrolls a new agent (consuming the token) or re-identifies an existing
// one (by agent id), recording presence either way.
func (s *AgentService) Register(ctx context.Context, in RegisterInput) (*Registration, error) {
	// Reconnection: known agent id, no token needed.
	if in.AgentID != "" {
		c, err := s.clusters.FindByAgentID(ctx, in.AgentID)
		if err != nil {
			if errors.Is(err, domainerrors.ErrNotFound) {
				return nil, ErrAgentNotRegistered
			}
			return nil, err
		}
		return s.bindSeen(ctx, c, in.AgentID, in.Node, false)
	}

	// First enrollment: resolve the cluster by the token hash, then verify.
	if in.EnrollToken == "" {
		return nil, ErrInvalidEnrollment
	}
	c, err := s.clusters.FindByEnrollmentTokenHash(ctx, cluster.HashEnrollmentToken(in.EnrollToken))
	if err != nil {
		if errors.Is(err, domainerrors.ErrNotFound) {
			return nil, ErrInvalidEnrollment
		}
		return nil, err
	}
	ok, err := c.MatchEnrollment(in.EnrollToken)
	if err != nil || !ok {
		return nil, ErrInvalidEnrollment
	}
	agentID := uuid.NewString()
	c.BindAgent(agentID)
	return s.bindSeen(ctx, c, agentID, in.Node, true)
}

// Bound reports whether an agent id maps to an enrolled cluster — used to
// authenticate a tunnel connection before attaching it.
func (s *AgentService) Bound(ctx context.Context, agentID string) bool {
	if agentID == "" {
		return false
	}
	_, err := s.clusters.FindByAgentID(ctx, agentID)
	return err == nil
}

// Heartbeat records liveness from an enrolled agent.
func (s *AgentService) Heartbeat(ctx context.Context, agentID string, node ports.AgentNode) error {
	c, err := s.clusters.FindByAgentID(ctx, agentID)
	if err != nil {
		if errors.Is(err, domainerrors.ErrNotFound) {
			return ErrAgentNotRegistered
		}
		return err
	}
	c.MarkAgentSeen()
	if err := s.clusters.Update(ctx, c); err != nil {
		return err
	}
	s.presence.MarkSeen(agentID, node)
	return nil
}

// bindSeen persists the cluster, records presence and returns the registration.
// invalidate is true when the binding changed (new enrollment) so the registry
// drops any stale resolution.
func (s *AgentService) bindSeen(ctx context.Context, c *cluster.Cluster, agentID string, node ports.AgentNode, invalidate bool) (*Registration, error) {
	c.MarkAgentSeen()
	if err := s.clusters.Update(ctx, c); err != nil {
		return nil, fmt.Errorf("persist agent registration: %w", err)
	}
	s.presence.MarkSeen(agentID, node)
	if invalidate {
		s.invalidate(c.ID)
	}
	return &Registration{AgentID: agentID, ClusterID: c.ID, ClusterName: c.Name}, nil
}

func (s *AgentService) invalidate(id uuid.UUID) {
	if s.registry != nil {
		s.registry.Invalidate(id)
	}
}
