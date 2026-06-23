package application

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"

	"github.com/orange/hivemind/internal/domain/acl"
	"github.com/orange/hivemind/internal/domain/user"
	"github.com/orange/hivemind/internal/ports"
	"github.com/orange/hivemind/pkg/domainerrors"
)

// ErrSelfGrant is returned when a user tries to grant or revoke access for
// themselves — that must go through another manager (or an admin) to avoid
// self-escalation loops.
var ErrSelfGrant = errors.New("cannot manage your own access grant")

// AclService manages fine-grained access grants and resolves the effective
// scopes embedded in access tokens (ADR 0003).
type AclService struct {
	grants ports.AclRepository
	users  ports.UserRepository
	clock  ports.Clock
}

func NewAclService(grants ports.AclRepository, users ports.UserRepository, clock ports.Clock) *AclService {
	return &AclService{grants: grants, users: users, clock: clock}
}

// ScopesFor computes the effective scopes for a user. Admins bypass grants
// (nil scopes), so the token stays small and the middleware short-circuits on
// the role. Expired grants are dropped.
func (s *AclService) ScopesFor(ctx context.Context, u *user.User) ([]ports.Scope, error) {
	if u.IsAdmin() {
		return nil, nil
	}
	grants, err := s.grants.ListBySubject(ctx, u.ID)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	out := make([]ports.Scope, 0, len(grants))
	for _, g := range grants {
		if !g.Active(now) {
			continue
		}
		out = append(out, ports.Scope{Type: g.ResourceType, ID: g.ResourceID, Verb: g.Verb})
	}
	return out, nil
}

// Grant creates (or updates) an access grant for subjectID and bumps that
// user's token version so the new access takes effect immediately. granterID is
// recorded for audit. A user may not grant to themselves.
func (s *AclService) Grant(ctx context.Context, granterID, subjectID uuid.UUID, rt acl.ResourceType, resourceID uuid.UUID, verb acl.Verb, expiresAt *time.Time) (*acl.Grant, error) {
	if granterID != uuid.Nil && granterID == subjectID {
		return nil, ErrSelfGrant
	}
	// The subject must exist (clearer than a downstream FK error).
	if _, err := s.users.FindByID(ctx, subjectID); err != nil {
		return nil, err
	}
	g, err := acl.NewGrant(subjectID, rt, resourceID, verb, granterID, expiresAt, s.clock.Now())
	if err != nil {
		return nil, err
	}
	if err := s.grants.Save(ctx, g); err != nil {
		return nil, err
	}
	if err := s.bumpTokenVersion(ctx, subjectID); err != nil {
		return nil, err
	}
	return g, nil
}

// Revoke deletes a grant and bumps the affected user's token version.
func (s *AclService) Revoke(ctx context.Context, granterID, grantID uuid.UUID) error {
	g, err := s.grants.FindByID(ctx, grantID)
	if err != nil {
		return err
	}
	if granterID != uuid.Nil && granterID == g.SubjectID {
		return ErrSelfGrant
	}
	if err := s.grants.DeleteByID(ctx, grantID); err != nil {
		return err
	}
	return s.bumpTokenVersion(ctx, g.SubjectID)
}

// ListByResource returns the grants on a resource (management view).
func (s *AclService) ListByResource(ctx context.Context, rt acl.ResourceType, resourceID uuid.UUID) ([]*acl.Grant, error) {
	return s.grants.ListByResource(ctx, rt, resourceID)
}

// GrantByID fetches a single grant (used to authorize a delete against the
// grant's resource).
func (s *AclService) GrantByID(ctx context.Context, id uuid.UUID) (*acl.Grant, error) {
	return s.grants.FindByID(ctx, id)
}

// DeleteResourceGrants removes all grants on a resource — call when a cluster or
// hive is deleted so dangling grants don't linger.
func (s *AclService) DeleteResourceGrants(ctx context.Context, rt acl.ResourceType, resourceID uuid.UUID) error {
	return s.grants.DeleteByResource(ctx, rt, resourceID)
}

func (s *AclService) bumpTokenVersion(ctx context.Context, userID uuid.UUID) error {
	u, err := s.users.FindByID(ctx, userID)
	if errors.Is(err, domainerrors.ErrNotFound) {
		return nil // user vanished; nothing to revoke
	}
	if err != nil {
		return err
	}
	u.BumpTokenVersion()
	return s.users.Update(ctx, u)
}
