package server

import (
	"context"
	"crypto/rsa"
	"crypto/tls"
	"database/sql"
	"fmt"
	"net/http"
	"os"

	dconfig "github.com/distribution/distribution/v3/configuration"
	_ "github.com/distribution/distribution/v3/registry/auth/token"
	_ "github.com/distribution/distribution/v3/registry/storage/driver/filesystem"
	"github.com/golang-jwt/jwt/v4"
	"github.com/gorilla/mux"
	_ "github.com/mattn/go-sqlite3"

	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/auth/permissions"
	"github.com/nickheyer/distroface/internal/config"
	"github.com/nickheyer/distroface/internal/constants"
	"github.com/nickheyer/distroface/internal/handlers"
	"github.com/nickheyer/distroface/internal/models"
	"github.com/nickheyer/distroface/internal/repository"
	"github.com/nickheyer/distroface/internal/server/middleware"
)

type Server struct {
	config         *config.Config
	distConfig     *dconfig.Configuration
	router         *mux.Router
	ctx            context.Context
	db             *sql.DB
	authService    auth.AuthService
	authMiddleware *auth.Middleware
	permManager    *permissions.PermissionManager
}

func NewServer(cfg *config.Config) (*Server, error) {
	ctx := context.Background()

	// INIT DATABASE
	db, err := initDB(cfg.Database.Path)
	if err != nil {
		return nil, fmt.Errorf("DATABASE INIT FAILED: %v", err)
	}

	// READ RSA KEYS
	signKey, verifyKey, err := loadRSAKeys(cfg.Server.RSAKeyFile)
	if err != nil {
		return nil, fmt.Errorf("FAILED TO LOAD RSA KEYS: %v", err)
	}

	// INIT REPOSITORY
	repo := repository.NewSQLiteRepository(db)

	// INIT PERMISSION MANAGER
	permManager := permissions.NewPermissionManager(db)

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
	}

	if err := s.setupRoutes(); err != nil {
		db.Close()
		return nil, fmt.Errorf("ROUTE SETUP FAILED: %v", err)
	}

	return s, nil
}

func initDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("DB CONNECTION FAILED: %v", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("DB PING FAILED: %v", err)
	}

	return db, nil
}

