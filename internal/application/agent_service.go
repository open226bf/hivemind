package application

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"

	"github.com/orange/hivemind/internal/domain/cluster"
	"github.com/orange/hivemind/internal/ports"
	"github.com/orange/hivemind/pkg/domainerrors"
	"github.com/orange/hivemind/pkg/pagination"
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

// InstallScript renders a self-contained shell script to run on a cluster
// manager: it provisions the agent (secrets + compose + stack deploy). The
// caller authenticates with the enrollment token. When the CA is configured the
// script installs the mutual-TLS agent (a fresh client certificate is issued and
// embedded, rotating any previous one); otherwise it installs the token agent.
func (s *AgentService) InstallScript(ctx context.Context, token, serverURL string) (string, error) {
	if token == "" {
		return "", ErrInvalidEnrollment
	}
	c, err := s.clusters.FindByEnrollmentTokenHash(ctx, cluster.HashEnrollmentToken(token))
	if err != nil {
		return "", ErrInvalidEnrollment
	}
	if ok, _ := c.MatchEnrollment(token); !ok {
		return "", ErrInvalidEnrollment
	}

	if s.certs != nil {
		certPEM, keyPEM, serial, err := s.certs.IssueClient(c.ID.String(), clientCertTTL)
		if err != nil {
			return "", fmt.Errorf("issue client cert: %w", err)
		}
		c.AgentID = c.ID.String()
		c.AgentCertSerial = serial
		if err := s.clusters.Update(ctx, c); err != nil {
			return "", err
		}
		s.invalidate(c.ID)
		return renderMTLSInstall(s.hubAddr, string(certPEM), string(keyPEM), string(s.certs.CertPEM())), nil
	}

	return renderTokenInstall(serverURL, token), nil
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

// ReconcilePresence aligns each agent cluster's persisted status with the live
// presence (live tunnel or fresh heartbeat). Run periodically so a cluster flips
// to "offline" when its agent drops, and back to "online" when it returns.
func (s *AgentService) ReconcilePresence(ctx context.Context) error {
	clusters, _, err := s.clusters.List(ctx, pagination.Page{Number: 1, Size: 1000})
	if err != nil {
		return err
	}
	for _, c := range clusters {
		if c.ConnectionMode != cluster.ModeAgent || c.AgentID == "" {
			continue
		}
		online := s.presence.Online(c.AgentID)
		switch {
		case online && c.AgentStatus != cluster.AgentOnline:
			c.MarkAgentSeen()
		case !online && c.AgentStatus == cluster.AgentOnline:
			c.MarkAgentOffline()
		default:
			continue
		}
		if err := s.clusters.Update(ctx, c); err != nil {
			return err
		}
	}
	return nil
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

// ─── install scripts ──────────────────────────────────────────────────────────

const composeMTLS = `version: "3.8"
services:
  agent:
    image: hivemind/agent:latest
    deploy:
      mode: global
      restart_policy: { condition: any }
    environment:
      HIVEMIND_HUB_ADDR: "${HIVEMIND_HUB_ADDR}"
      HIVEMIND_CLIENT_CERT_FILE: /run/secrets/hivemind_client_cert
      HIVEMIND_CLIENT_KEY_FILE: /run/secrets/hivemind_client_key
      HIVEMIND_CA_CERT_FILE: /run/secrets/hivemind_ca_cert
    secrets: [hivemind_client_cert, hivemind_client_key, hivemind_ca_cert]
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
secrets:
  hivemind_client_cert: { external: true }
  hivemind_client_key: { external: true }
  hivemind_ca_cert: { external: true }
`

const composeToken = `version: "3.8"
services:
  agent:
    image: hivemind/agent:latest
    deploy:
      mode: global
      restart_policy: { condition: any }
    environment:
      HIVEMIND_SERVER: "${HIVEMIND_SERVER}"
      HIVEMIND_ENROLL_TOKEN: "${HIVEMIND_ENROLL_TOKEN}"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
`

func renderMTLSInstall(hubAddr, certPEM, keyPEM, caPEM string) string {
	return fmt.Sprintf(`#!/bin/sh
set -e
echo "Installing Hivemind agent (mTLS)…"
docker secret rm hivemind_client_cert hivemind_client_key hivemind_ca_cert 2>/dev/null || true
printf '%%s' '%s' | docker secret create hivemind_client_cert - >/dev/null
printf '%%s' '%s' | docker secret create hivemind_client_key  - >/dev/null
printf '%%s' '%s' | docker secret create hivemind_ca_cert     - >/dev/null
cat > /tmp/hivemind-agent.yml <<'YML'
%sYML
HIVEMIND_HUB_ADDR='%s' docker stack deploy -c /tmp/hivemind-agent.yml hivemind-agent
echo "Done — the agent will dial Hivemind over mTLS."
`, certPEM, keyPEM, caPEM, composeMTLS, hubAddr)
}

func renderTokenInstall(serverURL, token string) string {
	return fmt.Sprintf(`#!/bin/sh
set -e
echo "Installing Hivemind agent…"
cat > /tmp/hivemind-agent.yml <<'YML'
%sYML
HIVEMIND_SERVER='%s' HIVEMIND_ENROLL_TOKEN='%s' docker stack deploy -c /tmp/hivemind-agent.yml hivemind-agent
echo "Done — the agent will dial %s."
`, composeToken, serverURL, token, serverURL)
}
