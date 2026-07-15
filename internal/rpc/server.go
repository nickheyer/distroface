package rpc

import (
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"connectrpc.com/grpcreflect"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/config"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/registry"
	"github.com/nickheyer/distroface/internal/rpc/services"
	"github.com/nickheyer/distroface/internal/webhook"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	web "github.com/nickheyer/distroface/web/distroface"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type Server struct {
	store   *storage.Store
	config  *config.Config
	log     *logger.Logger
	handler http.Handler

	registryHandler   http.Handler
	registryAccess    *registry.RegistryAccess
	tokenHandler      *auth.TokenHandler
	authManager       *auth.Manager
	enforcer          *rbac.Enforcer
	oidcHandler       *auth.OIDCHandler
	webhookDispatcher *webhook.Dispatcher
	portalResolver    *registry.PortalResolver
}

type ServerDeps struct {
	Store             *storage.Store
	Config            *config.Config
	Log               *logger.Logger
	RegistryHandler   http.Handler
	RegistryAccess    *registry.RegistryAccess
	TokenHandler      *auth.TokenHandler
	AuthManager       *auth.Manager
	Enforcer          *rbac.Enforcer
	OIDCHandler       *auth.OIDCHandler
	WebhookDispatcher *webhook.Dispatcher
	PortalResolver    *registry.PortalResolver
}

func NewServer(deps ServerDeps) *Server {
	s := &Server{
		store:             deps.Store,
		config:            deps.Config,
		log:               deps.Log,
		registryHandler:   deps.RegistryHandler,
		registryAccess:    deps.RegistryAccess,
		tokenHandler:      deps.TokenHandler,
		authManager:       deps.AuthManager,
		enforcer:          deps.Enforcer,
		oidcHandler:       deps.OIDCHandler,
		webhookDispatcher: deps.WebhookDispatcher,
		portalResolver:    deps.PortalResolver,
	}
	s.setupHandler()
	return s
}

func (s *Server) setupHandler() {
	mux := http.NewServeMux()

	interceptors := []connect.Interceptor{
		connect.UnaryInterceptorFunc(s.authInterceptor()),
		&loggingInterceptor{log: s.log},
	}

	opts := []connect.HandlerOption{
		connect.WithInterceptors(interceptors...),
	}

	// Registry handler (OCI Distribution API)
	if s.registryHandler != nil {
		mux.Handle("/v2/", s.registryHandler)
	}

	// Docker token auth endpoint
	if s.tokenHandler != nil {
		mux.Handle("GET /auth/token", s.tokenHandler)
		mux.Handle("POST /auth/token", s.tokenHandler)
	}

	// OIDC HTTP handlers (not Connect RPC — these are OAuth2 redirect flows)
	if s.oidcHandler != nil && s.oidcHandler.IsEnabled() {
		mux.HandleFunc("/api/v1/auth/oidc/login", s.oidcHandler.HandleLogin)
		mux.HandleFunc("/api/v1/auth/oidc/callback", s.oidcHandler.HandleCallback)
	}

	// Register RPC services
	healthService := services.NewHealthService(s.log)
	healthPath, healthHandler := distrofacev1connect.NewHealthServiceHandler(healthService, opts...)
	mux.Handle(healthPath, healthHandler)

	authService := services.NewAuthService(s.store, s.config, s.authManager, s.enforcer, s.oidcHandler, s.log)
	authPath, authHandler := distrofacev1connect.NewAuthServiceHandler(authService, opts...)
	mux.Handle(authPath, authHandler)

	userService := services.NewUserService(s.store, s.authManager, s.enforcer, s.log)
	userPath, userHandler := distrofacev1connect.NewUserServiceHandler(userService, opts...)
	mux.Handle(userPath, userHandler)

	repoService := services.NewRepositoryService(s.store, s.registryAccess, s.enforcer, s.log)
	repoPath, repoHandler := distrofacev1connect.NewRepositoryServiceHandler(repoService, opts...)
	mux.Handle(repoPath, repoHandler)

	configService := services.NewConfigurationService(s.config, s.log)
	configPath, configHandler := distrofacev1connect.NewConfigurationServiceHandler(configService, opts...)
	mux.Handle(configPath, configHandler)

	roleService := services.NewRoleService(s.store, s.enforcer, s.log)
	rolePath, roleHandler := distrofacev1connect.NewRoleServiceHandler(roleService, opts...)
	mux.Handle(rolePath, roleHandler)

	tokenService := services.NewTokenService(s.authManager, s.enforcer, s.log)
	tokenSvcPath, tokenSvcHandler := distrofacev1connect.NewTokenServiceHandler(tokenService, opts...)
	mux.Handle(tokenSvcPath, tokenSvcHandler)

	orgService := services.NewOrganizationService(s.store, s.registryAccess, s.enforcer, s.log)
	orgPath, orgHandler := distrofacev1connect.NewOrganizationServiceHandler(orgService, opts...)
	mux.Handle(orgPath, orgHandler)

	webhookService := services.NewWebhookService(s.store, s.enforcer, s.webhookDispatcher, s.log)
	webhookPath, webhookHandler := distrofacev1connect.NewWebhookServiceHandler(webhookService, opts...)
	mux.Handle(webhookPath, webhookHandler)

	if s.portalResolver != nil {
		portalService := services.NewPortalService(s.store, s.enforcer, s.portalResolver, s.config, s.log)
		portalPath, portalHandler := distrofacev1connect.NewPortalServiceHandler(portalService, opts...)
		mux.Handle(portalPath, portalHandler)
	}

	// gRPC reflection
	reflector := grpcreflect.NewStaticReflector(
		distrofacev1connect.HealthServiceName,
		distrofacev1connect.AuthServiceName,
		distrofacev1connect.UserServiceName,
		distrofacev1connect.RepositoryServiceName,
		distrofacev1connect.ConfigurationServiceName,
		distrofacev1connect.RoleServiceName,
		distrofacev1connect.TokenServiceName,
		distrofacev1connect.OrganizationServiceName,
		distrofacev1connect.WebhookServiceName,
		distrofacev1connect.PortalServiceName,
	)
	mux.Handle(grpcreflect.NewHandlerV1(reflector))
	mux.Handle(grpcreflect.NewHandlerV1Alpha(reflector))

	// Serve frontend for non-RPC routes
	s.setupFrontend(mux)

	s.handler = h2c.NewHandler(mux, &http2.Server{})
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

func (s *Server) setupFrontend(mux *http.ServeMux) {
	fs := s.getFrontendFS()
	if fs == nil {
		s.log.Warn("No frontend found - API only mode")
		return
	}
	mux.Handle("/", s.createFrontendHandler(fs))
}

func (s *Server) getFrontendFS() http.FileSystem {
	if buildFS, err := web.BuildFS(); err == nil {
		s.log.Info("Using embedded frontend")
		return http.FS(buildFS)
	}
	return nil
}

func (s *Server) createFrontendHandler(fs http.FileSystem) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if isConnectPath(r.URL.Path) {
			http.NotFound(w, r)
			return
		}

		path := r.URL.Path
		if path == "/" {
			path = "/index.html"
		}

		file, err := fs.Open(path)
		if err == nil {
			defer file.Close()
			stat, _ := file.Stat()
			http.ServeContent(w, r, path, stat.ModTime(), file)
			return
		}

		indexFile, err := fs.Open("/index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer indexFile.Close()

		stat, _ := indexFile.Stat()
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		http.ServeContent(w, r, "/index.html", stat.ModTime(), indexFile)
	}
}

func isConnectPath(path string) bool {
	connectPrefixes := []string{
		"/distroface.v1.",
		"/grpc.reflection.",
		"/connect.",
	}
	for _, prefix := range connectPrefixes {
		if strings.HasPrefix(path, prefix) {
			return true
		}
	}
	return false
}
