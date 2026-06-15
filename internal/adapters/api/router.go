package api

import (
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"gorm.io/gorm"

	"github.com/open226bf/hivemind/internal/adapters/api/handler"
	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/user"
	"github.com/open226bf/hivemind/internal/ports"

	// Import generated docs (produced by `swag init`).
	_ "github.com/open226bf/hivemind/docs"
)

type Dependencies struct {
	DB           *gorm.DB
	Tokens       ports.TokenService
	Auth         *application.AuthService
	Users        *application.UserService
	Services     *application.ServiceService
	Networks     *application.NetworkService
	Secrets      *application.SecretService
	Configs      *application.ConfigService
	Deployments  *application.DeploymentService
	Orchestrator ports.Orchestrator
	AuditLog     ports.AuditLogRepository
}

// NewRouter builds the Gin engine with health endpoints and the /api/v1 group.
func NewRouter(deps Dependencies) *gin.Engine {
	r := gin.New()
	r.Use(gin.Recovery())

	// ─── Health ─────────────────────────────────────────────────────────────
	r.GET("/healthz", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})
	r.GET("/readyz", func(c *gin.Context) {
		sqlDB, err := deps.DB.DB()
		if err != nil || sqlDB.PingContext(c.Request.Context()) != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"status": "db unavailable"})
			return
		}
		c.JSON(http.StatusOK, gin.H{"status": "ok"})
	})

	if os.Getenv("APP_ENV") != "production" || os.Getenv("SWAGGER_ENABLED") == "true" {
		r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerfiles.Handler))
	}

	// ─── API v1 ──────────────────────────────────────────────────────────────
	v1 := r.Group("/api/v1")

	public := v1.Group("")
	protected := v1.Group("")
	// AuditForbidden runs first so it can observe the final status and journal
	// every 403 (F-V1-01). Auth then populates the claims it reads.
	if deps.AuditLog != nil {
		protected.Use(middleware.AuditForbidden(deps.AuditLog))
	}
	protected.Use(middleware.Auth(deps.Tokens))

	handler.NewAuthHandler(deps.Auth).Register(public, protected)
	handler.NewUserHandler(deps.Users).Register(protected)
	handler.NewServiceHandler(deps.Services).Register(protected)
	handler.NewNetworkHandler(deps.Networks, deps.Orchestrator).Register(protected)
	handler.NewSecretHandler(deps.Secrets).Register(protected)
	handler.NewConfigHandler(deps.Configs).Register(protected)
	handler.NewDeploymentHandler(deps.Deployments).Register(protected)

	// Interactive exec (web terminal). Authenticated via a `token` query
	// parameter since browsers can't set headers on a WebSocket. The Admin-only
	// restriction is temporarily lifted — any authenticated user may attach.
	// TODO: re-enable middleware.RequireRole(user.RoleAdmin) for exec.
	wsAuth := v1.Group("")
	wsAuth.Use(middleware.AuthFromQuery(deps.Tokens), middleware.RequireRole(user.RoleViewer))
	wsAuth.GET("/services/:id/exec", handler.NewExecHandler(deps.Deployments).Exec)

	return r
}
