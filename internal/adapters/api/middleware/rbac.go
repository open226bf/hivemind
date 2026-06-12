package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/orange/hivemind/internal/adapters/api/dto"
	"github.com/orange/hivemind/internal/domain/user"
)

// roleRank orders roles: Viewer < Operator < Admin.
var roleRank = map[user.Role]int{
	user.RoleViewer:   1,
	user.RoleOperator: 2,
	user.RoleAdmin:    3,
}

// RequireRole ensures the authenticated user has at least the given role.
// Must run after Auth.
func RequireRole(min user.Role) gin.HandlerFunc {
	required := roleRank[min]
	return func(c *gin.Context) {
		claims, ok := ClaimsFrom(c)
		if !ok {
			dto.Abort(c, http.StatusUnauthorized, dto.CodeUnauthorized, "authentication required")
			return
		}
		if roleRank[user.Role(claims.Role)] < required {
			dto.Abort(c, http.StatusForbidden, dto.CodeForbidden, "insufficient permissions")
			return
		}
		c.Next()
	}
}
