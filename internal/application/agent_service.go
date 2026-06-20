package application

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	clusters   ports.ClusterRepository
	presence   ports.AgentPresence
	registry   ports.OrchestratorRegistry // optional; invalidated on mode/binding change
	certs      ports.AgentCertIssuer      // optional; nil disables mTLS (dev/token mode)
	hubAddr    string                     // advertised agent-hub address (mTLS endpoint)
	agentImage string                     // agent image used in install scripts (registry path in prod)
}

func NewAgentService(clusters ports.ClusterRepository, presence ports.AgentPresence, registry ports.OrchestratorRegistry, certs ports.AgentCertIssuer, hubAddr, agentImage string) *AgentService {
	return &AgentService{clusters: clusters, presence: presence, registry: registry, certs: certs, hubAddr: hubAddr, agentImage: agentImage}
}

// Enrollment is the result of issuing an enrollment token (admin side). When the
// CA is configured it also carries the client certificate material the agent
// uses to authenticate the mTLS tunnel.
type Enrollment struct {
	ClusterID   uuid.UUID
	ClusterName string
	AgentID     string // stable agent identity to bake into the deployment
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

	// Assign a stable agent identity up front (the cluster id), baked into the
	// generated deployment as HIVEMIND_AGENT_ID. Every agent task reconnects with
	// it, so restarts and extra nodes never need to re-consume the token.
	c.AgentID = c.ID.String()

	out := &Enrollment{ClusterID: c.ID, ClusterName: c.Name, AgentID: c.AgentID, Token: token}

	// Issue a client certificate only when the mTLS hub is actually reachable
	// (CA present AND an advertised hub address). Otherwise the agent would get
	// an empty HIVEMIND_HUB_ADDR and fail — fall back to token mode instead.
	if s.mtlsEnabled() {
		certPEM, keyPEM, serial, err := s.certs.IssueClient(c.ID.String(), clientCertTTL)
		if err != nil {
			return nil, fmt.Errorf("issue client cert: %w", err)
		}
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

	if s.mtlsEnabled() {
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
		return renderMTLSInstall(s.image(), s.hubAddr, string(certPEM), string(keyPEM), string(s.certs.CertPEM()), secretRev(serial)), nil
	}

	// Token mode: ensure a stable agent id is baked in (covers clusters enrolled
	// before this became the default) so the agent reconnects without re-enrolling.
	if c.AgentID == "" {
		c.AgentID = c.ID.String()
		if err := s.clusters.Update(ctx, c); err != nil {
			return "", err
		}
	}
	return renderTokenInstall(s.image(), serverURL, token, c.AgentID), nil
}

// mtlsEnabled reports whether mutual-TLS agent enrollment is usable: a CA to
// sign client certs AND an advertised hub address for the agent to dial.
func (s *AgentService) mtlsEnabled() bool {
	return s.certs != nil && s.hubAddr != ""
}

// secretRev derives a short, Docker-secret-name-safe revision from the cert
// serial, so each rotation produces distinct secret names.
func secretRev(serial string) string {
	var b strings.Builder
	for _, r := range serial {
		if (r >= '0' && r <= '9') || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			b.WriteRune(r)
		}
		if b.Len() >= 16 {
			break
		}
	}
	if b.Len() == 0 {
		return "v1"
	}
	return b.String()
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
	// Reconnection: the agent presents its stable agent id (baked into the
	// deployment at enrollment). It is the same credential the data-plane tunnel
	// authorises on, and the token is never consumed, so this path works across
	// restarts and for every node of a global agent service.
	if in.AgentID != "" {
		c, err := s.clusters.FindByAgentID(ctx, in.AgentID)
		if err == nil {
			return s.bindSeen(ctx, c, in.AgentID, in.Node, false)
		}
		if !errors.Is(err, domainerrors.ErrNotFound) {
			return nil, err
		}
		// Unknown agent id: fall through to token enrollment below.
	}

	// Enrollment by token: resolve the cluster by token hash, verify, then bind a
	// stable agent id (the cluster id). The token is kept (reusable / revocable).
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
	agentID := c.ID.String()
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

// AuthorizeTunnelToken authenticates a token-mode data-plane tunnel before it is
// attached. The agent presents its agent id (public — the API exposes it) AND
// the reusable enrollment token (secret); both must resolve to the same enrolled
// cluster. The token is what makes the tunnel a trusted data plane: the control
// plane pushes Docker API calls — including secret and config payloads — over
// it, so authenticating on the agent id alone would let anyone who knows the
// cluster id hijack the tunnel and receive those payloads. The token match is
// constant-time (see cluster.MatchEnrollment); rotating or clearing the token
// (GenerateEnrollment / UseDirectMode) revokes existing tunnels.
func (s *AgentService) AuthorizeTunnelToken(ctx context.Context, agentID, token string) error {
	if agentID == "" || token == "" {
		return fmt.Errorf("missing agent id or tunnel token: %w", ErrInvalidEnrollment)
	}
	c, err := s.clusters.FindByAgentID(ctx, agentID)
	if err != nil {
		return fmt.Errorf("no cluster bound to agent id %q: %w", agentID, ErrInvalidEnrollment)
	}
	ok, err := c.MatchEnrollment(token)
	if err != nil || !ok {
		// Most common cause: the cluster was re-enrolled (token rotated) after the
		// agent was deployed, so the agent's baked token no longer matches. The
		// reconnect path authorises on agent id alone, so the agent still registers
		// and looks online — only the data-plane tunnel, which requires the token, is
		// refused. The wrapped reason is logged server-side; the agent sees a 401.
		return fmt.Errorf("tunnel token does not match cluster enrollment (rotated by re-enroll?): %w", ErrInvalidEnrollment)
	}
	return nil
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

// DefaultAgentImage is used when AGENT_IMAGE is not configured. It points at the
// published multi-arch image so the generated compose is pullable by every node
// out of the box. Override AGENT_IMAGE to pin a version or use a private registry.
const DefaultAgentImage = "open226/hivemind-agent:latest"

// composeMTLS renders the stack. Secrets are versioned (rev suffix) and mounted
// at stable in-container paths via target:, so re-enrolling rotates the
// certificate by deploying new secrets and updating the service (Docker secrets
// are immutable and cannot be removed while in use).
func composeMTLS(image, rev string) string {
	return fmt.Sprintf(`version: "3.8"
services:
  agent:
    image: %s
    user: root # needs the host docker.sock (root:docker)
    deploy:
      mode: global
      restart_policy: { condition: any }
    environment:
      HIVEMIND_HUB_ADDR: "${HIVEMIND_HUB_ADDR}"
      HIVEMIND_CLIENT_CERT_FILE: /run/secrets/hivemind_client_cert
      HIVEMIND_CLIENT_KEY_FILE: /run/secrets/hivemind_client_key
      HIVEMIND_CA_CERT_FILE: /run/secrets/hivemind_ca_cert
    secrets:
      - source: hivemind_client_cert_%s
        target: hivemind_client_cert
      - source: hivemind_client_key_%s
        target: hivemind_client_key
      - source: hivemind_ca_cert_%s
        target: hivemind_ca_cert
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
secrets:
  hivemind_client_cert_%s: { external: true }
  hivemind_client_key_%s: { external: true }
  hivemind_ca_cert_%s: { external: true }
`, image, rev, rev, rev, rev, rev, rev)
}

func composeToken(image string) string {
	return fmt.Sprintf(`version: "3.8"
services:
  agent:
    image: %s
    user: root # needs the host docker.sock (root:docker)
    deploy:
      mode: global
      restart_policy: { condition: any }
    environment:
      HIVEMIND_SERVER: "${HIVEMIND_SERVER}"
      HIVEMIND_ENROLL_TOKEN: "${HIVEMIND_ENROLL_TOKEN}"
      HIVEMIND_AGENT_ID: "${HIVEMIND_AGENT_ID}"
    volumes:
      - /var/run/docker.sock:/var/run/docker.sock:ro
`, image)
}

func (s *AgentService) image() string {
	if s.agentImage != "" {
		return s.agentImage
	}
	return DefaultAgentImage
}

func renderMTLSInstall(image, hubAddr, certPEM, keyPEM, caPEM, rev string) string {
	return fmt.Sprintf(`#!/bin/sh
set -e
echo "Installing Hivemind agent (mTLS)…"
printf '%%s' '%s' | docker secret create hivemind_client_cert_%s - >/dev/null
printf '%%s' '%s' | docker secret create hivemind_client_key_%s  - >/dev/null
printf '%%s' '%s' | docker secret create hivemind_ca_cert_%s     - >/dev/null
cat > /tmp/hivemind-agent.yml <<'YML'
%sYML
HIVEMIND_HUB_ADDR='%s' docker stack deploy -c /tmp/hivemind-agent.yml hivemind-agent
# Prune now-unused previous secret revisions (best effort).
for s in $(docker secret ls --format '{{.Name}}' | grep -E '^hivemind_(client_cert|client_key|ca_cert)_'); do
  docker secret rm "$s" 2>/dev/null || true
done
echo "Done — the agent will dial Hivemind over mTLS."
`, certPEM, rev, keyPEM, rev, caPEM, rev, composeMTLS(image, rev), hubAddr)
}

func renderTokenInstall(image, serverURL, token, agentID string) string {
	return fmt.Sprintf(`#!/bin/sh
set -e
echo "Installing Hivemind agent…"
cat > /tmp/hivemind-agent.yml <<'YML'
%sYML
HIVEMIND_SERVER='%s' HIVEMIND_ENROLL_TOKEN='%s' HIVEMIND_AGENT_ID='%s' docker stack deploy -c /tmp/hivemind-agent.yml hivemind-agent
echo "Done — the agent will dial %s."
`, composeToken(image), serverURL, token, agentID, serverURL)
}
