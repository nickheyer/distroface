package container

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/distribution/distribution/v3/registry/handlers"
	"github.com/nickheyer/distroface/internal/admin"
	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/certs"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/registry"
	"github.com/nickheyer/distroface/internal/rpc"
	"github.com/nickheyer/distroface/internal/webhook"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/sirupsen/logrus"
)

// App holds all initialized dependencies and the HTTP server.
type App struct {
	Config          *config.Config
	Log             *logger.Logger
	Store           *stores.Store
	TokenService    *auth.TokenService
	AuthManager     *auth.Manager
	Enforcer        *rbac.Enforcer
	RegistryAccess  *registry.RegistryAccess
	PortalProxies   *portal.Manager
	CertEngine      *certs.Engine
	Server          *http.Server
	ChallengeServer *http.Server // Cleartext acme http-01 and https redirect
}

// New builds the entire application: config, logger, store, token service,
// RBAC enforcer, auth manager, registry handler, RPC server, and HTTP server.
func New() (*App, error) {
	cfg, err := config.Load(".")
	if err != nil {
		return nil, fmt.Errorf("loading configuration: %w", err)
	}

	if err := os.MkdirAll(cfg.Logging.Dir, 0755); err != nil {
		return nil, fmt.Errorf("creating log directory: %w", err)
	}

	log := logger.NewWithConfig(&logger.Config{
		Enabled:       cfg.Logging.Enabled,
		Dir:           cfg.Logging.Dir,
		DefaultModule: cfg.Logging.DefaultModule,
		MaxSize:       cfg.Logging.MaxSize,
		MaxBackups:    cfg.Logging.MaxBackups,
		MaxAge:        cfg.Logging.MaxAge,
		Compress:      cfg.Logging.Compress,
	})

	if err := os.MkdirAll(cfg.Storage.DataDir, 0755); err != nil {
		log.Close()
		return nil, fmt.Errorf("creating data directory: %w", err)
	}

	store, err := stores.NewSQLiteStore(cfg.Database.Path, stores.DBConfig{
		MaxOpenConns:    cfg.Database.MaxConnections,
		MaxIdleConns:    cfg.Database.MaxIdleConns,
		ConnMaxLifetime: time.Duration(cfg.Database.ConnMaxLifetime) * time.Second,
	})
	if err != nil {
		log.Close()
		return nil, fmt.Errorf("initializing storage: %w", err)
	}

	// Initialize Casbin RBAC enforcer
	enforcer, err := rbac.NewEnforcer(store.DB())
	if err != nil {
		store.Close()
		log.Close()
		return nil, fmt.Errorf("initializing RBAC enforcer: %w", err)
	}
	if err := enforcer.SeedDefaultPolicies(cfg.Auth.AnonymousAccess); err != nil {
		store.Close()
		log.Close()
		return nil, fmt.Errorf("seeding RBAC policies: %w", err)
	}
	log.Info("RBAC enforcer initialized")

	// Initialize Auth Manager (HS256 JWT for web sessions + API tokens)
	authManager, err := auth.NewManager(store, enforcer, &cfg.Auth)
	if err != nil {
		store.Close()
		log.Close()
		return nil, fmt.Errorf("initializing auth manager: %w", err)
	}
	log.Info("Auth manager initialized")

	if !authManager.IsAnyAuthEnabled() {
		log.Warn("SECURITY: no auth provider is enabled. Every request runs as admin, do not expose this instance")
	}

	if err := admin.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		store.Close()
		log.Close()
		return nil, fmt.Errorf("configuring trusted proxies: %w", err)
	}

	if err := Run(context.Background(), cfg.Bootstrap, store, authManager, log); err != nil {
		store.Close()
		log.Close()
		return nil, fmt.Errorf("bootstrap seeding: %w", err)
	}

	// Initialize Token Service (ECDSA keys for Docker registry JWTs - separate from HS256 sessions)
	tokenExpiry := time.Duration(cfg.Auth.TokenExpiry) * time.Second
	tokenService, err := auth.NewTokenService(cfg.Storage.DataDir, "distroface", "distroface-registry", tokenExpiry)
	if err != nil {
		store.Close()
		log.Close()
		return nil, fmt.Errorf("initializing token service: %w", err)
	}
	log.Info("Token service initialized (cert: %s)", tokenService.CertPath())

	// Create logger module for registry, set logrus out to reg logger
	registryLog := log.Module("distroface-registry")
	logrus.SetOutput(registryLog)
	logrus.SetLevel(logrus.InfoLevel)
	logrus.SetFormatter(&logrus.TextFormatter{
		DisableColors:    true,
		DisableTimestamp: true,
	})

	if err := os.MkdirAll(cfg.Registry.StoragePath, 0755); err != nil {
		store.Close()
		log.Close()
		return nil, fmt.Errorf("creating registry storage directory: %w", err)
	}

	dispatcher := webhook.NewDispatcher(store, registryLog, cfg.Webhooks.AllowPrivateNetworks)

	registry.RegisterListenerMiddleware(store, registryLog, dispatcher)

	registryCfg := registry.BuildConfig(cfg.Registry.StoragePath, tokenService.CertPath(), cfg.Server.Host, cfg.Server.Port)
	registryApp := handlers.NewApp(context.Background(), registryCfg)
	registryLog.Info("Distribution v3 initialized")

	registryAccess, err := registry.NewRegistryAccess(cfg.Registry.StoragePath)
	if err != nil {
		store.Close()
		log.Close()
		return nil, fmt.Errorf("initializing registry access: %w", err)
	}

	portalResolver := portal.NewResolver(store, registryLog)

	// Rate limiters nil means tier off
	var authLimiter, pullLimiter, anonPullLimiter *admin.Limiter
	if cfg.RateLimit.AuthFailureLimit > 0 && cfg.RateLimit.AuthFailureWindow > 0 {
		authLimiter = admin.NewLimiter(cfg.RateLimit.AuthFailureLimit, time.Duration(cfg.RateLimit.AuthFailureWindow)*time.Second)
	}
	if cfg.RateLimit.PullPerMinute > 0 {
		pullLimiter = admin.NewLimiter(cfg.RateLimit.PullPerMinute, time.Minute)
	}
	if cfg.RateLimit.AnonPullPerMinute > 0 {
		anonPullLimiter = admin.NewLimiter(cfg.RateLimit.AnonPullPerMinute, time.Minute)
	}

	tokenHandler := auth.NewTokenHandler(tokenService, store, authManager, enforcer, portalResolver, authLimiter, registryLog)

	registryHandler := registry.PullRateLimit(registryApp, tokenService, pullLimiter, anonPullLimiter, registryLog)

	blobStore, err := artifacts.NewBlobStore(cfg.Artifacts.StoragePath)
	if err != nil {
		store.Close()
		log.Close()
		return nil, fmt.Errorf("initializing artifact storage: %w", err)
	}
	artifactManager := artifacts.NewManager(store, blobStore, cfg.Artifacts, log)
	artifactV1Facade := artifacts.NewV1API(store, artifactManager, authManager, enforcer, authLimiter, log)

	// Portal listeners serve the whole app on their own ports
	portalProxies := portal.NewManager(portalResolver, cfg.Server.Host, registryLog)
	portalProxies.SetTimeouts(portal.ServerTimeouts{
		ReadHeader: time.Duration(cfg.Server.ReadTimeout) * time.Second,
		Idle:       time.Duration(cfg.Server.IdleTimeout) * time.Second,
	})
	portalService := portal.NewService(store, enforcer, portalProxies, cfg, log)

	// TLS/ACME engine, acme alone pre-provisions certs while serving cleartext
	var certEngine *certs.Engine
	if cfg.TLS.Enabled || cfg.TLS.ACME.Enabled {
		certEngine, err = certs.NewEngine(cfg, store, log)
		if err != nil {
			store.Close()
			log.Close()
			return nil, fmt.Errorf("initializing tls engine: %w", err)
		}
		if cfg.TLS.Enabled {
			portalProxies.SetTLSConfig(certEngine.TLSConfig())
			log.Info("TLS enabled (acme=%v manual_cert=%v)", certEngine.ACMEEnabled(), certEngine.ManualCertLoaded())
		} else {
			log.Info("ACME pre-provisioning enabled, serving stays cleartext until tls.enabled is set")
		}
	}
	certService := certs.NewService(store, enforcer, certEngine, cfg, log)

	// Audit trail, nil recorder disables recording entirely
	var auditRecorder *audit.Recorder
	var auditService *audit.Service
	if cfg.Security.Audit.Enabled {
		auditRecorder = audit.NewRecorder(store, log)
		auditRecorder.ScheduleRetention(context.Background(), cfg.Security.Audit.RetentionDays)
		auditService = audit.NewService(store, log)
		log.Info("Audit trail enabled (retention %dd)", cfg.Security.Audit.RetentionDays)
	}

	oidcHandler := auth.NewOIDCHandler(authManager, store, &cfg.Auth.OIDC, portalResolver, log)

	gcCollector, err := admin.NewCollector(cfg.Registry.StoragePath, registryLog)
	if err != nil {
		store.Close()
		log.Close()
		return nil, fmt.Errorf("initializing garbage collector: %w", err)
	}
	if cfg.GC.Enabled && cfg.GC.IntervalHours > 0 {
		gcCollector.Schedule(context.Background(), time.Duration(cfg.GC.IntervalHours)*time.Hour, cfg.GC.RemoveUntagged)
		log.Info("Scheduled registry GC every %dh (remove_untagged=%v)", cfg.GC.IntervalHours, cfg.GC.RemoveUntagged)
	}

	staleAge := time.Duration(cfg.Artifacts.StaleUploadCleanupHours) * time.Hour
	if removed, err := blobStore.CleanStaleUploads(staleAge); err != nil {
		log.Error("cleaning stale artifact uploads: %v", err)
	} else if removed > 0 {
		log.Info("Cleaned %d stale artifact upload sessions", removed)
	}

	artifactReaper := artifacts.NewReaper(artifactManager, store, staleAge, log)
	if cfg.Artifacts.Reaper.Enabled && cfg.Artifacts.Reaper.IntervalHours > 0 {
		artifactReaper.Schedule(context.Background(), time.Duration(cfg.Artifacts.Reaper.IntervalHours)*time.Hour)
		log.Info("Scheduled artifact reaper every %dh", cfg.Artifacts.Reaper.IntervalHours)
	}

	rpcServer := rpc.NewServer(rpc.ServerDeps{
		Store:             store,
		Config:            cfg,
		Log:               log,
		RegistryHandler:   registryHandler,
		RegistryAccess:    registryAccess,
		TokenHandler:      tokenHandler,
		AuthManager:       authManager,
		Enforcer:          enforcer,
		OIDCHandler:       oidcHandler,
		WebhookDispatcher: dispatcher,
		PortalResolver:    portalResolver,
		PortalService:     portalService,
		AuthLimiter:       authLimiter,
		ArtifactManager:   artifactManager,
		ArtifactV1Facade:  artifactV1Facade,
		GCCollector:       gcCollector,
		CertService:       certService,
		AuditRecorder:     auditRecorder,
		AuditService:      auditService,
	})

	// Portal listeners reuse the fully built app handler
	portalProxies.SetHandler(rpcServer.Handler())
	if err := portalProxies.Reconcile(context.Background()); err != nil {
		registryLog.Error("portal proxy startup: %v", err)
	}

	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%s", cfg.Server.Host, cfg.Server.Port),
		Handler:           rpcServer.Handler(),
		ReadHeaderTimeout: time.Duration(cfg.Server.ReadTimeout) * time.Second,
		IdleTimeout:       time.Duration(cfg.Server.IdleTimeout) * time.Second,
	}

	var challengeSrv *http.Server
	if certEngine != nil {
		if cfg.TLS.Enabled {
			srv.TLSConfig = certEngine.TLSConfig()
		}
		if cfg.TLS.ACME.Enabled && cfg.TLS.ACME.HTTPPort != "" {
			challengeSrv = &http.Server{
				Addr:              fmt.Sprintf("%s:%s", cfg.Server.Host, cfg.TLS.ACME.HTTPPort),
				Handler:           certEngine.HTTPChallengeHandler(),
				ReadHeaderTimeout: 10 * time.Second,
				IdleTimeout:       time.Duration(cfg.Server.IdleTimeout) * time.Second,
			}
		}
	}

	return &App{
		Config:          cfg,
		Log:             log,
		Store:           store,
		TokenService:    tokenService,
		AuthManager:     authManager,
		Enforcer:        enforcer,
		RegistryAccess:  registryAccess,
		PortalProxies:   portalProxies,
		CertEngine:      certEngine,
		Server:          srv,
		ChallengeServer: challengeSrv,
	}, nil
}

