package container

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/distribution/distribution/v3/registry/handlers"
	"github.com/nickheyer/distroface/internal/admin"
	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/certs"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/registry"
	"github.com/nickheyer/distroface/internal/rpc"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/internal/webhook"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/sirupsen/logrus"
)

// App holds all initialized dependencies and the HTTP server.
type App struct {
	Config         *config.Config
	Log            *logger.Logger
	Store          *stores.Store
	Resolver       *settings.Resolver
	TokenService   *auth.TokenService
	AuthManager    *auth.Manager
	Enforcer       *rbac.Enforcer
	RegistryAccess *registry.RegistryAccess
	PortalProxies  *portal.Manager
	CertEngine     *certs.Engine
	Server         *http.Server
}

// New builds the entire application: config, logger, store, settings
// resolver, RBAC enforcer, auth manager, registry handler, and HTTP server.
func New() (*App, error) {
	ctx := context.Background()

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

	fail := func(step string, err error) (*App, error) {
		store.Close()
		log.Close()
		return nil, fmt.Errorf("%s: %w", step, err)
	}

	// Typed runtime settings, file seeds once and pins forever
	resolver := settings.NewResolver(store, cfg.Overrides)
	if err := resolver.SeedSystem(ctx, cfg.Settings); err != nil {
		return fail("seeding settings", err)
	}
	if locked := resolver.LockedPaths(); len(locked) > 0 {
		log.Info("Settings pinned by config file: %v", locked)
	}

	enforcer, err := rbac.NewEnforcer(store.DB())
	if err != nil {
		return fail("initializing RBAC enforcer", err)
	}
	anonymous := resolver.System(ctx).GetAuth().GetAnonymousAccess()
	if err := enforcer.SeedDefaultPolicies(anonymous); err != nil {
		return fail("seeding RBAC policies", err)
	}
	subscribeAnonymousReseed(resolver, enforcer, anonymous, log)
	log.Info("RBAC enforcer initialized")

	authManager, err := auth.NewManager(store, enforcer, cfg.Auth.JWTSecret, resolver)
	if err != nil {
		return fail("initializing auth manager", err)
	}
	if !authManager.IsAnyAuthEnabled() {
		log.Warn("SECURITY: no auth provider is enabled. Every request runs as admin, do not expose this instance")
	}

	if err := admin.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		return fail("configuring trusted proxies", err)
	}

	if err := Run(ctx, cfg.Bootstrap, store, authManager, log); err != nil {
		return fail("bootstrap seeding", err)
	}

	// ECDSA keys for registry JWTs, separate from HS256 sessions
	tokenService, err := auth.NewTokenService(cfg.Storage.DataDir, "distroface", "distroface-registry", resolver)
	if err != nil {
		return fail("initializing token service", err)
	}
	log.Info("Token service initialized (cert: %s)", tokenService.CertPath())

	registryLog := log.Module("distroface-registry")
	logrus.SetOutput(registryLog)
	logrus.SetLevel(logrus.InfoLevel)
	logrus.SetFormatter(&logrus.TextFormatter{
		DisableColors:    true,
		DisableTimestamp: true,
	})

	if err := os.MkdirAll(cfg.Registry.StoragePath, 0755); err != nil {
		return fail("creating registry storage directory", err)
	}

	dispatcher := webhook.NewDispatcher(store, registryLog, resolver)

	// Recorder self gates on the live audit setting
	auditRecorder := audit.NewRecorder(store, resolver, log)
	auditRecorder.ScheduleRetention(ctx)
	auditService := audit.NewService(store, log)

	registry.RegisterListenerMiddleware(store, registryLog, dispatcher, auditRecorder)

	registryCfg := registry.BuildConfig(cfg.Registry.StoragePath, tokenService.CertPath(), cfg.Server.Host, cfg.Server.Port)
	registryApp := handlers.NewApp(ctx, registryCfg)
	registryLog.Info("Distribution v3 initialized")

	registryAccess, err := registry.NewRegistryAccess(cfg.Registry.StoragePath)
	if err != nil {
		return fail("initializing registry access", err)
	}

	portalResolver := portal.NewResolver(store, registryLog)

	// Limits read live, zero disables at call time
	rateLimits := func() *v1.RateLimitSettings {
		return resolver.System(context.Background()).GetRateLimit()
	}
	authLimiter := admin.NewDynamicLimiter(func() (int, time.Duration) {
		rl := rateLimits()
		return int(rl.GetAuthFailureLimit()), time.Duration(rl.GetAuthFailureWindowSeconds()) * time.Second
	})
	pullLimiter := admin.NewDynamicLimiter(func() (int, time.Duration) {
		return int(rateLimits().GetPullPerMinute()), time.Minute
	})
	anonPullLimiter := admin.NewDynamicLimiter(func() (int, time.Duration) {
		return int(rateLimits().GetAnonPullPerMinute()), time.Minute
	})

	tokenHandler := auth.NewTokenHandler(tokenService, store, authManager, enforcer, portalResolver, authLimiter, auditRecorder, registryLog)
	registryHandler := registry.PullRateLimit(registryApp, tokenService, pullLimiter, anonPullLimiter, registryLog)

	blobStore, err := artifacts.NewBlobStore(cfg.Artifacts.StoragePath)
	if err != nil {
		return fail("initializing artifact storage", err)
	}
	artifactManager := artifacts.NewManager(store, blobStore, resolver, log)
	artifactV1Facade := artifacts.NewV1API(store, artifactManager, authManager, enforcer, authLimiter, auditRecorder, log)

	// Portal listeners serve the whole app on their own ports
	portalProxies := portal.NewManager(portalResolver, cfg.Server.Host, registryLog)
	portalProxies.SetTimeouts(portal.ServerTimeouts{
		ReadHeader: time.Duration(cfg.Server.ReadTimeout) * time.Second,
		Idle:       time.Duration(cfg.Server.IdleTimeout) * time.Second,
	})

	certEngine, err := certs.NewEngine(store, resolver, portalResolver, cfg.TLS.CertFile, cfg.TLS.KeyFile, cfg.Server.Host, log)
	if err != nil {
		return fail("initializing tls engine", err)
	}
	resolver.Subscribe(func() { certEngine.Invalidate(context.Background()) })
	portalResolver.SetCertReady(func(ctx context.Context, p *portal.Portal, host string) bool {
		return certEngine.PortalCertReady(ctx, p.CertSource, p.OrgID, p.ID, host)
	})
	certEngine.ScheduleRenewal(ctx)

	mainPort, err := strconv.Atoi(cfg.Server.Port)
	if err != nil {
		return fail("parsing server port", err)
	}
	portalService := portal.NewService(store, enforcer, portalProxies, certEngine, resolver, mainPort, log)

	// Portals decide https themselves, independent of the primary's mode
	portalProxies.SetTLSConfig(certEngine.TLSConfig())
	log.Info("TLS engine ready (mode=%v acme=%v)",
		resolver.System(ctx).GetTls().GetMode(), certEngine.ACMEEnabled(ctx))
	certService := certs.NewService(store, enforcer, certEngine, resolver, log)
	acmeServer := certs.NewACMEServer(certEngine)

	oidcHandler := auth.NewOIDCHandler(authManager, store, resolver, portalResolver, log)

	gcCollector, err := admin.NewCollector(cfg.Registry.StoragePath, registryLog)
	if err != nil {
		return fail("initializing garbage collector", err)
	}
	gcCollector.Schedule(ctx, resolver)

	if removed, err := blobStore.CleanStaleUploads(artifactManager.StaleUploadAge(ctx)); err != nil {
		log.Error("cleaning stale artifact uploads: %v", err)
	} else if removed > 0 {
		log.Info("Cleaned %d stale artifact upload sessions", removed)
	}

	artifactReaper := artifacts.NewReaper(artifactManager, store, log)
	artifactReaper.Schedule(ctx)

	if err := seedLegacyACMEDomains(ctx, cfg.LegacyACMEDomains, store, log); err != nil {
		return fail("seeding legacy acme domains", err)
	}

	rpcServer := rpc.NewServer(rpc.ServerDeps{
		Store:               store,
		Resolver:            resolver,
		Log:                 log,
		RegistryHandler:     registryHandler,
		RegistryAccess:      registryAccess,
		RegistryStoragePath: cfg.Registry.StoragePath,
		TokenHandler:        tokenHandler,
		AuthManager:         authManager,
		Enforcer:            enforcer,
		OIDCHandler:         oidcHandler,
		WebhookDispatcher:   dispatcher,
		PortalResolver:      portalResolver,
		PortalService:       portalService,
		CertEngine:          certEngine,
		ACMEServer:          acmeServer,
		AuthLimiter:         authLimiter,
		ArtifactManager:     artifactManager,
		ArtifactV1Facade:    artifactV1Facade,
		GCCollector:         gcCollector,
		CertService:         certService,
		AuditRecorder:       auditRecorder,
		AuditService:        auditService,
	})

	// Portal listeners reuse the fully built app handler
	portalProxies.SetHandler(rpcServer.Handler())
	if err := portalProxies.Reconcile(ctx); err != nil {
		registryLog.Error("portal proxy startup: %v", err)
	}

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
		Resolver:       resolver,
		TokenService:   tokenService,
		AuthManager:    authManager,
		Enforcer:       enforcer,
		RegistryAccess: registryAccess,
		PortalProxies:  portalProxies,
		CertEngine:     certEngine,
		Server:         srv,
	}, nil
}

