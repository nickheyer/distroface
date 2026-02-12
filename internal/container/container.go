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
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/config"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/registry"
	"github.com/nickheyer/distroface/internal/rpc"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/sirupsen/logrus"
)

// App holds all initialized dependencies and the HTTP server.
type App struct {
	Config         *config.Config
	Log            *logger.Logger
	Store          *storage.Store
	TokenService   *auth.TokenService
	RegistryAccess *registry.RegistryAccess
	Server         *http.Server
}

// New builds the entire application: config, logger, store, token service,
// registry handler, RPC server, and HTTP server. Returns a ready-to-start App.
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

	registry.RegisterListenerMiddleware(store, registryLog)

	registryCfg := registry.BuildConfig(cfg.Registry.StoragePath, tokenService.CertPath(), cfg.Server.Host, cfg.Server.Port)
	registryApp := handlers.NewApp(context.Background(), registryCfg)
	registryLog.Info("Distribution v3 initialized")

	registryAccess, err := registry.NewRegistryAccess(cfg.Registry.StoragePath)
	if err != nil {
		store.Close()
		log.Close()
		return nil, fmt.Errorf("initializing registry access: %w", err)
	}

	tokenHandler := auth.NewTokenHandler(tokenService, store, registryLog)

	rpcServer := rpc.NewServer(rpc.ServerDeps{
		Store:           store,
		Config:          cfg,
		Log:             log,
		RegistryHandler: registryApp,
		RegistryAccess:  registryAccess,
		TokenHandler:    tokenHandler,
	})

	srv := &http.Server{
		Addr:        fmt.Sprintf("%s:%s", cfg.Server.Host, cfg.Server.Port),
		Handler:     rpcServer.Handler(),
		ReadTimeout: time.Duration(cfg.Server.ReadTimeout) * time.Second,
		IdleTimeout: time.Duration(cfg.Server.IdleTimeout) * time.Second,
	}

	return &App{
		Config:         cfg,
		Log:            log,
		Store:          store,
		TokenService:   tokenService,
		RegistryAccess: registryAccess,
		Server:         srv,
	}, nil
}

// Start begins listening and blocks until a SIGINT/SIGTERM is received,
// then gracefully shuts down the server.
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

	if err := a.Server.Shutdown(ctx); err != nil {
		a.Log.Error("Server forced to shutdown: %v", err)
	}

	a.Log.Info("Server stopped")
	return nil
}

// Close releases all held resources
func (a *App) Close() {
	if a.Store != nil {
		a.Store.Close()
	}
	if a.Log != nil {
		a.Log.Close()
	}
}
