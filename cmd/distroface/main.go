package main

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
)

func main() {
	cfg, err := config.Load(".")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load configuration: %v\n", err)
		os.Exit(1)
	}

	logConfig := &logger.Config{
		Enabled:    cfg.Logging.Enabled,
		FilePath:   cfg.Logging.FilePath,
		MaxSize:    cfg.Logging.MaxSize,
		MaxBackups: cfg.Logging.MaxBackups,
		MaxAge:     cfg.Logging.MaxAge,
		Compress:   cfg.Logging.Compress,
	}
	log := logger.NewWithConfig(logConfig)
	defer log.Close()

	if err := os.MkdirAll(cfg.Storage.DataDir, 0755); err != nil {
		log.Fatal("Failed to create data directory: %v", err)
	}

	store, err := storage.NewSQLiteStore(cfg.Database.Path, storage.DBConfig{
		MaxOpenConns:    cfg.Database.MaxConnections,
		MaxIdleConns:    cfg.Database.MaxIdleConns,
		ConnMaxLifetime: time.Duration(cfg.Database.ConnMaxLifetime) * time.Second,
	})
	if err != nil {
		log.Fatal("Failed to initialize storage: %v", err)
	}
	defer store.Close()

	// Initialize token service (ECDSA key pair for Docker token auth)
	tokenExpiry := time.Duration(cfg.Auth.TokenExpiry) * time.Second
	tokenService, err := auth.NewTokenService(cfg.Storage.DataDir, "distroface", "distroface-registry", tokenExpiry)
	if err != nil {
		log.Fatal("Failed to initialize token service: %v", err)
	}
	log.Info("Token service initialized (cert: %s)", tokenService.CertPath())

	// Ensure registry storage directory exists
	if err := os.MkdirAll(cfg.Registry.StoragePath, 0755); err != nil {
		log.Fatal("Failed to create registry storage directory: %v", err)
	}

	// Build Distribution v3 registry handler
	registryCfg := registry.BuildConfig(cfg.Registry.StoragePath, tokenService.CertPath(), cfg.Server.Host, cfg.Server.Port)
	registryApp := handlers.NewApp(context.Background(), registryCfg)
	log.Info("Distribution v3 registry initialized")

	// Create auth and event handlers
	tokenHandler := auth.NewTokenHandler(tokenService, store, log)
	eventHandler := registry.NewEventHandler(store, log)

	rpcServer := rpc.NewServer(rpc.ServerDeps{
		Store:           store,
		Config:          cfg,
		Log:             log,
		RegistryHandler: registryApp,
		TokenHandler:    tokenHandler,
		EventHandler:    eventHandler,
	})

	srv := &http.Server{
		Addr:        fmt.Sprintf("%s:%s", cfg.Server.Host, cfg.Server.Port),
		Handler:     rpcServer.Handler(),
		ReadTimeout: time.Duration(cfg.Server.ReadTimeout) * time.Second,
		IdleTimeout: time.Duration(cfg.Server.IdleTimeout) * time.Second,
		// WriteTimeout set to 0 for large layer uploads via the registry
	}

	go func() {
		log.Info("Starting Distroface on %s:%s", cfg.Server.Host, cfg.Server.Port)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatal("Failed to start server: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, os.Interrupt, syscall.SIGTERM)
	<-quit

	log.Info("Shutting down server...")

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := srv.Shutdown(ctx); err != nil {
		log.Error("Server forced to shutdown: %v", err)
	}

	log.Info("Server stopped")
}
