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
	"github.com/open226bf/hivemind/internal/ports"

	// Import generated docs (produced by `swag init`).
	_ "github.com/open226bf/hivemind/docs"
)

type Dependencies struct {
	DB     *gorm.DB
	Tokens ports.TokenService
	Auth   *application.AuthService
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

	// ─── Swagger UI (disabled in production unless explicitly enabled) ───────
	if os.Getenv("APP_ENV") != "production" || os.Getenv("SWAGGER_ENABLED") == "true" {
		r.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerfiles.Handler))
	}

	// ─── API v1 ──────────────────────────────────────────────────────────────
	v1 := r.Group("/api/v1")

	public := v1.Group("")
	protected := v1.Group("")
	protected.Use(middleware.Auth(deps.Tokens))

	handler.NewAuthHandler(deps.Auth).Register(public, protected)

	return r
}
