package middleware

import (
	"context"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/internal/domain/acl"
	"github.com/open226bf/hivemind/internal/domain/user"
	"github.com/open226bf/hivemind/internal/ports"
)

// ACLConfig carries the runtime ACL settings shared by the access middlewares.
// When Enforced is false the middlewares run in shadow mode: they evaluate
// access and stamp a "would-deny" marker for the audit trail, but never block.
type ACLConfig struct {
	Enforced bool
}

// Target is the kind of resource a route acts on. Unlike acl.ResourceType it
// includes services, which are authorized through their hive/cluster.
type Target string

const (
	TargetCluster Target = "cluster"
	TargetHive    Target = "hive"
	TargetService Target = "service"
)

// shadowDenyKey marks (in shadow mode) a request the ACL would have denied, so
// AuditForbidden / logs can surface it without changing the response.
const shadowDenyKey = "acl.shadow_deny"

// TokenVersionReader reads a user's current revocation epoch (one indexed read).
type TokenVersionReader interface {
	TokenVersion(ctx context.Context, id uuid.UUID) (int, error)
}

// ResourceResolver maps a route target id to the (cluster, hive) coordinates the
// access cascade is evaluated against. hive is uuid.Nil for cluster-level
// resources and hive-less services.
type ResourceResolver interface {
	Resolve(ctx context.Context, target Target, id uuid.UUID) (clusterID, hiveID uuid.UUID, err error)
}

// CheckRevocation rejects an access token whose embedded version is behind the
// user's stored token_version, making ACL revocation immediate. Runs after
// Auth. In shadow mode it never blocks.
func CheckRevocation(users TokenVersionReader, cfg ACLConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := ClaimsFrom(c)
		if !ok {
			c.Next()
			return
		}
		cur, err := users.TokenVersion(c.Request.Context(), claims.UserID)
		if err == nil && claims.TokenVer < cur {
			if cfg.Enforced {
				dto.Abort(c, http.StatusUnauthorized, dto.CodeUnauthorized, "token revoked, please re-authenticate")
				return
			}
			c.Set(shadowDenyKey, "stale_token_version")
		}
		c.Next()
	}
}

// InjectListScope stamps the per-request ACL list scope on the request context
// so list repositories filter to the caller's authorized clusters/hives. Only
// active when enforcing; admins and shadow mode get no filter (nil scope). A
// non-admin with no grants gets an empty (deny-all) scope.
func InjectListScope(cfg ACLConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !cfg.Enforced {
			c.Next()
			return
		}
		claims, ok := ClaimsFrom(c)
		if !ok || claims.Role == string(user.RoleAdmin) {
			c.Next()
			return
		}
		scope := &ports.ACLListScope{}
		for _, s := range claims.Scopes {
			switch s.Type {
			case acl.ResourceCluster:
				scope.Clusters = append(scope.Clusters, s.ID.String())
			case acl.ResourceHive:
				scope.Hives = append(scope.Hives, s.ID.String())
			}
		}
		c.Request = c.Request.WithContext(ports.WithACLListScope(c.Request.Context(), scope))
		c.Next()
	}
}

// RequireVerb authorizes the request against the user's ACL scopes: the
// effective verb on the route's resource must be at least min. Admins bypass.
//
//   - param is the URL path parameter holding the resource id. When empty, the
//     resource is the active (write) cluster from ClusterContext — used for
//     create routes that have no id yet.
//   - In shadow mode a failing check is recorded but allowed through.
func RequireVerb(target Target, param string, min acl.Verb, resolver ResourceResolver, cfg ACLConfig) gin.HandlerFunc {
	return func(c *gin.Context) {
		claims, ok := ClaimsFrom(c)
		if !ok {
			dto.Abort(c, http.StatusUnauthorized, dto.CodeUnauthorized, "authentication required")
			return
		}
		// Admins bypass grants entirely (ADR 0003).
		if claims.Role == string(user.RoleAdmin) {
			c.Next()
			return
		}

		clusterID, hiveID, ok := resolveTarget(c, target, param, resolver)
		if !ok {
			// Could not resolve (missing/invalid id or lookup error). Deny when
			// enforcing; in shadow mode let the handler return its own 404.
			if cfg.Enforced {
				dto.Abort(c, http.StatusForbidden, dto.CodeForbidden, "insufficient permissions")
				return
			}
			c.Set(shadowDenyKey, "unresolved_resource")
			c.Next()
			return
		}

		verb := ports.EffectiveVerb(claims.Scopes, clusterID, hiveID)
		if !verb.AtLeast(min) {
			if cfg.Enforced {
				dto.Abort(c, http.StatusForbidden, dto.CodeForbidden, "insufficient permissions")
				return
			}
			c.Set(shadowDenyKey, "insufficient_verb")
		}
		c.Next()
	}
}

// AuthorizeVerb authorizes a request against an already-resolved (cluster, hive)
// — for handlers whose target is not a URL path parameter (it comes from the
// request body or a lookup), so RequireVerb does not fit. It mirrors RequireVerb's
// policy: admins bypass; under enforcement an effective verb below min is a 403;
// in shadow mode the shortfall is recorded but allowed. Returns false only when
// it has denied the request (and already written the response).
func AuthorizeVerb(c *gin.Context, cfg ACLConfig, clusterID, hiveID uuid.UUID, min acl.Verb) bool {
	claims, ok := ClaimsFrom(c)
	if !ok {
		dto.Abort(c, http.StatusUnauthorized, dto.CodeUnauthorized, "authentication required")
		return false
	}
	if claims.Role == string(user.RoleAdmin) {
		return true
	}
	if ports.EffectiveVerb(claims.Scopes, clusterID, hiveID).AtLeast(min) {
		return true
	}
	if cfg.Enforced {
		dto.Abort(c, http.StatusForbidden, dto.CodeForbidden, "insufficient permissions")
		return false
	}
	c.Set(shadowDenyKey, "insufficient_verb")
	return true
}

// resolveTarget computes the (cluster, hive) the effective verb is evaluated
// against. Returns ok=false when it cannot be determined.
func resolveTarget(c *gin.Context, target Target, param string, resolver ResourceResolver) (uuid.UUID, uuid.UUID, bool) {
	// Create routes (no id): the resource is the active write cluster.
	if param == "" {
		cid := writeClusterFromContext(c)
		if cid == uuid.Nil {
			return uuid.Nil, uuid.Nil, false
		}
		return cid, uuid.Nil, true
	}

	id, err := uuid.Parse(c.Param(param))
	if err != nil {
		return uuid.Nil, uuid.Nil, false
	}
	if resolver == nil {
		return uuid.Nil, uuid.Nil, false
	}
	clusterID, hiveID, err := resolver.Resolve(c.Request.Context(), target, id)
	if err != nil {
		return uuid.Nil, uuid.Nil, false
	}
	return clusterID, hiveID, true
}

// writeClusterFromContext mirrors handler.writeCluster without importing the
// handler package: the concrete cluster a create should attach to.
func writeClusterFromContext(c *gin.Context) uuid.UUID {
	if v, ok := c.Get(ClusterWriteContextKey); ok {
		if id, ok := v.(uuid.UUID); ok && id != uuid.Nil {
			return id
		}
	}
	if v, ok := c.Get(ClusterContextKey); ok {
		if id, ok := v.(uuid.UUID); ok {
			return id
		}
	}
	return uuid.Nil
}