// Reseeds the anonymous policy tier when the toggle flips
func subscribeAnonymousReseed(resolver *settings.Resolver, enforcer *rbac.Enforcer, initial bool, log *logger.Logger) {
	var mu sync.Mutex
	last := initial
	resolver.Subscribe(func() {
		current := resolver.System(context.Background()).GetAuth().GetAnonymousAccess()
		mu.Lock()
		defer mu.Unlock()
		if current == last {
			return
		}
		last = current
		if err := enforcer.SeedDefaultPolicies(current); err != nil {
			log.Error("reseeding anonymous policies: %v", err)
		}
	})
}

// Seeds retired static acme domains as approved system rows
func seedLegacyACMEDomains(ctx context.Context, domains []string, store *stores.Store, log *logger.Logger) error {
	for _, domain := range domains {
		existing, err := store.GetCertificateDomainByName(ctx, domain)
		if err != nil {
			return err
		}
		if existing != nil {
			continue
		}
		row := &db.CertificateDomain{
			Domain:    domain,
			Scope:     v1.CertificateDomainScope_CERTIFICATE_DOMAIN_SCOPE_SYSTEM,
			Approved:  true,
			CreatedBy: "config",
		}
		if err := store.CreateCertificateDomain(ctx, row); err != nil {
			return err
		}
		log.Info("Seeded legacy acme domain %s as system scope", domain)
	}
	return nil
}

// Starts listening and blocks until a SIGINT/SIGTERM is received then shuts down
func (a *App) Start() error {
	go func() {
		ln, err := net.Listen("tcp", a.Server.Addr)
		if err != nil {
			a.Log.Fatal("Failed to start server: %v", err)
			return
		}
		a.Log.Info("Starting Distroface on %s (tls+cleartext)", a.Server.Addr)
		ln = certs.DualSchemeListener(ln, a.CertEngine.TLSConfig(), a.Server.ReadHeaderTimeout)
		if err := a.Server.Serve(ln); err != nil && err != http.ErrServerClosed {
			a.Log.Fatal("Failed to start server: %v", err)
		}
	}()

	a.CertEngine.ReconcileChallengeServer()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	a.Log.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if a.PortalProxies != nil {
		a.PortalProxies.Close()
	}
	if a.CertEngine != nil {
		a.CertEngine.Close()
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