// Starts listening and blocks until a SIGINT/SIGTERM is received then shuts down
func (a *App) Start() error {
	go func() {
		if a.Server.TLSConfig != nil {
			a.Log.Info("Starting Distroface with TLS on %s", a.Server.Addr)
			if err := a.Server.ListenAndServeTLS("", ""); err != nil && err != http.ErrServerClosed {
				a.Log.Fatal("Failed to start server: %v", err)
			}
			return
		}
		a.Log.Info("Starting Distroface on %s", a.Server.Addr)
		if err := a.Server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.Log.Fatal("Failed to start server: %v", err)
		}
	}()

	if a.ChallengeServer != nil {
		go func() {
			a.Log.Info("ACME challenge listener on %s", a.ChallengeServer.Addr)
			if err := a.ChallengeServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
				a.Log.Error("ACME challenge listener stopped: %v", err)
			}
		}()
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	a.Log.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if a.PortalProxies != nil {
		a.PortalProxies.Close()
	}
	if a.ChallengeServer != nil {
		_ = a.ChallengeServer.Shutdown(ctx)
	}
	if err := a.Server.Shutdown(ctx); err != nil {
		a.Log.Error("Server forced to shutdown: %v", err)
	}

	a.Log.Info("Server stopped")
	return nil
}

// Close releases all held resources
func (a *App) Close() {
	if a.PortalProxies != nil {
		a.PortalProxies.Close()
	}
	if a.Store != nil {
		a.Store.Close()
	}
	if a.Log != nil {
		a.Log.Close()
	}
}
