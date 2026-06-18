// Package notify holds ports.AlertRouter implementations. The default one logs;
// real channels (Slack/email/webhook) and external Alertmanager are later phases
// (ADR 0002).
package notify

import (
	"context"
	"log/slog"

	"github.com/open226bf/hivemind/internal/domain/monitoring"
	"github.com/open226bf/hivemind/internal/ports"
)

// LogAlertRouter is the default ports.AlertRouter: it logs alert fire/resolve
// events. It keeps the alert engine useful with zero configuration; richer
// routing plugs in by swapping this implementation.
type LogAlertRouter struct{ log *slog.Logger }

func NewLogAlertRouter(log *slog.Logger) LogAlertRouter {
	if log == nil {
		log = slog.Default()
	}
	return LogAlertRouter{log: log}
}

func (r LogAlertRouter) Route(ctx context.Context, a monitoring.Alert) error {
	level, msg := slog.LevelWarn, "alert firing"
	if a.State == monitoring.AlertResolved {
		level, msg = slog.LevelInfo, "alert resolved"
	}
	r.log.Log(ctx, level, msg,
		"severity", string(a.Severity),
		"summary", a.Summary,
		"cluster", a.ClusterID.String(),
		"kind", a.Labels["kind"],
	)
	return nil
}

var _ ports.AlertRouter = LogAlertRouter{}
