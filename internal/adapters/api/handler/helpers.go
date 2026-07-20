package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/domainerrors"
	"github.com/open226bf/hivemind/pkg/pagination"
)

// writeError maps the cross-cutting use-case errors that every resource handler
// shares to their HTTP responses: not-found (with the caller's resource-specific
// message), conflict, forbidden, cluster mismatch, orchestrator unavailable, and
// the 500 fallback. Handlers handle their own domain-specific validation (422)
// and any extra sentinels first, then delegate here for the rest — so this
// mapping lives in one place instead of in a switch per handler.
func writeError(c *gin.Context, err error, notFound string) {
	switch {
	case errors.Is(err, domainerrors.ErrNotFound), errors.Is(err, ports.ErrSwarmServiceNotFound):
		dto.Abort(c, http.StatusNotFound, dto.CodeNotFound, notFound)
	case errors.Is(err, domainerrors.ErrConflict), errors.Is(err, application.ErrClusterMismatch):
		dto.Abort(c, http.StatusConflict, dto.CodeConflict, err.Error())
	case errors.Is(err, domainerrors.ErrForbidden):
		dto.Abort(c, http.StatusForbidden, dto.CodeForbidden, err.Error())
	case errors.Is(err, application.ErrOrchestratorUnavailable):
		dto.Abort(c, http.StatusServiceUnavailable, dto.CodeInternal, err.Error())
	default:
		dto.Abort(c, http.StatusInternalServerError, dto.CodeInternal, "internal error")
	}
}

// parseUUID parses a named URL parameter as a UUID. It writes a 400 response
// and returns false if the value is not a valid UUID.
func parseUUID(c *gin.Context, param string) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid "+param+": must be a valid UUID")
		return uuid.Nil, false
	}
	return id, true
}

// parseUUIDValue parses an arbitrary string (e.g. a request-body field) as a
// UUID. It writes a 400 response and returns false if the value is invalid.
func parseUUIDValue(c *gin.Context, value, field string) (uuid.UUID, bool) {
	id, err := uuid.Parse(value)
	if err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid "+field+": must be a valid UUID")
		return uuid.Nil, false
	}
	return id, true
}

// clusterIDString renders a cluster id for API responses: the zero UUID (the
// default cluster) becomes an empty string so it is omitted from the payload.
func clusterIDString(id uuid.UUID) string {
	if id == uuid.Nil {
		return ""
	}
	return id.String()
}

// currentCluster returns the active cluster resolved by middleware.ClusterContext
// (from the X-Hivemind-Cluster header, falling back to the `cluster_id` query).
// It is the single source of cluster scope for both list and create handlers, so
// the active cluster selected in the UI drives every request without per-call
// plumbing. Absent or malformed → zero UUID (default cluster / no filter).
func currentCluster(c *gin.Context) uuid.UUID {
	if v, ok := c.Get(middleware.ClusterContextKey); ok {
		if id, ok := v.(uuid.UUID); ok {
			return id
		}
	}
	return uuid.Nil
}

// writeCluster returns the cluster a newly created resource should attach to:
// the active cluster, or the platform default when none is selected (resolved by
// middleware.ClusterContext). Unlike currentCluster it avoids the zero UUID, so
// creates never persist a NULL cluster_id — keeping the resource visible when
// that cluster is later selected and subject to per-cluster name uniqueness. It
// falls back to currentCluster if the write scope was not set (non-write route).
func writeCluster(c *gin.Context) uuid.UUID {
	if v, ok := c.Get(middleware.ClusterWriteContextKey); ok {
		if id, ok := v.(uuid.UUID); ok && id != uuid.Nil {
			return id
		}
	}
	return currentCluster(c)
}

// parsePage reads the `page` and `size` query parameters and returns a
// validated Page. Defaults: page=1, size=20, max size=100.
func parsePage(c *gin.Context) pagination.Page {
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	size, _ := strconv.Atoi(c.DefaultQuery("size", "20"))
	return pagination.New(page, size)
}

// resolveOrchestrator resolves the orchestrator for the cluster named by the
// optional `cluster_id` query parameter, defaulting to the platform's default
// cluster when absent. It writes the appropriate error response and returns
// false on a bad id or an unreachable target.
func resolveOrchestrator(c *gin.Context, registry ports.OrchestratorRegistry) (ports.Orchestrator, bool) {
	if registry == nil {
		dto.Abort(c, http.StatusServiceUnavailable, dto.CodeInternal, "orchestrator not configured")
		return nil, false
	}

	orch, err := registry.For(c.Request.Context(), currentCluster(c))
	if err != nil || orch == nil {
		dto.Abort(c, http.StatusServiceUnavailable, dto.CodeInternal, "cluster orchestrator unavailable")
		return nil, false
	}
	return orch, true
}

// resolveCollector resolves the telemetry collector for the active cluster (the
// X-Hivemind-Cluster header, default cluster when absent). It writes the
// appropriate error response and returns false when monitoring is unconfigured
// or the cluster cannot provide telemetry (e.g. stub backend, or an agent-mode
// cluster before the agent collector exists).
func resolveCollector(c *gin.Context, registry ports.TelemetryCollectorRegistry) (ports.TelemetryCollector, bool) {
	if registry == nil {
		dto.Abort(c, http.StatusServiceUnavailable, dto.CodeInternal, "monitoring not configured")
		return nil, false
	}

	col, err := registry.For(c.Request.Context(), currentCluster(c))
	if err != nil || col == nil {
		dto.Abort(c, http.StatusServiceUnavailable, dto.CodeInternal, "cluster telemetry unavailable")
		return nil, false
	}
	return col, true
}
