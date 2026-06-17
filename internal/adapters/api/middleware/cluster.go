package middleware

import (
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ClusterContextKey is where the resolved active-cluster id is stashed on the
// gin context for handlers to read via handler.currentCluster.
const ClusterContextKey = "cluster.id"

// ClusterHeader is the request header carrying the active cluster the UI is
// scoped to. An interceptor sets it on every API call so both reads (lists) and
// writes (creates) target the selected cluster without per-request plumbing.
const ClusterHeader = "X-Hivemind-Cluster"

// ClusterContext resolves the active cluster for the request and stores it on
// the context. Resolution order: the X-Hivemind-Cluster header, then the legacy
// `cluster_id` query parameter. A missing or malformed value yields the zero
// UUID (the default cluster / no scope), so the middleware never rejects.
func ClusterContext() gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.GetHeader(ClusterHeader)
		if raw == "" {
			raw = c.Query("cluster_id")
		}
		if id, err := uuid.Parse(raw); err == nil {
			c.Set(ClusterContextKey, id)
		} else {
			c.Set(ClusterContextKey, uuid.Nil)
		}
		c.Next()
	}
}
