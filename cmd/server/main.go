// @title           Hivemind API
// @version         1.0
// @description     Plateforme de déploiement autonome pour Docker Swarm.
//
// @contact.name    Équipe technique Hivemind
// @contact.email   issadicko78@gmail.com
//
// @license.name    Proprietary
//
// @host            localhost:8080
// @BasePath        /api/v1
//
// @securityDefinitions.apikey  BearerAuth
// @in                          header
// @name                        Authorization
// @description                 Format: "Bearer <token>"
//
// @externalDocs.description    Cahier des charges
// @externalDocs.url            https://github.com/orange/hivemind

package main

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/joho/godotenv"

	"github.com/orange/hivemind/internal/adapters/api"
	"github.com/orange/hivemind/internal/adapters/auth"
	"github.com/orange/hivemind/internal/adapters/orchestrator"
	"github.com/orange/hivemind/internal/adapters/persistence"
	"github.com/orange/hivemind/internal/application"
	"github.com/orange/hivemind/internal/ports"
	"github.com/orange/hivemind/pkg/clock"
	"github.com/orange/hivemind/pkg/logger"
)

func main() {
	_ = godotenv.Load()

	env := os.Getenv("APP_ENV")
	if env == "" {
		env = "development"
	}

	log := logger.New(env)
	slog.SetDefault(log)

	if env == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	// ─── Database ───────────────────────────────────────────────────────────
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		log.Error("DATABASE_URL is required")
		os.Exit(1)
	}

	db, err := persistence.NewDB(persistence.DBConfig{DSN: dsn})
	if err != nil {
		log.Error("connect db", "err", err)
		os.Exit(1)
	}

	if shouldAutoMigrate(env) {
		log.Info("running auto-migration", "env", env)
		if err := persistence.Migrate(db); err != nil {
			log.Error("auto-migration failed", "err", err)
			os.Exit(1)
		}
		log.Info("auto-migration completed")
	}

	// ─── JWT / auth ─────────────────────────────────────────────────────────
	privKey, generated, err := auth.LoadOrGenerateKey(os.Getenv("JWT_PRIVATE_KEY_PATH"))
	if err != nil {
		log.Error("load signing key", "err", err)
		os.Exit(1)
	}
	if generated {
		log.Warn("using ephemeral JWT signing key — tokens will not survive a restart; set JWT_PRIVATE_KEY_PATH in production")
	}
	tokens := auth.NewTokenService(auth.Config{PrivateKey: privKey, Issuer: "hivemind"})

	// ─── Repositories ───────────────────────────────────────────────────────
	cipher, err := buildCipher(os.Getenv("AES_KEY"))
	if err != nil {
		log.Error("init encryption cipher", "err", err)
		os.Exit(1)
	}
	if _, isNop := cipher.(persistence.NopCipher); isNop {
		log.Warn("AES_KEY not set — secret and env values are stored UNENCRYPTED; set a 32-byte base64 key in production")
	}

	userRepo := persistence.NewUserRepository(db)
	serviceRepo := persistence.NewServiceRepository(db, cipher)
	networkRepo := persistence.NewNetworkRepository(db)
	secretRepo := persistence.NewSecretRepository(db, cipher)
	configRepo := persistence.NewConfigRepository(db)
	deploymentRepo := persistence.NewDeploymentRepository(db)
	auditRepo := persistence.NewAuditLogRepository(db)

	// ─── Cleanup orphaned deployments ────────────────────────────────────────
	if n, err := deploymentRepo.FailOrphaned(context.Background()); err != nil {
		log.Error("fail orphaned deployments", "err", err)
	} else if n > 0 {
		log.Warn("marked orphaned deployments as failed", "count", n)
	}

	// ─── Orchestrator ────────────────────────────────────────────────────────
	orch := buildOrchestrator(context.Background(), env, log)

	// ─── Use cases ──────────────────────────────────────────────────────────
	authSvc := application.NewAuthService(userRepo, tokens, clock.System{})
	userSvc := application.NewUserService(userRepo)
	serviceSvc := application.NewServiceService(serviceRepo, orch)
	networkSvc := application.NewNetworkService(networkRepo, serviceRepo)
	secretSvc := application.NewSecretService(secretRepo, serviceRepo)
	configSvc := application.NewConfigService(configRepo, serviceRepo)
	deploymentSvc := application.NewDeploymentService(serviceRepo, deploymentRepo, networkRepo, secretRepo, configRepo, orch, nil)
	clusterSvc := application.NewClusterService(orch, serviceRepo, deploymentRepo, networkRepo, secretRepo, configRepo)

	// ─── Bootstrap admin (F-MVP-01) ─────────────────────────────────────────
	if adminEmail := os.Getenv("ADMIN_EMAIL"); adminEmail != "" {
		created, err := application.EnsureAdmin(context.Background(), userRepo, adminEmail, os.Getenv("ADMIN_PASSWORD"))
		if err != nil {
			log.Error("bootstrap admin", "err", err)
			os.Exit(1)
		}
		if created {
			log.Info("bootstrapped initial admin account", "email", adminEmail)
		}
	}

	// ─── HTTP server ────────────────────────────────────────────────────────
	r := api.NewRouter(api.Dependencies{
		DB:           db,
		Tokens:       tokens,
		Auth:         authSvc,
		Users:        userSvc,
		Services:     serviceSvc,
		Networks:     networkSvc,
		Secrets:      secretSvc,
		Configs:      configSvc,
		Deployments:  deploymentSvc,
		Cluster:      clusterSvc,
		Orchestrator: orch,
		AuditLog:     auditRepo,
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8080"
	}

	srv := &http.Server{
		Addr:         ":" + port,
		Handler:      r,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		log.Info("hivemind starting", "port", port, "env", env)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("server error", "err", err)
			os.Exit(1)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
	}
	log.Info("hivemind stopped")
}

