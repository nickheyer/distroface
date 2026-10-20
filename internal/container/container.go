package container

import (
	"context"
	"fmt"
	"github.com/nickheyer/distroface/pkg/utils"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/distribution/distribution/v3/registry/handlers"
	"github.com/nickheyer/distroface/internal/actions"
	"github.com/nickheyer/distroface/internal/admin"
	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/certs"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/mailer"
	"github.com/nickheyer/distroface/internal/mirror"
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
	GitSSH         *gitrepo.SSHManager
	Forge          *forge.Manager
	Server         *http.Server
	cancel         context.CancelFunc
}

// New builds the entire application: config, logger, store, settings
// resolver, RBAC enforcer, auth manager, registry handler, and HTTP server.
func New() (*App, error) {
	ctx, cancel := context.WithCancel(context.Background())

	cfg, err := config.Load(".")
	if err != nil {
		cancel()
		return nil, fmt.Errorf("loading configuration: %w", err)
	}

	if err := os.MkdirAll(cfg.Logging.Dir, 0755); err != nil {
		cancel()
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
		cancel()
		log.Close()
		return nil, fmt.Errorf("creating data directory: %w", err)
	}

	store, err := stores.NewSQLiteStore(cfg.Database.Path, stores.DBConfig{
		MaxOpenConns:    cfg.Database.MaxConnections,
		MaxIdleConns:    cfg.Database.MaxIdleConns,
		ConnMaxLifetime: time.Duration(cfg.Database.ConnMaxLifetime) * time.Second,
	})
	if err != nil {
		cancel()
		log.Close()
		return nil, fmt.Errorf("initializing storage: %w", err)
	}

	fail := func(step string, err error) (*App, error) {
		cancel()
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
	if err := seedPolicies(ctx, store, enforcer, log); err != nil {
		return fail("seeding RBAC policies", err)
	}
	log.Info("RBAC enforcer initialized")

	authManager, err := auth.NewManager(store, enforcer, cfg.Auth.JWTSecret, resolver)
	if err == nil {
		err = authManager.LoadSealingKey(cfg.Auth.SealingKey, cfg.Storage.DataDir)
	}
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

	portalResolver := portal.NewResolver(store, resolver, registryLog)

	// Org isolation toggles must reach already cached portals
	resolver.Subscribe(portalResolver.Invalidate)

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
	resolver.Subscribe(certEngine.Invalidate)
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

	// Pushes go straight into the embedded registry handler
	ociSyncer := mirror.NewOCISyncer(registryApp, tokenService)
	mirrorMonitor := mirror.NewMonitor(store, resolver, artifactManager, ociSyncer, log)
	mirrorMonitor.SetSealer(authManager.Sealer("mirror"), authManager.LegacySealer("mirror"))

	// Git hosting, the forge, mail, and actions share one manager chain
	gitEngine, err := gitrepo.NewEngine(cfg.Git.StoragePath, cfg.Storage.DataDir)
	if err != nil {
		return fail("initializing git storage", err)
	}
	gitAccess := gitrepo.NewAccess(store, enforcer)
	gitManager := gitrepo.NewManager(store, gitEngine, blobStore, resolver, gitAccess, dispatcher, auditRecorder, log)
	gitManager.SetPackageManager(&packageManager{
		store: store, registry: registryAccess, artifacts: artifactManager,
		images: auth.NewRepoAccess(store, enforcer), artAccess: artifacts.NewAccess(store, enforcer),
	})
	registry.SetSourceLinker(gitManager)
	tokenHandler.SetLinkedAccess(gitAccess)
	artifactV1Facade.SetLinkedAccess(gitAccess)
	mailService := mailer.NewService(store, resolver, log)
	mailService.SetAuditRecorder(auditRecorder)
	forgeManager := forge.NewManager(store, gitManager, mailService, resolver, log)
	forgeManager.SetTokenKey(authManager.DerivedKey("notifications"))
	actionsManager := actions.NewManager(store, gitManager, forgeManager, artifactManager, authManager, resolver, log)
	forgeManager.SetTrigger(actionsManager.Trigger)
	forgeManager.SetReleaseCreated(actionsManager.PinReleaseImages)
	registry.SetImageRecorder(actionsManager)
	mailService.SetInboundHandler(forgeManager.HandleInboundMail)
	gitManager.Subscribe(forgeManager.OnRefUpdate, actionsManager.OnRefUpdate, mirrorMonitor.OnRefUpdate)
	if err := actionsManager.ResealLegacy(ctx); err != nil {
		log.Error("resealing action secrets: %v", err)
	}
	if err := mirrorMonitor.ResealLegacy(ctx); err != nil {
		log.Error("resealing mirror credentials: %v", err)
	}
	gitHTTP := gitrepo.NewHTTP(gitManager, authManager, tokenService, authLimiter, pullLimiter, anonPullLimiter, log)
	gitSSH, err := gitrepo.NewSSHManager(gitManager, tokenService, portalResolver, cfg.Server.Host, log)
	if err != nil {
		return fail("initializing git ssh", err)
	}
	resolver.Subscribe(gitSSH.Reconcile)
	portalService.OnChange(gitSSH.Reconcile)
	portalService.SetSSHProbe(gitSSH.Probe)
	mirrorMonitor.SetGit(gitManager)
	mirrorMonitor.Schedule(ctx)
	gitManager.ScheduleMaintenance(ctx)
	mailService.Start(ctx)
	actionsManager.Start(ctx)
	log.Info("Git engine ready (repos=%s)", gitEngine.ReposDir())

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
		MirrorMonitor:       mirrorMonitor,
		GCCollector:         gcCollector,
		CertService:         certService,
		AuditRecorder:       auditRecorder,
		AuditService:        auditService,
		GitHTTP:             gitHTTP,
		GitManager:          gitManager,
		GitSSH:              gitSSH,
		ForgeManager:        forgeManager,
		ActionsManager:      actionsManager,
		MailService:         mailService,
	})

	// Portal listeners reuse the fully built app handler
	portalProxies.SetHandler(rpcServer.Handler())
	if err := portalProxies.Reconcile(ctx); err != nil {
		registryLog.Error("portal proxy startup: %v", err)
	}

	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%s", cfg.Server.Host, cfg.Server.Port),
		Handler:           rpcServer.Handler(),
		Protocols:         utils.CleartextProtocols(),
		ReadHeaderTimeout: time.Duration(cfg.Server.ReadTimeout) * time.Second,
		IdleTimeout:       time.Duration(cfg.Server.IdleTimeout) * time.Second,
	}

	return &App{
		Config:         cfg,
		Forge:          forgeManager,
		Log:            log,
		Store:          store,
		Resolver:       resolver,
		TokenService:   tokenService,
		AuthManager:    authManager,
		Enforcer:       enforcer,
		RegistryAccess: registryAccess,
		PortalProxies:  portalProxies,
		CertEngine:     certEngine,
		GitSSH:         gitSSH,
		Server:         srv,
		cancel:         cancel,
	}, nil
}

