package notify

import (
	"context"
	"errors"

	"github.com/orange/hivemind/internal/domain/monitoring"
	"github.com/orange/hivemind/internal/ports"
)

// MultiRouter fans an alert out to several routers, attempting every one even if
// some fail (their errors are joined). Lets the engine log AND post to a webhook
// from a single AlertRouter.
type MultiRouter struct{ routers []ports.AlertRouter }

func NewMultiRouter(routers ...ports.AlertRouter) MultiRouter {
	return MultiRouter{routers: routers}
}

func (m MultiRouter) Route(ctx context.Context, a monitoring.Alert) error {
	var errs []error
	for _, r := range m.routers {
		if r == nil {
			continue
		}
		if err := r.Route(ctx, a); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

var _ ports.AlertRouter = MultiRouter{}
