// Package acl models fine-grained access grants on top of the global RBAC
// roles. A grant ties a subject (a user) to a resource (a cluster or a hive)
// with a verb (read < write < manage). Cluster grants cascade to the hives
// beneath them; admins bypass grants entirely (decided in ADR 0003).
package acl

import (
	"errors"
	"time"

	"github.com/google/uuid"
)

// Verb is an ordered access level. read < write < manage.
type Verb string

const (
	// VerbRead allows GET (list/get) of in-scope resources.
	VerbRead Verb = "read"
	// VerbWrite allows CRUD of services/deployments and editing the resource.
	VerbWrite Verb = "write"
	// VerbManage allows everything in write plus granting/revoking access on the
	// resource (owner delegation) and deleting it.
	VerbManage Verb = "manage"
)

// ResourceType is the kind of resource a grant attaches to.
type ResourceType string

const (
	ResourceCluster ResourceType = "cluster"
	ResourceHive    ResourceType = "hive"
)

var (
	ErrInvalidVerb         = errors.New("invalid acl verb")
	ErrInvalidResourceType = errors.New("invalid acl resource type")
	ErrInvalidSubject      = errors.New("invalid acl subject")
	ErrInvalidResource     = errors.New("invalid acl resource id")
)

// IsValid reports whether v is one of the known verbs.
func (v Verb) IsValid() bool {
	return v == VerbRead || v == VerbWrite || v == VerbManage
}

// Rank orders the verbs so they can be compared: read=1, write=2, manage=3.
// An unknown verb ranks 0 (below read), so it never accidentally grants access.
func (v Verb) Rank() int {
	switch v {
	case VerbRead:
		return 1
	case VerbWrite:
		return 2
	case VerbManage:
		return 3
	default:
		return 0
	}
}

// AtLeast reports whether v grants at least the access of min.
func (v Verb) AtLeast(min Verb) bool {
	return v.Rank() >= min.Rank()
}

// MaxVerb returns the higher-ranked of two verbs (used to combine a cluster
// grant with a more specific hive grant during cascade resolution).
func MaxVerb(a, b Verb) Verb {
	if a.Rank() >= b.Rank() {
		return a
	}
	return b
}

// IsValid reports whether rt is a known resource type.
func (rt ResourceType) IsValid() bool {
	return rt == ResourceCluster || rt == ResourceHive
}

// Grant is an access grant: subject may act on the resource at the given verb.
type Grant struct {
	ID           uuid.UUID
	SubjectID    uuid.UUID
	ResourceType ResourceType
	ResourceID   uuid.UUID
	Verb         Verb
	CreatedBy    uuid.UUID
	CreatedAt    time.Time
	// ExpiresAt, when set, makes the grant time-bound: it is ignored once the
	// instant has passed.
	ExpiresAt *time.Time
}

// NewGrant validates the inputs and builds a Grant with a fresh id.
func NewGrant(subjectID uuid.UUID, rt ResourceType, resourceID uuid.UUID, verb Verb, createdBy uuid.UUID, expiresAt *time.Time, now time.Time) (*Grant, error) {
	if subjectID == uuid.Nil {
		return nil, ErrInvalidSubject
	}
	if !rt.IsValid() {
		return nil, ErrInvalidResourceType
	}
	if resourceID == uuid.Nil {
		return nil, ErrInvalidResource
	}
	if !verb.IsValid() {
		return nil, ErrInvalidVerb
	}
	return &Grant{
		ID:           uuid.New(),
		SubjectID:    subjectID,
		ResourceType: rt,
		ResourceID:   resourceID,
		Verb:         verb,
		CreatedBy:    createdBy,
		CreatedAt:    now,
		ExpiresAt:    expiresAt,
	}, nil
}

// Active reports whether the grant is currently in force at the given instant
// (i.e. it has no expiry, or its expiry is in the future).
func (g *Grant) Active(now time.Time) bool {
	return g.ExpiresAt == nil || now.Before(*g.ExpiresAt)
}
