package server

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	dconfig "github.com/distribution/distribution/v3/configuration"
	_ "github.com/distribution/distribution/v3/registry/auth/token"
	_ "github.com/distribution/distribution/v3/registry/storage/driver/filesystem"
	"github.com/golang-jwt/jwt/v4"
	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/auth/permissions"
	"github.com/nickheyer/distroface/internal/constants"
	"github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/handlers"
	"github.com/nickheyer/distroface/internal/logging"
	"github.com/nickheyer/distroface/internal/metrics"
	"github.com/nickheyer/distroface/internal/models"
	"github.com/nickheyer/distroface/internal/repository"
	"github.com/nickheyer/distroface/internal/server/middleware"
)

type Server struct {
	config         *models.Config
	distConfig     *dconfig.Configuration
	router         *mux.Router
	ctx            context.Context
	db             *sql.DB
	authService    auth.AuthService
	authMiddleware *auth.Middleware
	permManager    *permissions.PermissionManager
	log            *logging.LogService
	client         *http.Client
}

func NewServer(cfg *models.Config) (*Server, error) {
	ctx := context.Background()

	// INIT LOGGING
	log, err := logging.NewLogService()
	if err != nil {
		return nil, fmt.Errorf("LOGGING INIT FAILED: %v", err)
	}

	// INIT DB
	db, err := initDB(cfg)
	if err != nil {
		return nil, log.Errorf("DATABASE INIT FAILED", err)
	}
	repo := repository.NewSQLiteRepository(db)
	permManager := permissions.NewPermissionManager(repo, db)

	// READ RSA KEYS
	signKey, verifyKey, err := loadRSAKeys(cfg.Server.RSAKeyFile)
	if err != nil {
		return nil, log.Errorf("FAILED TO LOAD RSA KEYS", err)
	}

	// INIT AUTH SERVICE
	authService := auth.NewAuthService(
		repo,
		permManager,
		signKey,
		verifyKey,
		cfg,
	)

	// INIT AUTH MIDDLEWARE
	authMiddleware := auth.NewMiddleware(authService, cfg)

	// INIT DISTRIBUTION CONFIG
	distConfig := &dconfig.Configuration{
		Storage: dconfig.Storage{
			"filesystem": dconfig.Parameters{
				"rootdirectory": cfg.Storage.RootDirectory,
			},
			"delete": dconfig.Parameters{
				"enabled": true,
			},
			"cache": dconfig.Parameters{
				"blobdescriptor": "inmemory",
			},
		},
		Auth: dconfig.Auth{
			"token": dconfig.Parameters{
				"realm":          cfg.Auth.Realm,
				"service":        cfg.Auth.Service,
				"issuer":         cfg.Auth.Issuer,
				"rootcertbundle": cfg.Server.CertBundle,
			},
		},
		Middleware: map[string][]dconfig.Middleware{
			"registry":   {{Name: "auth"}},
			"repository": {{Name: "auth"}},
			"storage":    {{Name: "auth"}},
		},
	}

	// SET HTTP CONFIG SEPARATELY
	distConfig.HTTP.Addr = fmt.Sprintf(":%s", cfg.Server.Port)
	distConfig.HTTP.Host = cfg.Server.Domain
	distConfig.HTTP.TLS.Certificate = cfg.Server.TLSCertFile
	distConfig.HTTP.TLS.Key = cfg.Server.TLSKeyFile

	s := &Server{
		config:         cfg,
		distConfig:     distConfig,
		router:         mux.NewRouter(),
		ctx:            ctx,
		db:             db,
		authService:    authService,
		authMiddleware: authMiddleware,
		permManager:    permManager,
		log:            log,
	}

	if err := s.setupRoutes(); err != nil {
		db.Close()
		return nil, log.Errorf("ROUTE SETUP FAILED", err)
	}

	return s, nil
}

