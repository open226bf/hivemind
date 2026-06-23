package handler

import (
	"context"

	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/application"
)

// aclResolver implements middleware.ResourceResolver by mapping a route target
// id to the (cluster, hive) coordinates the access cascade is evaluated
// against. It lives in the handler layer so the application services stay
// unaware of the HTTP middleware.
type aclResolver struct {
	hives    *application.HiveService
	services *application.ServiceService
}

// NewAclResolver builds the resolver used by middleware.RequireVerb.
func NewAclResolver(hives *application.HiveService, services *application.ServiceService) middleware.ResourceResolver {
	return aclResolver{hives: hives, services: services}
}

func (r aclResolver) Resolve(ctx context.Context, target middleware.Target, id uuid.UUID) (uuid.UUID, uuid.UUID, error) {
	switch target {
	case middleware.TargetCluster:
		return id, uuid.Nil, nil
	case middleware.TargetHive:
		h, err := r.hives.Get(ctx, id)
		if err != nil {
			return uuid.Nil, uuid.Nil, err
		}
		return h.ClusterID, h.ID, nil
	case middleware.TargetService:
		s, err := r.services.Get(ctx, id)
		if err != nil {
			return uuid.Nil, uuid.Nil, err
		}
		hive := uuid.Nil
		if s.HiveID != nil {
			hive = *s.HiveID
		}
		return s.ClusterID, hive, nil
	default:
		return uuid.Nil, uuid.Nil, nil
	}
}