const policySeedMarker = "rbac_policies_seeded"

// Default grants land once, later restarts never undo admin edits
func seedPolicies(ctx context.Context, store *stores.Store, enforcer *rbac.Enforcer, log *logger.Logger) error {
	if err := enforcer.EnsureAdminPolicy(); err != nil {
		return err
	}
	if seeded, _ := store.GetSystemSetting(ctx, policySeedMarker); seeded != "" {
		return nil
	}
	if err := enforcer.SeedDefaultPolicies(); err != nil {
		return err
	}
	log.Info("Seeded default role policies")
	return store.SetSystemSetting(ctx, policySeedMarker, "1")
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
	if a.GitSSH != nil {
		a.GitSSH.Reconcile()
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	a.Log.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if a.cancel != nil {
		a.cancel()
	}
	if a.GitSSH != nil {
		a.GitSSH.Close()
	}
	if a.PortalProxies != nil {
		a.PortalProxies.Close()
	}
	if a.CertEngine != nil {
		a.CertEngine.Close()
	}
	if err := a.Server.Shutdown(ctx); err != nil {
		a.Log.Error("Server forced to shutdown: %v", err)
	}
	if a.Forge != nil {
		a.Forge.Wait(ctx)
	}

	a.Log.Info("Server stopped")
	return nil
}

// Close releases all held resources
func (a *App) Close() {
	if a.cancel != nil {
		a.cancel()
	}
	if a.GitSSH != nil {
		a.GitSSH.Close()
	}
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
