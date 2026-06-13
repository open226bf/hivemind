package handler_test

import (
	"testing"

	"github.com/gin-gonic/gin"

	"github.com/orange/hivemind/internal/adapters/api/handler"
	"github.com/orange/hivemind/internal/application"
)

// TestRouteRegistration_NoWildcardConflict ensures the service and network
// handlers can both register their routes under /services/:id without Gin
// panicking over conflicting wildcards. Handlers are built with nil repos —
// only route registration is exercised, never the handler bodies.
func TestRouteRegistration_NoWildcardConflict(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	g := r.Group("/api/v1")

	defer func() {
		if rec := recover(); rec != nil {
			t.Fatalf("route registration panicked: %v", rec)
		}
	}()

	handler.NewServiceHandler(application.NewServiceService(nil, nil)).Register(g)
	handler.NewNetworkHandler(application.NewNetworkService(nil, nil)).Register(g)
	handler.NewSecretHandler(application.NewSecretService(nil, nil)).Register(g)
	handler.NewConfigHandler(application.NewConfigService(nil, nil)).Register(g)
	handler.NewDeploymentHandler(application.NewDeploymentService(nil, nil, nil, nil, nil, nil, nil)).Register(g)
}