func initDB(cfg *models.Config) (*sql.DB, error) {
	// ENSURE DB PATH EXISTS
	dir := filepath.Dir(cfg.Database.Path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create DB directory: %w", err)
	}

	// CREATE ONE-TIME BACKUP IF DB EXISTS AND BACKUP DOESN'T
	dbPath := cfg.Database.Path
	backupPath := dbPath + ".backup"

	if _, err := os.Stat(dbPath); err == nil {
		if _, err := os.Stat(backupPath); err != nil {
			srcFile, err := os.Open(dbPath)
			if err != nil {
				return nil, fmt.Errorf("failed to open DB for backup: %w", err)
			}
			defer srcFile.Close()

			dstFile, err := os.Create(backupPath)
			if err != nil {
				return nil, fmt.Errorf("failed to create backup file: %w", err)
			}
			defer dstFile.Close()

			if _, err := io.Copy(dstFile, srcFile); err != nil {
				return nil, fmt.Errorf("failed to copy DB to backup: %w", err)
			}
		}
	}

	// OPEN DB
	database, err := sql.Open("sqlite3", cfg.Database.Path)
	if err != nil {
		return nil, fmt.Errorf("failed to open DB: %w", err)
	}

	// PING DB FOR HEALTHCHECK
	if err := database.Ping(); err != nil {
		database.Close()
		return nil, fmt.Errorf("DB PING FAILED: %v", err)
	}

	// WAL MODE FOR CONCURRENCY
	_, _ = database.Exec("PRAGMA journal_mode=WAL;")
	_, _ = database.Exec("PRAGMA busy_timeout=5000;")

	// RUN SCHEMA
	if err := db.RunSchema(database, cfg); err != nil {
		database.Close()
		return nil, fmt.Errorf("schema init error: %w", err)
	}

	// INSERT INITIAL VALUES VIA CONFIG
	if err := db.RunInit(database, cfg); err != nil {
		database.Close()
		return nil, fmt.Errorf("init data error: %w", err)
	}

	// RUN MIGRATIONS
	if err := db.RunMigrations(database, cfg); err != nil {
		database.Close()
		return nil, fmt.Errorf("migrations error: %w", err)
	}

	return database, nil
}

func (s *Server) notFoundHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.log.Errorf("404 NOT FOUND", fmt.Errorf("PATH: %s METHOD: %s", r.URL.Path, r.Method))
		http.NotFound(w, r)
	}
}

