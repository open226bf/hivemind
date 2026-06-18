package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/monitoring"
	"github.com/open226bf/hivemind/internal/domain/user"
	"github.com/open226bf/hivemind/internal/ports"
)

// MonitoringHandler serves the cluster observability endpoints. It resolves the
// telemetry collector for the active cluster from the registry, mirroring how
// the network/volume handlers resolve the orchestrator.
type MonitoringHandler struct {
	collectors ports.TelemetryCollectorRegistry
	alerts     *application.AlertEngine
}

func NewMonitoringHandler(collectors ports.TelemetryCollectorRegistry, alerts *application.AlertEngine) *MonitoringHandler {
	return &MonitoringHandler{collectors: collectors, alerts: alerts}
}

// Register wires the monitoring routes (read-only, Viewer and up).
func (h *MonitoringHandler) Register(protected *gin.RouterGroup) {
	m := protected.Group("/monitoring")
	m.GET("/health", middleware.RequireRole(user.RoleViewer), h.ClusterHealth)
	m.GET("/alerts", middleware.RequireRole(user.RoleViewer), h.Alerts)
}

// ClusterHealth godoc
//
//	@Summary		Cluster health (per-node container health)
//	@Description	Per-node health snapshot of the active cluster: every task/container with a normalised verdict (ok/warning/critical), grouped by node, plus a rollup. Cluster-wide in both connection modes.
//	@Tags			monitoring
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{object}	dto.ClusterHealthResponse
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Failure		503	{object}	dto.ErrorResponse	"cluster telemetry unavailable"
//	@Router			/monitoring/health [get]
func (h *MonitoringHandler) ClusterHealth(c *gin.Context) {
	col, ok := resolveCollector(c, h.collectors)
	if !ok {
		return
	}
	snap, err := col.CollectHealth(c.Request.Context())
	if err != nil {
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "failed to collect cluster health")
		return
	}
	c.JSON(http.StatusOK, toClusterHealthResponse(snap, col.Capabilities()))
}

// Alerts godoc
//
//	@Summary		Active alerts
//	@Description	The alerts currently firing across clusters, produced by the event-driven alert engine (failed/crash-looping containers, unreachable nodes).
//	@Tags			monitoring
//	@Security		BearerAuth
//	@Produce		json
//	@Success		200	{object}	dto.AlertListResponse
//	@Failure		401	{object}	dto.ErrorResponse
//	@Failure		403	{object}	dto.ErrorResponse
//	@Router			/monitoring/alerts [get]
func (h *MonitoringHandler) Alerts(c *gin.Context) {
	var alerts []monitoring.Alert
	if h.alerts != nil {
		alerts = h.alerts.ActiveAlerts()
	}
	resp := dto.AlertListResponse{Items: make([]dto.AlertResponse, len(alerts)), Total: len(alerts)}
	for i, a := range alerts {
		resp.Items[i] = toAlertResponse(a)
	}
	c.JSON(http.StatusOK, resp)
}

func toAlertResponse(a monitoring.Alert) dto.AlertResponse {
	svc := ""
	if a.ServiceID != nil {
		svc = a.ServiceID.String()
	}
	return dto.AlertResponse{
		ID:          a.ID.String(),
		Severity:    string(a.Severity),
		Kind:        a.Labels["kind"],
		ClusterID:   clusterIDString(a.ClusterID),
		ServiceID:   svc,
		NodeID:      a.NodeID,
		ContainerID: a.ContainerID,
		Summary:     a.Summary,
		Detail:      a.Detail,
		FiredAt:     a.FiredAt,
	}
}

func toClusterHealthResponse(h *monitoring.ClusterHealth, caps ports.CollectorCapabilities) dto.ClusterHealthResponse {
	resp := dto.ClusterHealthResponse{
		ClusterID:       clusterIDString(h.ClusterID),
		ObservedAt:      h.ObservedAt,
		MetricsCoverage: string(caps.MetricsCoverage),
		Nodes:           make([]dto.NodeHealthResponse, len(h.Nodes)),
	}
	for i, n := range h.Nodes {
		nr := dto.NodeHealthResponse{
			NodeID:      n.NodeID,
			Hostname:    n.Hostname,
			Role:        n.Role,
			Reachable:   n.Reachable,
			TunnelUp:    n.TunnelUp,
			CPUs:        n.CPUs,
			MemoryBytes: n.MemoryBytes,
			Worst:       string(n.Worst()),
			OK:          n.OK,
			Warning:     n.Warning,
			Critical:    n.Critical,
			Containers:  make([]dto.ContainerHealthResponse, len(n.Containers)),
		}
		for j, ch := range n.Containers {
			nr.Containers[j] = dto.ContainerHealthResponse{
				TaskID:      ch.TaskID,
				ContainerID: ch.ContainerID,
				ServiceID:   clusterIDString(ch.ServiceID),
				ServiceName: ch.ServiceName,
				Slot:        ch.Slot,
				State:       ch.State,
				Verdict:     string(ch.Verdict),
				Reason:      ch.Reason,
				Restarts:    ch.Restarts,
				ExitCode:    ch.ExitCode,
				Since:       ch.Since,
			}
		}
		resp.Nodes[i] = nr
	}
	return resp
}
