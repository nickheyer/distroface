package rpc

import (
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"connectrpc.com/grpcreflect"
	"github.com/nickheyer/distroface/internal/admin"
	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/certs"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/registry"
	"github.com/nickheyer/distroface/internal/rpc/services"
	"github.com/nickheyer/distroface/internal/webhook"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"github.com/nickheyer/distroface/pkg/utils"
	web "github.com/nickheyer/distroface/web/distroface"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type ServerDeps struct {
	Store             *stores.Store
	Config            *config.Config
	Log               *logger.Logger
	RegistryHandler   http.Handler
	RegistryAccess    *registry.RegistryAccess
	TokenHandler      *auth.TokenHandler
	AuthManager       *auth.Manager
	Enforcer          *rbac.Enforcer
	OIDCHandler       *auth.OIDCHandler
	WebhookDispatcher *webhook.Dispatcher
	PortalResolver    *portal.Resolver
	PortalService     *portal.Service
	AuthLimiter       *admin.Limiter // Lockout limiter nil disables
	ArtifactManager   *artifacts.Manager
	ArtifactV1Facade  *artifacts.V1API
	GCCollector       *admin.Collector
	CertService       *certs.Service  // Nil hides the certificate api
	AuditRecorder     *audit.Recorder // Nil disables the audit trail
	AuditService      *audit.Service
}

type Server struct {
	ServerDeps
	handler http.Handler
}

func NewServer(deps ServerDeps) *Server {
	s := &Server{ServerDeps: deps}
	s.setupHandler()
	return s
}

func (s *Server) setupHandler() {
	mux := http.NewServeMux()

	interceptors := []connect.Interceptor{
		connect.UnaryInterceptorFunc(s.rateLimitInterceptor()),
		connect.UnaryInterceptorFunc(s.authInterceptor()),
		&loggingInterceptor{log: s.Log},
	}
	if s.AuditRecorder != nil {
		interceptors = append(interceptors, connect.UnaryInterceptorFunc(s.auditInterceptor(s.AuditRecorder)))
	}

	opts := []connect.HandlerOption{
		connect.WithInterceptors(interceptors...),
	}

	// Registry handler (OCI Distribution API)
	if s.RegistryHandler != nil {
		mux.Handle("/v2/", s.RegistryHandler)
	}

	// Docker token auth endpoint
	if s.TokenHandler != nil {
		mux.Handle("GET /auth/token", s.TokenHandler)
		mux.Handle("POST /auth/token", s.TokenHandler)
	}

	// OIDC HTTP handlers (not Connect RPC - these are OAuth2 redirect flows)
	if s.OIDCHandler != nil && s.OIDCHandler.IsEnabled() {
		mux.HandleFunc("/api/v1/auth/oidc/login", s.OIDCHandler.HandleLogin)
		mux.HandleFunc("/api/v1/auth/oidc/callback", s.OIDCHandler.HandleCallback)
	}

	// V1 artifact facade for old dfcli and ci
	if s.ArtifactV1Facade != nil && s.Config.Artifacts.V1Compat {
		s.ArtifactV1Facade.RegisterAuth(mux)
		s.ArtifactV1Facade.RegisterArtifacts(mux)
	}

	// Register RPC services
	healthService := services.NewHealthService(s.Log)
	healthPath, healthHandler := distrofacev1connect.NewHealthServiceHandler(healthService, opts...)
	mux.Handle(healthPath, healthHandler)

	authService := services.NewAuthService(s.Store, s.Config, s.AuthManager, s.Enforcer, s.OIDCHandler, s.Log)
	authPath, authHandler := distrofacev1connect.NewAuthServiceHandler(authService, opts...)
	mux.Handle(authPath, authHandler)

	userService := services.NewUserService(s.Store, s.AuthManager, s.Enforcer, s.Log)
	userPath, userHandler := distrofacev1connect.NewUserServiceHandler(userService, opts...)
	mux.Handle(userPath, userHandler)

	repoService := services.NewRepositoryService(s.Store, s.RegistryAccess, s.Enforcer, s.Log)
	repoPath, repoHandler := distrofacev1connect.NewRepositoryServiceHandler(repoService, opts...)
	mux.Handle(repoPath, repoHandler)

	configService := services.NewConfigurationService(s.Store, s.Config, s.Log)
	configPath, configHandler := distrofacev1connect.NewConfigurationServiceHandler(configService, opts...)
	mux.Handle(configPath, configHandler)

	roleService := services.NewRoleService(s.Store, s.Enforcer, s.Log)
	rolePath, roleHandler := distrofacev1connect.NewRoleServiceHandler(roleService, opts...)
	mux.Handle(rolePath, roleHandler)

	tokenService := services.NewTokenService(s.AuthManager, s.Enforcer, s.Log)
	tokenSvcPath, tokenSvcHandler := distrofacev1connect.NewTokenServiceHandler(tokenService, opts...)
	mux.Handle(tokenSvcPath, tokenSvcHandler)

	orgService := services.NewOrganizationService(s.Store, s.RegistryAccess, s.Enforcer, s.Config, s.Log)
	orgPath, orgHandler := distrofacev1connect.NewOrganizationServiceHandler(orgService, opts...)
	mux.Handle(orgPath, orgHandler)

	webhookService := services.NewWebhookService(s.Store, s.Enforcer, s.WebhookDispatcher, s.Log)
	webhookPath, webhookHandler := distrofacev1connect.NewWebhookServiceHandler(webhookService, opts...)
	mux.Handle(webhookPath, webhookHandler)

	if s.PortalService != nil {
		portalPath, portalHandler := distrofacev1connect.NewPortalServiceHandler(s.PortalService, opts...)
		mux.Handle(portalPath, portalHandler)
	}

	if s.ArtifactManager != nil {
		artifactService := services.NewArtifactService(s.Store, s.ArtifactManager, s.Enforcer, s.Log)
		artifactPath, artifactHandler := distrofacev1connect.NewArtifactServiceHandler(artifactService, opts...)
		mux.Handle(artifactPath, artifactHandler)
	}

	if s.GCCollector != nil {
		gcService := services.NewGCService(s.GCCollector, s.Config, s.Log)
		gcPath, gcHandler := distrofacev1connect.NewGCServiceHandler(gcService, opts...)
		mux.Handle(gcPath, gcHandler)
	}

	if s.CertService != nil {
		certPath, certHandler := distrofacev1connect.NewCertificateServiceHandler(s.CertService, opts...)
		mux.Handle(certPath, certHandler)
	}

	if s.AuditService != nil {
		auditPath, auditHandler := distrofacev1connect.NewAuditServiceHandler(s.AuditService, opts...)
		mux.Handle(auditPath, auditHandler)
	}

	// GRPC reflection
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
		distrofacev1connect.ArtifactServiceName,
		distrofacev1connect.GCServiceName,
		distrofacev1connect.CertificateServiceName,
		distrofacev1connect.AuditServiceName,
	)
	reflectV1Path, reflectV1Handler := grpcreflect.NewHandlerV1(reflector)
	mux.Handle(reflectV1Path, s.requireAuth(reflectV1Handler))
	reflectV1AlphaPath, reflectV1AlphaHandler := grpcreflect.NewHandlerV1Alpha(reflector)
	mux.Handle(reflectV1AlphaPath, s.requireAuth(reflectV1AlphaHandler))

	// Serve frontend for non-RPC routes
	s.setupFrontend(mux)

	// Portal hosts get the whole app, org scoped by the resolved portal
	var root http.Handler = mux
	if s.PortalResolver != nil {
		root = s.PortalResolver.Middleware(s.Config.Server.Hostname, mux)
	}
	root = utils.Headers(s.Config.Security.Headers, s.Config.TLS.Enabled, root)
	s.handler = h2c.NewHandler(root, &http2.Server{})
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

// Gate plain http handlers behind session or token auth
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.AuthManager.IsAnyAuthEnabled() {
			next.ServeHTTP(w, r)
			return
		}

		token := auth.ExtractToken(r.Header)
		if token == "" {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		if _, err := s.AuthManager.ValidateToken(r.Context(), token); err != nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

func (s *Server) setupFrontend(mux *http.ServeMux) {
	fs := s.getFrontendFS()
	if fs == nil {
		s.Log.Warn("No frontend found - API only mode")
		return
	}
	mux.Handle("/", s.createFrontendHandler(fs))
}

func (s *Server) getFrontendFS() http.FileSystem {
	if buildFS, err := web.BuildFS(); err == nil {
		s.Log.Info("Using embedded frontend")
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