func (s *Server) setupRoutes() error {
	s.router.Use(middleware.LoggingMiddleware(s.log))
	s.router.Use(middleware.CORS)

	// 404
	s.router.NotFoundHandler = s.notFoundHandler()

	// INIT HANDLERS + MISC SERVICES
	metricsSrv := metrics.NewMetricsService(s.log, s.config.Storage.RootDirectory)
	repo := repository.NewSQLiteRepository(s.db)
	authHandler := handlers.NewAuthHandler(s.config, s.authService, s.log)
	userHandler := handlers.NewUserHandler(repo, s.permManager, s.log)
	repoHandler := handlers.NewRepositoryHandler(repo, s.config, s.log, metricsSrv)
	artifactHandler := handlers.NewArtifactHandler(repo, s.config, s.log, metricsSrv)
	groupHandler := handlers.NewGroupHandler(repo, s.log)
	roleHandler := handlers.NewRoleHandler(repo, s.permManager, s.log)
	settingsHandler := handlers.NewSettingsHandler(repo, s.config, s.log)
	metricsHandler := handlers.NewMetricsHandler(metricsSrv, s.log)

	// PUBLIC ROUTES
	s.router.HandleFunc("/v2/", authHandler.HandleV2Check)
	s.router.HandleFunc("/auth/token", authHandler.HandleRegistryAuth).Methods("GET", "POST")
	s.router.HandleFunc("/api/v1/auth/login", authHandler.HandleWebLogin).Methods("POST")
	s.router.HandleFunc("/api/v1/auth/refresh", authHandler.HandleTokenRefresh).Methods("POST")

	// API ROUTES
	api := s.router.PathPrefix("/api/v1").Subrouter()
	api.Use(s.authMiddleware.AuthMiddleware)

	// SETTINGS ROUTES
	api.Handle("/settings", requirePermission(s.authService, models.ActionAdmin, models.ResourceSystem, metricsHandler)(
		http.HandlerFunc(settingsHandler.GetSettings))).Methods("GET")
	api.Handle("/settings/metrics", requirePermission(s.authService, models.ActionAdmin, models.ResourceSystem, nil)(
		http.HandlerFunc(metricsHandler.GetMetrics))).Methods("GET")
	api.Handle("/settings/{section}", requirePermission(s.authService, models.ActionAdmin, models.ResourceSystem, metricsHandler)(
		http.HandlerFunc(settingsHandler.GetSettings))).Methods("GET")
	api.Handle("/settings/{section}", requirePermission(s.authService, models.ActionAdmin, models.ResourceSystem, metricsHandler)(
		http.HandlerFunc(settingsHandler.UpdateSettings))).Methods("PUT")
	api.Handle("/settings/{section}/reset", requirePermission(s.authService, models.ActionAdmin, models.ResourceSystem, metricsHandler)(
		http.HandlerFunc(settingsHandler.ResetSettings))).Methods("POST")

	// REPOSITORY ROUTES
	api.Handle("/repositories", requirePermission(s.authService, models.ActionView, models.ResourceWebUI, metricsHandler)(
		http.HandlerFunc(repoHandler.ListRepositories))).Methods("GET")
	api.Handle("/repositories/{name}/tags/{tag}", requirePermission(s.authService, models.ActionDelete, models.ResourceTag, metricsHandler)(
		http.HandlerFunc(repoHandler.DeleteTag))).Methods("DELETE")
	api.Handle("/repositories/public", requirePermission(s.authService, models.ActionView, models.ResourceWebUI, metricsHandler)(
		http.HandlerFunc(repoHandler.ListGlobalRepositories))).Methods("GET")
	api.Handle("/repositories/visibility", requirePermission(s.authService, models.ActionUpdate, models.ResourceWebUI, metricsHandler)(
		http.HandlerFunc(repoHandler.UpdateImageVisibility))).Methods("POST")

	// USER MANAGEMENT
	api.Handle("/users", requirePermission(s.authService, models.ActionCreate, models.ResourceUser, metricsHandler)(
		http.HandlerFunc(userHandler.CreateUser))).Methods("POST")
	api.Handle("/users/groups", requirePermission(s.authService, models.ActionUpdate, models.ResourceUser, metricsHandler)(
		http.HandlerFunc(userHandler.UpdateUserGroups))).Methods("PUT")
	api.Handle("/users", requirePermission(s.authService, models.ActionView, models.ResourceUser, metricsHandler)(
		http.HandlerFunc(userHandler.ListUsers))).Methods("GET")
	api.HandleFunc("/users/me", userHandler.GetUser).Methods("GET")
	api.Handle("/users/{username}", requirePermission(s.authService, models.ActionView, models.ResourceUser, metricsHandler)(
		http.HandlerFunc(userHandler.GetUser))).Methods("GET")
	api.Handle("/users/{username}", requirePermission(s.authService, models.ActionDelete, models.ResourceUser, metricsHandler)(
		http.HandlerFunc(userHandler.DeleteUser))).Methods("DELETE")

	// ROLE MANAGEMENT
	api.Handle("/roles", requirePermission(s.authService, models.ActionView, models.ResourceSystem, metricsHandler)(
		http.HandlerFunc(roleHandler.ListRoles))).Methods("GET")
	api.Handle("/roles", requirePermission(s.authService, models.ActionAdmin, models.ResourceSystem, metricsHandler)(
		http.HandlerFunc(roleHandler.CreateRole))).Methods("POST")
	api.Handle("/roles/{name}", requirePermission(s.authService, models.ActionAdmin, models.ResourceSystem, metricsHandler)(
		http.HandlerFunc(roleHandler.UpdateRole))).Methods("PUT")
	api.Handle("/roles/{name}", requirePermission(s.authService, models.ActionAdmin, models.ResourceSystem, metricsHandler)(
		http.HandlerFunc(roleHandler.DeleteRole))).Methods("DELETE")

	// GROUP MANAGEMENT
	api.Handle("/groups", requirePermission(s.authService, models.ActionView, models.ResourceGroup, metricsHandler)(
		http.HandlerFunc(groupHandler.ListGroups))).Methods("GET")
	api.Handle("/groups/{name}", requirePermission(s.authService, models.ActionUpdate, models.ResourceGroup, metricsHandler)(
		http.HandlerFunc(groupHandler.UpdateGroup))).Methods("PUT")
	api.Handle("/groups/{name}", requirePermission(s.authService, models.ActionDelete, models.ResourceGroup, metricsHandler)(
		http.HandlerFunc(groupHandler.DeleteGroup))).Methods("DELETE")

	// MIGRATION ROUTES
	api.Handle("/registry/migrate", requirePermission(s.authService, models.ActionMigrate, models.ResourceTask, metricsHandler)(
		http.HandlerFunc(repoHandler.MigrateImages))).Methods("POST")
	api.Handle("/registry/migrate/status", requirePermission(s.authService, models.ActionMigrate, models.ResourceTask, metricsHandler)(
		http.HandlerFunc(repoHandler.GetMigrationStatus))).Methods("GET")
	api.Handle("/registry/proxy/catalog", requirePermission(s.authService, models.ActionMigrate, models.ResourceTask, metricsHandler)(
		http.HandlerFunc(repoHandler.ProxyCatalog))).Methods("GET")
	api.Handle("/registry/proxy/tags", requirePermission(s.authService, models.ActionMigrate, models.ResourceTask, metricsHandler)(
		http.HandlerFunc(repoHandler.ProxyTags))).Methods("GET")

	// ARTIFACT ROUTES
	api.Handle("/artifacts/repos", requirePermission(s.authService, models.ActionCreate, models.ResourceRepo, metricsHandler)(
		http.HandlerFunc(artifactHandler.CreateRepository))).Methods("POST")
	api.Handle("/artifacts/repos", requirePermission(s.authService, models.ActionView, models.ResourceRepo, metricsHandler)(
		http.HandlerFunc(artifactHandler.ListRepositories))).Methods("GET")
	api.Handle("/artifacts/repos/{repo}", requirePermission(s.authService, models.ActionDelete, models.ResourceRepo, metricsHandler)(
		http.HandlerFunc(artifactHandler.DeleteRepository))).Methods("DELETE")
	api.Handle("/artifacts/{repo}/upload", requirePermission(s.authService, models.ActionUpload, models.ResourceArtifact, metricsHandler)(
		http.HandlerFunc(artifactHandler.InitiateUpload))).Methods("POST")
	api.Handle("/artifacts/{repo}/upload/{uuid}", // NO CHECKS PER CHUNK
		http.HandlerFunc(artifactHandler.HandleUpload)).Methods("PATCH")
	api.Handle("/artifacts/{repo}/upload/{uuid}", requirePermission(s.authService, models.ActionUpload, models.ResourceArtifact, metricsHandler)(
		http.HandlerFunc(artifactHandler.CompleteUpload))).Methods("PUT")
	api.Handle("/artifacts/{repo}/{version}/{path:.*}", requirePermission(s.authService, models.ActionDownload, models.ResourceArtifact, metricsHandler)(
		http.HandlerFunc(artifactHandler.DownloadArtifact))).Methods("GET")
	api.Handle("/artifacts/{repo}/query", requirePermission(s.authService, models.ActionDownload, models.ResourceArtifact, metricsHandler)(
		http.HandlerFunc(artifactHandler.QueryDownloadArtifacts))).Methods("GET")
	api.Handle("/artifacts/{repo}/{version}/{path:.*}", requirePermission(s.authService, models.ActionDelete, models.ResourceArtifact, metricsHandler)(
		http.HandlerFunc(artifactHandler.DeleteArtifact))).Methods("DELETE")
	api.Handle("/artifacts/{repo}/versions", requirePermission(s.authService, models.ActionView, models.ResourceArtifact, metricsHandler)(
		http.HandlerFunc(artifactHandler.ListVersions))).Methods("GET")
	api.Handle("/artifacts/{repo}/{id}/metadata", requirePermission(s.authService, models.ActionUpdate, models.ResourceArtifact, metricsHandler)(
		http.HandlerFunc(artifactHandler.UpdateMetadata))).Methods("PUT")
	api.Handle("/artifacts/{repo}/{id}/properties", requirePermission(s.authService, models.ActionUpdate, models.ResourceArtifact, metricsHandler)(
		http.HandlerFunc(artifactHandler.UpdateProperties))).Methods("PUT")
	api.Handle("/artifacts/search", requirePermission(s.authService, models.ActionView, models.ResourceArtifact, metricsHandler)(
		http.HandlerFunc(artifactHandler.SearchArtifacts))).Methods("GET")
	api.Handle("/artifacts/{repo}/{id}/rename", requirePermission(s.authService, models.ActionUpdate, models.ResourceArtifact, metricsHandler)(
		http.HandlerFunc(artifactHandler.RenameArtifact))).Methods("PUT")

	// REGISTRY ROUTES
	regAPI := s.router.PathPrefix("/v2").Subrouter()
	regAPI.Use(s.authMiddleware.RegistryAuthMiddleware)

	// MANIFEST OPERATIONS
	regAPI.Handle("/{name:.*}/manifests/{reference}", requirePermission(s.authService, models.ActionPull, models.ResourceImage, metricsHandler)(
		http.HandlerFunc(repoHandler.HandleManifest))).Methods("GET", "HEAD")
	regAPI.Handle("/{name:.*}/manifests/{reference}", requirePermission(s.authService, models.ActionPush, models.ResourceImage, metricsHandler)(
		http.HandlerFunc(repoHandler.HandleManifest))).Methods("PUT")

	// CATALOGUE OPERATIONS
	regAPI.Handle("/", requirePermission(s.authService, models.ActionView, models.ResourceImage, metricsHandler)(
		http.HandlerFunc(repoHandler.ListRepositories))).Methods("GET")

	// BLOB OPERATIONS
	regAPI.Handle("/{name:.*}/blobs/{digest}", requirePermission(s.authService, models.ActionPull, models.ResourceImage, metricsHandler)(
		http.HandlerFunc(repoHandler.GetBlob))).Methods("GET", "HEAD")
	regAPI.Handle("/{name:.*}/blobs/uploads/{uuid}", // NO AUTH HERE FOR OFFSET CHECK
		http.HandlerFunc(repoHandler.GetBlobUploadOffset)).Methods("HEAD")
	regAPI.Handle("/{name:.*}/blobs/uploads/", requirePermission(s.authService, models.ActionPush, models.ResourceImage, metricsHandler)(
		http.HandlerFunc(repoHandler.InitiateBlobUpload))).Methods("POST")
	regAPI.Handle("/{name:.*}/blobs/uploads/{uuid}", requirePermission(s.authService, models.ActionPush, models.ResourceImage, metricsHandler)(
		http.HandlerFunc(repoHandler.HandleBlobUpload))).Methods("PATCH")
	regAPI.Handle("/{name:.*}/blobs/uploads/{uuid}", requirePermission(s.authService, models.ActionPush, models.ResourceImage, metricsHandler)(
		http.HandlerFunc(repoHandler.CompleteBlobUpload))).Methods("PUT")

	// TAG OPERATIONS
	regAPI.Handle("/{name:.*}/tags/list", requirePermission(s.authService, models.ActionView, models.ResourceTag, metricsHandler)(
		http.HandlerFunc(repoHandler.ListTags))).Methods("GET")

	// ALL V2 DELETE OPERATIONS
	regAPI.Handle("/{name:.*}/manifests/{reference}", requirePermission(s.authService, models.ActionDelete, models.ResourceImage, metricsHandler)(
		http.HandlerFunc(repoHandler.DeleteManifest))).Methods("DELETE")
	regAPI.Handle("/{name:.*}/blobs/{digest}", requirePermission(s.authService, models.ActionDelete, models.ResourceImage, metricsHandler)(
		http.HandlerFunc(repoHandler.DeleteBlob))).Methods("DELETE")
	regAPI.Handle("/{name:.*}/tags/{tag}", requirePermission(s.authService, models.ActionDelete, models.ResourceTag, metricsHandler)(
		http.HandlerFunc(repoHandler.DeleteBlob))).Methods("DELETE")

	// STATIC FILES
	if os.Getenv("GO_ENV") == "production" {
		staticPath := "web/build"
		if _, err := os.Stat(staticPath); os.IsNotExist(err) {
			return fmt.Errorf("STATIC DIRECTORY NOT FOUND: %s", staticPath)
		}
		spa := handlers.NewSPAHandler(s.config, staticPath, "200.html")
		s.router.PathPrefix("/").Handler(spa)
	}

	return nil
}

