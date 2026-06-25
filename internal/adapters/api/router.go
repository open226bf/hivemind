package api

import (
	"log/slog"
	"net/http"
	"os"

	"github.com/gin-gonic/gin"
	swaggerfiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"gorm.io/gorm"

	"github.com/open226bf/hivemind/internal/adapters/agenthub"
	"github.com/open226bf/hivemind/internal/adapters/api/handler"
	"github.com/open226bf/hivemind/internal/adapters/api/middleware"
	"github.com/open226bf/hivemind/internal/adapters/api/web"
	"github.com/open226bf/hivemind/internal/adapters/auth"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/domain/user"
	"github.com/open226bf/hivemind/internal/ports"

	// Import generated docs (produced by `swag init`).
	_ "github.com/open226bf/hivemind/docs"
)

type Dependencies struct {
	DB          *gorm.DB
	Tokens      ports.TokenService
	Auth        *application.AuthService
	Users       *application.UserService
	Services    *application.ServiceService
	Discovery   *application.DiscoveryService
	Hives       *application.HiveService
	Networks    *application.NetworkService
	Volumes     *application.VolumeService
	Secrets     *application.SecretService
	Configs     *application.ConfigService
	Templates   *application.TemplateService
	Deployments *application.DeploymentService
	Snapshots   *application.SnapshotService
	Cluster     *application.ClusterService
	Agent       *application.AgentService
	AgentHub    *agenthub.Hub
	Registry    ports.OrchestratorRegistry
	Collectors  ports.TelemetryCollectorRegistry
	Alerts      *application.AlertEngine
	AuditLog    ports.AuditLogRepository
	// Acl manages fine-grained access grants (ADR 0003). AclEnforced flips the
	// access middlewares from shadow mode to real enforcement. TokenVersions
	// reads a user's revocation epoch for immediate grant revocation.
	Acl           *application.AclService
	AclEnforced   bool
	TokenVersions middleware.TokenVersionReader
	// WSTickets mints single-use tickets that authenticate the exec WebSocket
	// upgrade without putting the access token in the URL.
	WSTickets *auth.TicketStore
	// StreamTickets mints single-use tickets for the SSE status stream. A
	// separate store from WSTickets so a Viewer-issued stream ticket can never be
	// replayed against the Admin-only exec endpoint.
	StreamTickets *auth.TicketStore
	// BaseURL is the canonical external URL (HIVEMIND_BASE_URL) used to render
	// agent install/deploy commands.
	BaseURL string
	// BasePath is the URL prefix (HIVEMIND_BASE_PATH) the SPA is served under when
	// a reverse proxy hosts Hivemind on a sub-path; injected into the SPA's base
	// href. Empty serves at the root.
	BasePath string
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
	// Resolve the active cluster (X-Hivemind-Cluster header) once per request so
	// every handler scopes reads and writes to the cluster selected in the UI.
	// The cluster service resolves the default cluster for header-less writes.
	protected.Use(middleware.ClusterContext(deps.Cluster))

	// ─── Fine-grained ACL (ADR 0003) ─────────────────────────────────────────
	// CheckRevocation rejects tokens minted before a grant change; InjectListScope
	// bounds every list to the caller's authorized resources. Both no-op for
	// admins and in shadow mode (AclEnforced=false).
	aclCfg := middleware.ACLConfig{Enforced: deps.AclEnforced}
	if deps.TokenVersions != nil {
		protected.Use(middleware.CheckRevocation(deps.TokenVersions, aclCfg))
	}
	protected.Use(middleware.InjectListScope(aclCfg))
	resolver := handler.NewAclResolver(deps.Hives, deps.Services)

	handler.NewAuthHandler(deps.Auth, deps.AclEnforced).Register(public, protected)
	handler.NewUserHandler(deps.Users).Register(protected)
	handler.NewServiceHandler(deps.Services).Register(protected, resolver, aclCfg)
	handler.NewDiscoveryHandler(deps.Discovery, aclCfg).Register(protected)
	handler.NewHiveHandler(deps.Hives).Register(protected, resolver, aclCfg)
	if deps.Acl != nil {
		handler.NewGrantHandler(deps.Acl, resolver, aclCfg).Register(protected)
	}
	handler.NewNetworkHandler(deps.Networks, deps.Registry).Register(protected)
	handler.NewVolumeHandler(deps.Volumes, deps.Registry, deps.AuditLog).Register(protected)
	handler.NewSecretHandler(deps.Secrets).Register(protected)
	handler.NewConfigHandler(deps.Configs).Register(protected)
	handler.NewTemplateHandler(deps.Templates).Register(protected)
	handler.NewDeploymentHandler(deps.Deployments).Register(protected, resolver, aclCfg)
	handler.NewSnapshotHandler(deps.Snapshots).Register(protected, resolver, aclCfg)
	handler.NewClusterHandler(deps.Cluster).Register(protected)
	handler.NewAgentHandler(deps.Agent, deps.AgentHub, deps.BaseURL).Register(public, protected)
	handler.NewMonitoringHandler(deps.Collectors, deps.Alerts).Register(protected)

	// Interactive exec (web terminal). An interactive shell is the most powerful
	// supervision capability (arbitrary code execution inside the container,
	// including any mounted secrets), so issuing a session is gated to Admin.
	// Browsers can't set headers on a WebSocket, so instead of putting the access
	// token in the URL the Admin mints a short-lived single-use ticket over a
	// normal request, then opens the socket with ?ticket=<id>. The socket handler
	// validates (and consumes) the ticket itself — no token/role middleware here.
	exec := handler.NewExecHandler(deps.Deployments, deps.WSTickets)
	protected.POST("/services/:id/exec/ticket", middleware.RequireRole(user.RoleAdmin), exec.IssueTicket)
	v1.GET("/services/:id/exec", exec.Exec)

	// ─── Live status stream (SSE) ────────────────────────────────────────────
	// Reactive replacement for client polling: the UI gets a ticket (Viewer),
	// then opens an EventSource that pushes {status, tasks} on change. Like exec,
	// the stream route validates the ticket itself (EventSource can't send
	// headers), so no token/role middleware here.
	stream := handler.NewStreamHandler(deps.Deployments, deps.StreamTickets)
	protected.POST("/services/:id/status/stream-ticket", middleware.RequireRole(user.RoleViewer), stream.IssueTicket)
	v1.GET("/services/:id/status/stream", stream.StreamStatus)

	// ─── Embedded web UI ─────────────────────────────────────────────────────
	// Serve the bundled Angular SPA from the same engine so one container exposes
	// both the API and the UI. Registered last: it only handles routes no API
	// handler matched (NoRoute), and keeps /api 404s as JSON.
	if err := web.Register(r, deps.BasePath); err != nil {
		slog.Error("register embedded web UI", "err", err)
	}

	return r
}
