package monitoring

import (
	"fmt"

	"github.com/google/uuid"
)

// Finding is a condition currently firing in a health snapshot, before it
// becomes a stateful Alert. Key uniquely and *stably* identifies the condition
// within a cluster so the engine can dedup (same key across scans = the same
// ongoing alert) and resolve (key gone = recovered). It is deliberately keyed by
// the logical instance (service/slot/node), not the ephemeral task id, so a
// crash-looping slot is one alert rather than one per restart.
type Finding struct {
	Key         string
	Kind        RuleKind
	Severity    Severity
	NodeID      string
	ContainerID string
	ServiceID   uuid.UUID
	ServiceName string
	Summary     string
	Detail      string
}

// Evaluate derives the firing findings from a cluster health snapshot — the
// event-driven rules that need no time series: every critical container (task
// failed/rejected/unschedulable, or crash-looping) and every unreachable node.
// Warnings (transient: starting/pending) are intentionally not alerted, to keep
// the signal actionable.
func Evaluate(h ClusterHealth) []Finding {
	var out []Finding
	for _, n := range h.Nodes {
		if !n.Reachable {
			out = append(out, Finding{
				Key:      "node:" + n.NodeID,
				Kind:     RuleNodeUnreachable,
				Severity: SeverityCritical,
				NodeID:   n.NodeID,
				Summary:  fmt.Sprintf("Nœud %s injoignable", nodeLabel(n)),
				Detail:   "Le nœud ne répond plus au manager (état Swarm ≠ ready).",
			})
		}
		for _, c := range n.Containers {
			if c.Verdict != SeverityCritical {
				continue
			}
			kind := RuleTaskFailed
			if c.Restarts >= CrashLoopThreshold {
				kind = RuleCrashLoop
			}
			svc := c.ServiceName
			if svc == "" {
				svc = "service inconnu"
			}
			out = append(out, Finding{
				Key:         fmt.Sprintf("ctn:%s:slot:%d:node:%s", svc, c.Slot, n.NodeID),
				Kind:        kind,
				Severity:    SeverityCritical,
				NodeID:      n.NodeID,
				ContainerID: c.ContainerID,
				ServiceID:   c.ServiceID,
				ServiceName: c.ServiceName,
				Summary:     fmt.Sprintf("%s (slot %d) sur %s : %s", svc, c.Slot, nodeLabel(n), c.State),
				Detail:      c.Reason,
			})
		}
	}
	return out
}

func nodeLabel(n NodeHealth) string {
	if n.Hostname != "" {
		return n.Hostname
	}
	return n.NodeID
}
