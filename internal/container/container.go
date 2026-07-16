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
	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/bootstrap"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/gc"
	"github.com/nickheyer/distroface/internal/ratelimit"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/registry"
	"github.com/nickheyer/distroface/internal/rpc"
	"github.com/nickheyer/distroface/internal/webhook"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/sirupsen/logrus"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

// App holds all initialized dependencies and the HTTP server.
type App struct {
	Config         *config.Config
	Log            *logger.Logger
	Store          *storage.Store
	TokenService   *auth.TokenService
	AuthManager    *auth.Manager
	Enforcer       *rbac.Enforcer
	RegistryAccess *registry.RegistryAccess
	PortalProxies  *registry.PortalProxyManager
	Server         *http.Server
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

	store, err := storage.NewSQLiteStore(cfg.Database.Path, storage.DBConfig{
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

	if err := bootstrap.Run(context.Background(), cfg.Bootstrap, store, authManager, log); err != nil {
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

	portalResolver := registry.NewPortalResolver(store, registryLog)

	// Rate limiters nil means tier off
	var authLimiter, pullLimiter, anonPullLimiter *ratelimit.Limiter
	if cfg.RateLimit.AuthFailureLimit > 0 && cfg.RateLimit.AuthFailureWindow > 0 {
		authLimiter = ratelimit.New(cfg.RateLimit.AuthFailureLimit, time.Duration(cfg.RateLimit.AuthFailureWindow)*time.Second)
	}
	if cfg.RateLimit.PullPerMinute > 0 {
		pullLimiter = ratelimit.New(cfg.RateLimit.PullPerMinute, time.Minute)
	}
	if cfg.RateLimit.AnonPullPerMinute > 0 {
		anonPullLimiter = ratelimit.New(cfg.RateLimit.AnonPullPerMinute, time.Minute)
	}

	tokenHandler := auth.NewTokenHandler(tokenService, store, authManager, enforcer, portalResolver, authLimiter, registryLog)

	registryHandler := registry.PullRateLimit(portalResolver.Middleware(registryApp), tokenService, pullLimiter, anonPullLimiter, registryLog)

	blobStore, err := artifacts.NewBlobStore(cfg.Artifacts.StoragePath)
	if err != nil {
		store.Close()
		log.Close()
		return nil, fmt.Errorf("initializing artifact storage: %w", err)
	}
	artifactManager := artifacts.NewManager(store, blobStore, cfg.Artifacts, log)
	artifactV1Facade := artifacts.NewV1API(store, artifactManager, authManager, enforcer, authLimiter, log)
	artifactPortalHandler := portalResolver.ArtifactMiddleware(artifactV1Facade)

	// Portal proxies serve the docker and artifact surfaces on their own ports
	portalMux := http.NewServeMux()
	portalMux.Handle("/v2/", registryHandler)
	portalMux.Handle("GET /auth/token", tokenHandler)
	portalMux.Handle("POST /auth/token", tokenHandler)
	artifactV1Facade.RegisterAuth(portalMux)
	artifactV1Facade.RegisterArtifacts(portalMux, artifactPortalHandler)
	portalProxies := registry.NewPortalProxyManager(portalResolver, h2c.NewHandler(portalMux, &http2.Server{}), cfg.Server.Host, registryLog)
	if err := portalProxies.Reconcile(context.Background()); err != nil {
		registryLog.Error("portal proxy startup: %v", err)
	}

	oidcHandler := auth.NewOIDCHandler(authManager, store, &cfg.Auth.OIDC, log)

	gcCollector, err := gc.New(cfg.Registry.StoragePath, registryLog)
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
		Store:                 store,
		Config:                cfg,
		Log:                   log,
		RegistryHandler:       registryHandler,
		RegistryAccess:        registryAccess,
		TokenHandler:          tokenHandler,
		AuthManager:           authManager,
		Enforcer:              enforcer,
		OIDCHandler:           oidcHandler,
		WebhookDispatcher:     dispatcher,
		PortalProxies:         portalProxies,
		AuthLimiter:           authLimiter,
		ArtifactManager:       artifactManager,
		ArtifactV1Facade:      artifactV1Facade,
		ArtifactPortalHandler: artifactPortalHandler,
		GCCollector:           gcCollector,
	})

	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%s", cfg.Server.Host, cfg.Server.Port),
		Handler:           rpcServer.Handler(),
		ReadHeaderTimeout: time.Duration(cfg.Server.ReadTimeout) * time.Second,
		IdleTimeout:       time.Duration(cfg.Server.IdleTimeout) * time.Second,
	}

	return &App{
		Config:         cfg,
		Log:            log,
		Store:          store,
		TokenService:   tokenService,
		AuthManager:    authManager,
		Enforcer:       enforcer,
		RegistryAccess: registryAccess,
		PortalProxies:  portalProxies,
		Server:         srv,
	}, nil
}

// Starts listening and blocks until a SIGINT/SIGTERM is received then shuts down
func (a *App) Start() error {
	go func() {
		a.Log.Info("Starting Distroface on %s", a.Server.Addr)
		if err := a.Server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			a.Log.Fatal("Failed to start server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	a.Log.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if a.PortalProxies != nil {
		a.PortalProxies.Close()
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
