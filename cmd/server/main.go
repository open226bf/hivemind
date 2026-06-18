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
// @externalDocs.url            https://github.com/open226bf/hivemind

package main

import (
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/joho/godotenv"

	"github.com/open226bf/hivemind/internal/adapters/agentca"
	"github.com/open226bf/hivemind/internal/adapters/agenthub"
	"github.com/open226bf/hivemind/internal/adapters/api"
	"github.com/open226bf/hivemind/internal/adapters/auth"
	"github.com/open226bf/hivemind/internal/adapters/orchestrator"
	"github.com/open226bf/hivemind/internal/adapters/persistence"
	"github.com/open226bf/hivemind/internal/application"
	"github.com/open226bf/hivemind/internal/ports"
	"github.com/open226bf/hivemind/pkg/clock"
	"github.com/open226bf/hivemind/pkg/logger"
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
	// Single-use tickets authenticate the exec WebSocket upgrade (browsers can't
	// set headers on a WebSocket; this keeps the access token out of the URL).
	wsTickets := auth.NewTicketStore(30 * time.Second)
	// Separate store for the SSE status stream so a Viewer-issued stream ticket
	// can never be replayed against the Admin-only exec endpoint.
	streamTickets := auth.NewTicketStore(30 * time.Second)

	// ─── Repositories ───────────────────────────────────────────────────────
	cipher, err := buildCipher(os.Getenv("AES_KEY"))
	if err != nil {
		log.Error("init encryption cipher", "err", err)
		os.Exit(1)
	}
	if _, isNop := cipher.(persistence.NopCipher); isNop {
		log.Warn("AES_KEY not set — secret and env values are stored UNENCRYPTED; set a 32-byte base64 key in production")
	}

	clusterRepo := persistence.NewClusterRepository(db, cipher)
	userRepo := persistence.NewUserRepository(db)
	hiveRepo := persistence.NewHiveRepository(db)
	serviceRepo := persistence.NewServiceRepository(db, cipher)
	networkRepo := persistence.NewNetworkRepository(db)
	volumeRepo := persistence.NewVolumeRepository(db)
	secretRepo := persistence.NewSecretRepository(db, cipher)
	configRepo := persistence.NewConfigRepository(db)
	templateRepo := persistence.NewTemplateRepository(db)
	deploymentRepo := persistence.NewDeploymentRepository(db)
	snapshotRepo := persistence.NewSnapshotRepository(db, cipher)
	auditRepo := persistence.NewAuditLogRepository(db)

	// ─── Cleanup orphaned deployments ────────────────────────────────────────
	if n, err := deploymentRepo.FailOrphaned(context.Background()); err != nil {
		log.Error("fail orphaned deployments", "err", err)
	} else if n > 0 {
		log.Warn("marked orphaned deployments as failed", "count", n)
	}

	// ─── Bootstrap default cluster ───────────────────────────────────────────
	// Seed the default cluster (ambient Docker env) so the orchestrator registry
	// can resolve it and pre-existing resources (zero ClusterID) target it.
	if created, err := application.EnsureDefaultCluster(context.Background(), clusterRepo, os.Getenv("DEFAULT_CLUSTER_NAME")); err != nil {
		log.Error("bootstrap default cluster", "err", err)
		os.Exit(1)
	} else if created {
		log.Info("bootstrapped default cluster")
	}
	// Backfill resources that predate multi-cluster onto the default cluster so
	// they resolve explicitly and satisfy the per-cluster name uniqueness.
	if def, err := clusterRepo.FindDefault(context.Background()); err != nil {
		log.Error("resolve default cluster for backfill", "err", err)
		os.Exit(1)
	} else if err := persistence.BackfillClusterID(db, def.ID.String()); err != nil {
		log.Error("backfill cluster_id", "err", err)
		os.Exit(1)
	}

	// ─── Agent hub + orchestrator registry ───────────────────────────────────
	hub := agenthub.New(0)
	registry := buildRegistry(context.Background(), env, log, clusterRepo, hub)

	// Internal CA: signs agent client certs (enrollment) and the hub server cert.
	agentCA, err := persistence.NewAgentCARepository(db, cipher).LoadOrCreate(context.Background())
	if err != nil {
		log.Error("init agent CA", "err", err)
		os.Exit(1)
	}
	// hubPublic is the mTLS address advertised to agents; the listener binds AGENT_HUB_ADDR.
	hubPublic := os.Getenv("AGENT_HUB_PUBLIC_ADDR")

	// ─── Use cases ──────────────────────────────────────────────────────────
	authSvc := application.NewAuthService(userRepo, tokens, clock.System{})
	userSvc := application.NewUserService(userRepo)
	serviceSvc := application.NewServiceService(serviceRepo, registry)
	hiveSvc := application.NewHiveService(hiveRepo, serviceRepo)
	networkSvc := application.NewNetworkService(networkRepo, serviceRepo)
	volumeSvc := application.NewVolumeService(volumeRepo, serviceRepo)
	secretSvc := application.NewSecretService(secretRepo, serviceRepo)
	configSvc := application.NewConfigService(configRepo, serviceRepo)
	templateSvc := application.NewTemplateService(templateRepo, serviceSvc, networkSvc)
	deploymentSvc := application.NewDeploymentService(serviceRepo, deploymentRepo, networkRepo, secretRepo, configRepo, registry, nil)
	snapshotSvc := application.NewSnapshotService(snapshotRepo, serviceRepo, networkRepo, secretRepo, configRepo, deploymentSvc)
	clusterSvc := application.NewClusterService(registry, clusterRepo, hub, serviceRepo, deploymentRepo, networkRepo, secretRepo, configRepo)
	agentSvc := application.NewAgentService(clusterRepo, hub, registry, agentCA, hubPublic, os.Getenv("AGENT_IMAGE"))

	// Periodically reconcile agent presence so cluster status flips to offline
	// when a tunnel drops (and back to online when it returns).
	go func() {
		t := time.NewTicker(15 * time.Second)
		defer t.Stop()
		for range t.C {
			if err := agentSvc.ReconcilePresence(context.Background()); err != nil {
				log.Warn("reconcile agent presence", "err", err)
			}
		}
	}()

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

	// ─── Agent hub mTLS listener (opt-in) ────────────────────────────────────
	if addr := os.Getenv("AGENT_HUB_ADDR"); addr != "" {
		startAgentHub(addr, agentCA, agentSvc, hub, log)
	}

	// ─── HTTP server ────────────────────────────────────────────────────────
	r := api.NewRouter(api.Dependencies{
		DB:            db,
		Tokens:        tokens,
		Auth:          authSvc,
		Users:         userSvc,
		Services:      serviceSvc,
		Hives:         hiveSvc,
		Networks:      networkSvc,
		Volumes:       volumeSvc,
		Secrets:       secretSvc,
		Configs:       configSvc,
		Templates:     templateSvc,
		Deployments:   deploymentSvc,
		Snapshots:     snapshotSvc,
		Cluster:       clusterSvc,
		Agent:         agentSvc,
		AgentHub:      hub,
		Registry:      registry,
		AuditLog:      auditRepo,
		WSTickets:     wsTickets,
		StreamTickets: streamTickets,
		BaseURL:       os.Getenv("HIVEMIND_BASE_URL"),
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

// buildRegistry selects how cluster orchestrators are resolved. ORCHESTRATOR=stub
// forces a static registry over the simulated orchestrator (local dev without
// Docker). Otherwise it returns a cluster-backed registry that lazily connects
// to each cluster's daemon. A probe of the ambient Docker environment keeps the
// previous ergonomics: a connection failure is fatal in production but falls
// back to the stub elsewhere, so `go run` works without a live Swarm.
func buildRegistry(ctx context.Context, env string, log *slog.Logger, clusters ports.ClusterRepository, hub ports.AgentHub) ports.OrchestratorRegistry {
	if os.Getenv("ORCHESTRATOR") == "stub" {
		return orchestrator.NewStaticRegistry(orchestrator.NewStubOrchestrator())
	}
	probe, err := orchestrator.NewSwarmOrchestrator(ctx)
	if err != nil {
		if env == "production" {
			log.Error("cannot connect to Docker Swarm", "err", err)
			os.Exit(1)
		}
		log.Warn("Docker unavailable — falling back to STUB orchestrator (set ORCHESTRATOR=stub to silence)", "err", err)
		return orchestrator.NewStaticRegistry(orchestrator.NewStubOrchestrator())
	}
	_ = probe.Close()
	log.Info("connected to Docker Swarm orchestrator")
	return orchestrator.NewRegistry(clusters, hub)
}

// startAgentHub runs the mutual-TLS listener that agents dial out to. Client
// certs are verified against the internal CA; the agent's identity is the cert
// common name (cluster id) and the serial gates revocation. On /agent/connect
// the connection is hijacked and handed to the hub as a reverse tunnel.
func startAgentHub(addr string, ca *agentca.CA, svc *application.AgentService, hub *agenthub.Hub, log *slog.Logger) {
	hosts := []string{"localhost", "127.0.0.1"}
	// Include the host agents actually dial (from AGENT_HUB_PUBLIC_ADDR) so the
	// server cert SAN matches without a separate setting.
	if pub := os.Getenv("AGENT_HUB_PUBLIC_ADDR"); pub != "" {
		host := pub
		if h, _, err := net.SplitHostPort(pub); err == nil {
			host = h
		}
		if host != "" {
			hosts = append(hosts, host)
		}
	}
	// Optional explicit extra SAN.
	if h := os.Getenv("AGENT_HUB_HOSTNAME"); h != "" {
		hosts = append(hosts, h)
	}
	serverCert, err := ca.IssueServerTLS(hosts, 365*24*time.Hour)
	if err != nil {
		log.Error("agent hub: issue server cert", "err", err)
		os.Exit(1)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agent/connect", func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "client certificate required", http.StatusUnauthorized)
			return
		}
		peer := r.TLS.PeerCertificates[0]
		clusterID, err := uuid.Parse(peer.Subject.CommonName)
		if err != nil {
			http.Error(w, "bad certificate subject", http.StatusUnauthorized)
			return
		}
		node := agentNodeFromQuery(r)
		agentID, err := svc.AuthorizeTunnel(r.Context(), clusterID, peer.SerialNumber.String(), node)
		if err != nil {
			http.Error(w, "certificate rejected", http.StatusUnauthorized)
			return
		}
		hj, ok := w.(http.Hijacker)
		if !ok {
			http.Error(w, "hijack unsupported", http.StatusInternalServerError)
			return
		}
		conn, _, err := hj.Hijack()
		if err != nil {
			return
		}
		if _, err := io.WriteString(conn, "HTTP/1.1 101 Switching Protocols\r\nUpgrade: hivemind-tunnel\r\nConnection: Upgrade\r\n\r\n"); err != nil {
			_ = conn.Close()
			return
		}
		log.Info("agent tunnel attached (mTLS)", "agent_id", agentID, "node", node.NodeID, "role", node.Role)
		if err := hub.Attach(agentID, node.NodeID, node, conn); err != nil {
			log.Warn("agent tunnel ended", "agent_id", agentID, "err", err)
		}
		_ = conn.Close()
	})

	srv := &http.Server{
		Addr:    addr,
		Handler: mux,
		TLSConfig: &tls.Config{
			Certificates: []tls.Certificate{serverCert},
			ClientCAs:    ca.Pool(),
			ClientAuth:   tls.RequireAndVerifyClientCert,
			MinVersion:   tls.VersionTLS12,
			NextProtos:   []string{"http/1.1"}, // disable h2 so we can hijack
		},
	}
	go func() {
		log.Info("agent hub (mTLS) listening", "addr", addr)
		if err := srv.ListenAndServeTLS("", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("agent hub listener error", "err", err)
		}
	}()
}

// agentNodeFromQuery reads the node identity the agent passes on the connect URL.
func agentNodeFromQuery(r *http.Request) ports.AgentNode {
	q := r.URL.Query()
	return ports.AgentNode{
		NodeID:        q.Get("node_id"),
		Hostname:      q.Get("hostname"),
		Role:          q.Get("role"),
		IsManager:     q.Get("is_manager") == "true",
		IsLeader:      q.Get("is_leader") == "true",
		EngineVersion: q.Get("engine_version"),
	}
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