// FOR ROUTE AUTH + ACCESS LOG
type responseWriter struct {
	http.ResponseWriter
	status int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.status = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Write(b []byte) (int, error) {
	if rw.status == 0 {
		rw.status = 200
	}
	return rw.ResponseWriter.Write(b)
}

func requirePermission(auth auth.AuthService, action models.Action, resource models.Resource, m *handlers.MetricsHandler) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			username := r.Context().Value(constants.UsernameKey).(string)
			rw := &responseWriter{ResponseWriter: w}
			if !auth.HasPermission(r.Context(), username, models.Permission{
				Action:   action,
				Resource: resource,
			}) {
				if m != nil {
					m.LogAccess(username, string(action), string(resource), r, http.StatusForbidden)
				}

				http.Error(w, "FORBIDDEN", http.StatusForbidden)
				return
			}
			next.ServeHTTP(rw, r)
			if m != nil {
				m.LogAccess(username, string(action), string(resource), r, rw.status)
			}
		})
	}
}

func loadRSAKeys(keyPath string) (*rsa.PrivateKey, *rsa.PublicKey, error) {
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, fmt.Errorf("FAILED TO READ KEY FILE: %v", err)
	}

	privateKey, err := jwt.ParseRSAPrivateKeyFromPEM(keyData)
	if err != nil {
		return nil, nil, fmt.Errorf("FAILED TO PARSE PRIVATE KEY: %v", err)
	}

	return privateKey, &privateKey.PublicKey, nil
}

