package middleware

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/orange/hivemind/internal/domain/auditlog"
	"github.com/orange/hivemind/internal/ports"
)

// AuditForbidden records every request that ends in a 403 to the audit log
// (F-V1-01: "toute tentative non autorisée renvoie 403 et est journalisée").
// It must run before Auth/RequireRole so it can observe the final status code.
// The write is fire-and-forget on a background context so it never slows the
// request or fails it if the audit store is unavailable.
func AuditForbidden(audit ports.AuditLogRepository) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()

		if c.Writer.Status() != http.StatusForbidden {
			return
		}

		var userID *uuid.UUID
		if claims, ok := ClaimsFrom(c); ok {
			id := claims.UserID
			userID = &id
		}
		payload, _ := json.Marshal(map[string]string{
			"method": c.Request.Method,
			"path":   c.FullPath(),
		})
		entry := auditlog.New(userID, "access_denied", "http", c.Request.URL.Path, payload, c.ClientIP())

		go func() {
			_ = audit.Save(context.Background(), entry)
		}()
	}
}