// buildOrchestrator selects the deployment backend. ORCHESTRATOR=stub forces
// the simulated orchestrator (useful for local dev without Docker). Otherwise
// it connects to Docker Swarm; a connection failure is fatal in production but
// falls back to the stub elsewhere.
func buildOrchestrator(ctx context.Context, env string, log *slog.Logger) ports.Orchestrator {
	if os.Getenv("ORCHESTRATOR") == "stub" {
		return orchestrator.NewStubOrchestrator()
	}
	swarm, err := orchestrator.NewSwarmOrchestrator(ctx)
	if err != nil {
		if env == "production" {
			log.Error("cannot connect to Docker Swarm", "err", err)
			os.Exit(1)
		}
		log.Warn("Docker unavailable — falling back to STUB orchestrator (set ORCHESTRATOR=stub to silence)", "err", err)
		return orchestrator.NewStubOrchestrator()
	}
	log.Info("connected to Docker Swarm orchestrator")
	return swarm
}

// buildCipher selects the at-rest encryption strategy for sensitive values
// (secret values, secret-flagged env vars). A base64-encoded 32-byte AES_KEY
// enables AES-256-GCM; an empty key falls back to a no-op cipher for
// development only.
func buildCipher(b64Key string) (persistence.Cipher, error) {
	if b64Key == "" {
		return persistence.NopCipher{}, nil
	}
	key, err := base64.StdEncoding.DecodeString(b64Key)
	if err != nil {
		return nil, fmt.Errorf("AES_KEY must be base64-encoded: %w", err)
	}
	return persistence.NewAESCipher(key)
}

// shouldAutoMigrate decides whether to run migrations on boot.
// Default: enabled outside production. The AUTO_MIGRATE env var
// (true/false) overrides this default in any environment.
func shouldAutoMigrate(env string) bool {
	if v := os.Getenv("AUTO_MIGRATE"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			return b
		}
	}
	return env != "production"
}
