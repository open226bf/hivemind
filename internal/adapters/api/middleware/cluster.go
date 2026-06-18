package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// ClusterContextKey is where the resolved active-cluster *scope* is stashed on
// the gin context for handlers to read via handler.currentCluster. It is the
// filter scope: the zero UUID means "no scope" (lists aggregate every cluster).
const ClusterContextKey = "cluster.id"

// ClusterWriteContextKey is where the resolved *write* cluster is stashed: the
// concrete cluster a newly created resource attaches to. Unlike the scope it is
// never the zero UUID — handler.writeCluster reads it so creates never persist a
// NULL cluster_id.
const ClusterWriteContextKey = "cluster.write"

// ClusterHeader is the request header carrying the active cluster the UI is
// scoped to. An interceptor sets it on every API call so both reads (lists) and
// writes (creates) target the selected cluster without per-request plumbing.
const ClusterHeader = "X-Hivemind-Cluster"

// DefaultClusterResolver resolves the platform's default cluster id, used to
// pick a concrete write cluster when the request selects none.
type DefaultClusterResolver interface {
	DefaultClusterID(ctx context.Context) (uuid.UUID, error)
}

// ClusterContext resolves the active cluster for the request and stores both the
// filter scope and the write cluster on the context.
//
//   - scope: the X-Hivemind-Cluster header (or legacy cluster_id query). A
//     missing/malformed value yields the zero UUID — "all clusters" for lists —
//     so the middleware never rejects.
//   - write: the cluster a new resource should attach to. It mirrors the scope
//     when one is selected; when none is, it resolves to the default cluster so
//     creates land on a concrete cluster instead of a NULL/zero cluster (which
//     would be hidden once that cluster is selected explicitly, and would dodge
//     per-cluster name uniqueness). The default is only resolved for write
//     methods, so reads add no extra lookup.
func ClusterContext(defaults DefaultClusterResolver) gin.HandlerFunc {
	return func(c *gin.Context) {
		raw := c.GetHeader(ClusterHeader)
		if raw == "" {
			raw = c.Query("cluster_id")
		}
		scope := uuid.Nil
		if id, err := uuid.Parse(raw); err == nil {
			scope = id
		}
		c.Set(ClusterContextKey, scope)

		write := scope
		if write == uuid.Nil && defaults != nil && isWriteMethod(c.Request.Method) {
			if id, err := defaults.DefaultClusterID(c.Request.Context()); err == nil {
				write = id
			}
		}
		c.Set(ClusterWriteContextKey, write)

		c.Next()
	}
}

// isWriteMethod reports whether the method creates or mutates state, so the
// default cluster is resolved only when a write might need a concrete target.
func isWriteMethod(method string) bool {
	switch method {
	case http.MethodPost, http.MethodPut, http.MethodPatch:
		return true
	default:
		return false
	}
}
