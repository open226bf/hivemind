package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/open226bf/hivemind/internal/adapters/api/dto"
	"github.com/open226bf/hivemind/internal/ports"
)

const claimsContextKey = "auth.claims"

// Auth validates the Bearer access token and stores the claims in the context.
func Auth(tokens ports.TokenService) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := c.GetHeader("Authorization")
		parts := strings.SplitN(header, " ", 2)
		if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			dto.Abort(c, http.StatusUnauthorized, dto.CodeUnauthorized, "missing or malformed Authorization header")
			return
		}

		claims, err := tokens.Parse(parts[1])
		if err != nil {
			dto.Abort(c, http.StatusUnauthorized, dto.CodeUnauthorized, "invalid or expired token")
			return
		}
		if claims.TokenType != ports.TokenTypeAccess {
			dto.Abort(c, http.StatusUnauthorized, dto.CodeUnauthorized, "access token required")
			return
		}

		c.Set(claimsContextKey, claims)
		c.Next()
	}
}

// ClaimsFrom retrieves the authenticated claims set by the Auth middleware.
func ClaimsFrom(c *gin.Context) (*ports.TokenClaims, bool) {
	v, ok := c.Get(claimsContextKey)
	if !ok {
		return nil, false
	}
	claims, ok := v.(*ports.TokenClaims)
	return claims, ok
}