func (s *Server) setupRoutes() error {
	s.router.Use(middleware.LoggingMiddleware)
	s.router.Use(middleware.CORS)

	// INIT HANDLERS
	repo := repository.NewSQLiteRepository(s.db)
	authHandler := handlers.NewAuthHandler(s.config, s.authService)
	userHandler := handlers.NewUserHandler(repo, s.permManager)
	repoHandler := handlers.NewRepositoryHandler(repo, s.config)
	groupHandler := handlers.NewGroupHandler(repo)
	roleHandler := handlers.NewRoleHandler(repo, s.permManager)

	// PUBLIC ROUTES
	s.router.HandleFunc("/v2/", authHandler.HandleV2Check)
	s.router.HandleFunc("/auth/token", authHandler.HandleRegistryAuth).Methods("GET", "POST")
	s.router.HandleFunc("/api/v1/auth/login", authHandler.HandleWebLogin).Methods("POST")
	s.router.HandleFunc("/api/v1/auth/refresh", authHandler.HandleTokenRefresh).Methods("POST")

	// API ROUTES
	api := s.router.PathPrefix("/api/v1").Subrouter()
	api.Use(s.authMiddleware.AuthMiddleware)

	// REPOSITORY ROUTES
	api.Handle("/repositories", requirePermission(s.authService, models.ActionView, models.ResourceWebUI)(
		http.HandlerFunc(repoHandler.ListRepositories))).Methods("GET")
	api.Handle("/repositories/{name}/tags/{tag}", requirePermission(s.authService, models.ActionDelete, models.ResourceTag)(
		http.HandlerFunc(repoHandler.DeleteTag))).Methods("DELETE")
	api.Handle("/repositories/public", requirePermission(s.authService, models.ActionView, models.ResourceWebUI)(
		http.HandlerFunc(repoHandler.ListGlobalRepositories))).Methods("GET")
	api.Handle("/repositories/visibility", requirePermission(s.authService, models.ActionUpdate, models.ResourceWebUI)(
		http.HandlerFunc(repoHandler.UpdateImageVisibility))).Methods("POST")

	// USER MANAGEMENT
	api.Handle("/users", requirePermission(s.authService, models.ActionCreate, models.ResourceUser)(
		http.HandlerFunc(userHandler.CreateUser))).Methods("POST")
	api.Handle("/users/groups", requirePermission(s.authService, models.ActionUpdate, models.ResourceUser)(
		http.HandlerFunc(userHandler.UpdateUserGroups))).Methods("PUT")
	api.Handle("/users", requirePermission(s.authService, models.ActionView, models.ResourceUser)(
		http.HandlerFunc(userHandler.ListUsers))).Methods("GET")
	api.HandleFunc("/users/me", userHandler.GetUser).Methods("GET")
	api.Handle("/users/{username}", requirePermission(s.authService, models.ActionView, models.ResourceUser)(
		http.HandlerFunc(userHandler.GetUser))).Methods("GET")

	// ROLE MANAGEMENT
	api.Handle("/roles", requirePermission(s.authService, models.ActionView, models.ResourceSystem)(
		http.HandlerFunc(roleHandler.ListRoles))).Methods("GET")
	api.Handle("/roles", requirePermission(s.authService, models.ActionAdmin, models.ResourceSystem)(
		http.HandlerFunc(roleHandler.CreateRole))).Methods("POST")
	api.Handle("/roles/{name}", requirePermission(s.authService, models.ActionAdmin, models.ResourceSystem)(
		http.HandlerFunc(roleHandler.UpdateRole))).Methods("PUT")
	api.Handle("/roles/{name}", requirePermission(s.authService, models.ActionAdmin, models.ResourceSystem)(
		http.HandlerFunc(roleHandler.DeleteRole))).Methods("DELETE")

	// GROUP MANAGEMENT
	api.Handle("/groups", requirePermission(s.authService, models.ActionView, models.ResourceGroup)(
		http.HandlerFunc(groupHandler.ListGroups))).Methods("GET")
	api.Handle("/groups/{name}", requirePermission(s.authService, models.ActionUpdate, models.ResourceGroup)(
		http.HandlerFunc(groupHandler.UpdateGroup))).Methods("PUT")
	api.Handle("/groups/{name}", requirePermission(s.authService, models.ActionDelete, models.ResourceGroup)(
		http.HandlerFunc(groupHandler.DeleteGroup))).Methods("DELETE")

	// MIGRATION ROUTES
	api.Handle("/registry/migrate", requirePermission(s.authService, models.ActionMigrate, models.ResourceTask)(
		http.HandlerFunc(repoHandler.MigrateImages))).Methods("POST")
	api.Handle("/registry/migrate/status", requirePermission(s.authService, models.ActionMigrate, models.ResourceTask)(
		http.HandlerFunc(repoHandler.GetMigrationStatus))).Methods("GET")
	api.Handle("/registry/proxy/catalog", requirePermission(s.authService, models.ActionMigrate, models.ResourceTask)(
		http.HandlerFunc(repoHandler.ProxyCatalog))).Methods("GET")
	api.Handle("/registry/proxy/tags", requirePermission(s.authService, models.ActionMigrate, models.ResourceTask)(
		http.HandlerFunc(repoHandler.ProxyTags))).Methods("GET")

	// REGISTRY ROUTES
	regAPI := s.router.PathPrefix("/v2").Subrouter()
	regAPI.Use(s.authMiddleware.RegistryAuthMiddleware)

	// MANIFEST OPERATIONS
	regAPI.Handle("/{name}/manifests/{reference}", requirePermission(s.authService, models.ActionPull, models.ResourceImage)(
		http.HandlerFunc(repoHandler.HandleManifest))).Methods("GET", "HEAD")
	regAPI.Handle("/{name}/manifests/{reference}", requirePermission(s.authService, models.ActionPush, models.ResourceImage)(
		http.HandlerFunc(repoHandler.HandleManifest))).Methods("PUT")

	// CATALOGUE OPERATIONS
	regAPI.Handle("/", requirePermission(s.authService, models.ActionView, models.ResourceImage)(
		http.HandlerFunc(repoHandler.ListRepositories))).Methods("GET")

	// BLOB OPERATIONS
	regAPI.Handle("/{name}/blobs/{digest}", requirePermission(s.authService, models.ActionPull, models.ResourceImage)(
		http.HandlerFunc(repoHandler.GetBlob))).Methods("GET", "HEAD")
	regAPI.Handle("/{name}/blobs/uploads/", requirePermission(s.authService, models.ActionPush, models.ResourceImage)(
		http.HandlerFunc(repoHandler.InitiateBlobUpload))).Methods("POST")
	regAPI.Handle("/{name}/blobs/uploads/{uuid}", requirePermission(s.authService, models.ActionPush, models.ResourceImage)(
		http.HandlerFunc(repoHandler.HandleBlobUpload))).Methods("PATCH")
	regAPI.Handle("/{name}/blobs/uploads/{uuid}", requirePermission(s.authService, models.ActionPush, models.ResourceImage)(
		http.HandlerFunc(repoHandler.CompleteBlobUpload))).Methods("PUT")

	// TAG OPERATIONS
	regAPI.Handle("/{name}/tags/list", requirePermission(s.authService, models.ActionView, models.ResourceTag)(
		http.HandlerFunc(repoHandler.ListTags))).Methods("GET")

	// ALL V2 DELETE OPERATIONS
	regAPI.Handle("/{name}/manifests/{reference}", requirePermission(s.authService, models.ActionDelete, models.ResourceImage)(
		http.HandlerFunc(repoHandler.DeleteManifest))).Methods("DELETE")
	regAPI.Handle("/{name}/blobs/{digest}", requirePermission(s.authService, models.ActionDelete, models.ResourceImage)(
		http.HandlerFunc(repoHandler.DeleteBlob))).Methods("DELETE")
	regAPI.Handle("/{name}/tags/{tag}", requirePermission(s.authService, models.ActionDelete, models.ResourceTag)(
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

func requirePermission(auth auth.AuthService, action models.Action, resource models.Resource) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			username := r.Context().Value(constants.UsernameKey).(string)
			fmt.Printf("\nPERMISSION CHECK STARTED: USER: %v ACTION: %v RESOURCE: %v\n\n", username, action, resource)
			if !auth.HasPermission(r.Context(), username, models.Permission{
				Action:   action,
				Resource: resource,
			}) {
				fmt.Printf("\nPERMISSION CHECK FAILED: USER: %v ACTION: %v RESOURCE: %v\n\n", username, action, resource)
				http.Error(w, "FORBIDDEN", http.StatusForbidden)
				return
			}
			fmt.Printf("\nPERMISSION CHECK PASSED: USER: %v ACTION: %v RESOURCE: %v\n\n", username, action, resource)
			next.ServeHTTP(w, r)
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
	srv := &http.Server{
		Addr:    fmt.Sprintf("%s:%s", s.config.Server.Domain, s.config.Server.Port),
		Handler: s.router,
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
