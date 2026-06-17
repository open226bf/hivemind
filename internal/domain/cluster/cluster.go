// Package cluster models an orchestration cluster that Hivemind deploys to.
//
// Hivemind started single-cluster (one ambient Docker Swarm). A Cluster makes
// the target explicit and plural: every deployable resource carries a cluster
// id, and the orchestrator registry resolves that id to a live connection.
// Type is a discriminator so the same model can later describe non-Swarm
// backends (Kubernetes, Nomad…) without reshaping the domain.
package cluster

import (
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
)

var (
	ErrInvalidName     = errors.New("cluster name must be 1–64 characters")
	ErrInvalidType     = errors.New("cluster type must be one of: swarm")
	ErrClusterNotEmpty = errors.New("cluster still has services or resources attached")
	ErrDefaultCluster  = errors.New("the default cluster cannot be removed")
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
	ID        uuid.UUID
	Name      string
	Type      Type
	Endpoint  string
	IsDefault bool
	Status    Status
	Labels    map[string]string
	TLS       TLS
	CreatedAt time.Time
	UpdatedAt time.Time
}

// New builds a cluster definition. typ defaults to swarm when empty.
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
		ID:        uuid.New(),
		Name:      name,
		Type:      typ,
		Endpoint:  strings.TrimSpace(endpoint),
		Status:    StatusUnknown,
		CreatedAt: now,
		UpdatedAt: now,
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
