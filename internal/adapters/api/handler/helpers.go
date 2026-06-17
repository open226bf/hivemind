package handler

import (
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
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

// parseOptionalCluster parses an optional cluster id supplied in a request body
// field. An empty value maps to the zero UUID (the default cluster). It writes a
// 400 and returns false on a malformed id.
func parseOptionalCluster(c *gin.Context, raw string) (uuid.UUID, bool) {
	if raw == "" {
		return uuid.Nil, true
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid cluster: must be a valid UUID")
		return uuid.Nil, false
	}
	return id, true
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

	clusterID := uuid.Nil
	if raw := c.Query("cluster_id"); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			dto.Abort(c, http.StatusBadRequest, dto.CodeValidation, "invalid cluster_id: must be a valid UUID")
			return nil, false
		}
		clusterID = id
	}

	orch, err := registry.For(c.Request.Context(), clusterID)
	if err != nil || orch == nil {
		dto.Abort(c, http.StatusServiceUnavailable, dto.CodeInternal, "cluster orchestrator unavailable")
		return nil, false
	}
	return orch, true
}