func (s *Server) Start() error {
	transport := &http.Transport{
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
		ResponseHeaderTimeout: 30 * time.Second,

		// NEED FOR TRAEFIK ON OUTGOING
		DisableKeepAlives: false,
		ProxyConnectHeader: http.Header{
			"User-Agent": []string{"DistroFace/1.0"},
		},
	}

	httpClient := &http.Client{
		Transport: transport,
		Timeout:   30 * time.Minute,
	}
	http.DefaultClient = httpClient
	s.client = httpClient

	srv := &http.Server{
		Addr:              fmt.Sprintf("%s:%s", s.config.Server.Domain, s.config.Server.Port),
		ReadTimeout:       30 * time.Minute,
		WriteTimeout:      30 * time.Minute,
		ReadHeaderTimeout: 60 * time.Second,
		IdleTimeout:       120 * time.Second,
		Handler:           s.router,
	}

	// SETUP TLS IF CONFIGURED
	if s.config.Server.TLSKeyFile != "" && s.config.Server.TLSCertFile != "" {
		srv.TLSConfig = &tls.Config{
			MinVersion: tls.VersionTLS12,
			CurvePreferences: []tls.CurveID{
				tls.X25519,
				tls.CurveP256,
			},
			PreferServerCipherSuites: true,
			CipherSuites: []uint16{
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
			},
		}
		return srv.ListenAndServeTLS(s.config.Server.TLSCertFile, s.config.Server.TLSKeyFile)
	}

	return srv.ListenAndServe()
}

func (s *Server) Shutdown() {
	if s.db != nil {
		s.db.Close()
	}
}
