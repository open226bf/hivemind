package monitoring

import (
	"fmt"
	"strings"
)

// CrashLoopThreshold is the number of recent restarts of a task's slot above
// which it is treated as crash-looping (critical) rather than a one-off failure.
const CrashLoopThreshold = 3

// Classify maps a swarm task's raw state to a normalised Severity and a
// human-readable reason. It is the single place that encodes the Docker Swarm
// task taxonomy, so the collectors (direct/agent) and the alert engine don't
// repeat it.
//
// currentState/desiredState are the raw lowercase Swarm strings (e.g. "running",
// "failed", "pending", "shutdown"). restarts is the number of recent restarts
// observed for the task's slot — a crash-loop signal the collector tracks; pass
// 0 when unknown. errMsg is the task's status error, if any.
func Classify(currentState, desiredState, errMsg string, restarts int) (Severity, string) {
	// Intentionally stopping or being replaced (rolling update, scale-down): the
	// orchestrator wants this task gone, so it is expected churn, not an issue.
	if desiredState == "shutdown" || desiredState == "remove" {
		return SeverityOK, ""
	}

	// A slot that keeps dying is critical even if its current task is mid-restart.
	if restarts >= CrashLoopThreshold {
		return SeverityCritical, fmt.Sprintf("crash-looping (%d restarts)", restarts)
	}

	switch currentState {
	case "running", "complete":
		return SeverityOK, ""
	case "failed":
		return SeverityCritical, reasonOr(errMsg, "task failed")
	case "rejected":
		return SeverityCritical, reasonOr(errMsg, "task rejected")
	case "orphaned":
		return SeverityCritical, reasonOr(errMsg, "node lost (orphaned)")
	case "shutdown":
		// Desired running but shut down → it went away unexpectedly.
		return SeverityWarning, reasonOr(errMsg, "shut down")
	}

	// new/allocated/pending/assigned/accepted/preparing/ready/starting: the task
	// is converging. With an error it is usually stuck unschedulable (e.g.
	// "no suitable node") → critical; otherwise it is just coming up → warning.
	if isConverging(currentState) {
		if strings.TrimSpace(errMsg) != "" {
			return SeverityCritical, errMsg
		}
		return SeverityWarning, "starting"
	}

	return SeverityUnknown, reasonOr(errMsg, currentState)
}

func isConverging(state string) bool {
	switch state {
	case "new", "allocated", "pending", "assigned", "accepted", "preparing", "ready", "starting":
		return true
	}
	return false
}

func reasonOr(msg, fallback string) string {
	if strings.TrimSpace(msg) != "" {
		return msg
	}
	return fallback
}

// Recount recomputes the OK/Warning/Critical rollup from Containers. Collectors
// call it after filling Containers so the UI can badge a node without rescanning.
func (n *NodeHealth) Recount() {
	n.OK, n.Warning, n.Critical = 0, 0, 0
	for _, c := range n.Containers {
		switch c.Verdict {
		case SeverityOK:
			n.OK++
		case SeverityWarning:
			n.Warning++
		case SeverityCritical:
			n.Critical++
		}
	}
}

// Worst returns the highest severity present across the node's containers, for a
// single node badge. Critical > Warning > OK; Unknown only when nothing else.
func (n NodeHealth) Worst() Severity {
	if n.Critical > 0 {
		return SeverityCritical
	}
	if n.Warning > 0 {
		return SeverityWarning
	}
	if n.OK > 0 {
		return SeverityOK
	}
	return SeverityUnknown
}

// Struggling returns every container across the cluster whose verdict is not OK
// — the "what is struggling, and where" list that drives the health view and the
// event-driven alert rules.
func (c ClusterHealth) Struggling() []ContainerHealth {
	var out []ContainerHealth
	for _, n := range c.Nodes {
		for _, ch := range n.Containers {
			if ch.Verdict != SeverityOK {
				out = append(out, ch)
			}
		}
	}
	return out
}
