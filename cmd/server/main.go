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
	"errors"
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
	"github.com/orange/hivemind/internal/adapters/persistence"
	"github.com/orange/hivemind/internal/application"
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
	userRepo := persistence.NewUserRepository(db)

	// ─── Use cases ──────────────────────────────────────────────────────────
	authSvc := application.NewAuthService(userRepo, tokens, clock.System{})

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
		DB:     db,
		Tokens: tokens,
		Auth:   authSvc,
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
