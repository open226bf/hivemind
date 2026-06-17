package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/pagination"
)

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
