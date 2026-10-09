package rpc

import (
	"context"
	"net/http"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/grpcreflect/v2"
	"github.com/nickheyer/distroface/internal/admin"
	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/certs"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/mirror"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/registry"
	"github.com/nickheyer/distroface/internal/rpc/services"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/internal/webhook"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"github.com/nickheyer/distroface/pkg/utils"
	web "github.com/nickheyer/distroface/web/distroface"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"
)

type ServerDeps struct {
	Store               *stores.Store
	Resolver            *settings.Resolver
	Log                 *logger.Logger
	RegistryHandler     http.Handler
	RegistryAccess      *registry.RegistryAccess
	RegistryStoragePath string
	TokenHandler        *auth.TokenHandler
	AuthManager         *auth.Manager
	Enforcer            *rbac.Enforcer
	OIDCHandler         *auth.OIDCHandler
	WebhookDispatcher   *webhook.Dispatcher
	PortalResolver      *portal.Resolver
	PortalService       *portal.Service
	CertEngine          *certs.Engine
	ACMEServer          *certs.ACMEServer // Nil hides the built in acme directory
	AuthLimiter         *admin.Limiter    // Lockout limiter nil disables
	ArtifactManager     *artifacts.Manager
	ArtifactV1Facade    *artifacts.V1API
	MirrorMonitor       *mirror.Monitor
	GCCollector         *admin.Collector
	CertService         *certs.Service  // Nil hides the certificate api
	AuditRecorder       *audit.Recorder // Nil disables the audit trail
	AuditService        *audit.Service
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

	interceptors := []connect.ServerInterceptor{
		s.rateLimitInterceptor(),
		s.authInterceptor(),
		s.loggingInterceptor(),
	}
	if s.AuditRecorder != nil {
		interceptors = append(interceptors, s.auditInterceptor(s.AuditRecorder))
	}
	rpcServer := connect.NewServer(interceptors...)

	// Registry handler (OCI Distribution API)
	if s.RegistryHandler != nil {
		mux.Handle("/v2/", s.RegistryHandler)
	}

	// Docker token auth endpoint
	if s.TokenHandler != nil {
		mux.Handle("GET /auth/token", s.TokenHandler)
		mux.Handle("POST /auth/token", s.TokenHandler)
	}

	// Built in acme directory, public and self gating on the live toggle
	if s.ACMEServer != nil {
		mux.Handle("/acme/", s.ACMEServer.Handler())
	}

	// Instance trust anchor, public so docker and dfcli can fetch it
	if s.CertEngine != nil {
		mux.Handle("/.well-known/distroface/ca.pem", s.CertEngine.TrustBundleHandler())
	}

	// OIDC HTTP handlers (not Connect RPC - these are OAuth2 redirect flows)
	// Registered unconditionally, handlers self gate on runtime settings
	if s.OIDCHandler != nil {
		mux.HandleFunc("/api/v1/auth/oidc/login", s.OIDCHandler.HandleLogin)
		mux.HandleFunc("/api/v1/auth/oidc/callback", s.OIDCHandler.HandleCallback)
	}

	// V1 artifact facade for old dfcli and ci, gated per request
	if s.ArtifactV1Facade != nil {
		v1mux := http.NewServeMux()
		s.ArtifactV1Facade.RegisterAuth(v1mux)
		s.ArtifactV1Facade.RegisterArtifacts(v1mux)
		mux.Handle("/api/v1/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !s.Resolver.System(r.Context()).GetArtifacts().GetV1Compat() {
				http.NotFound(w, r)
				return
			}
			v1mux.ServeHTTP(w, r)
		}))
	}

	// Register RPC services
	distrofacev1connect.RegisterHealthServiceHandler(rpcServer, services.NewHealthService(s.Log))
	distrofacev1connect.RegisterAuthServiceHandler(rpcServer, services.NewAuthService(s.Store, s.AuthManager, s.Enforcer, s.OIDCHandler, s.Log))
	distrofacev1connect.RegisterUserServiceHandler(rpcServer, services.NewUserService(s.Store, s.AuthManager, s.Enforcer, s.Log))
	distrofacev1connect.RegisterRepositoryServiceHandler(rpcServer, services.NewRepositoryService(s.Store, s.RegistryAccess, s.Enforcer, s.MirrorMonitor, s.Log))
	distrofacev1connect.RegisterSettingsServiceHandler(rpcServer, services.NewSettingsService(s.Store, s.Resolver, s.Enforcer, s.Log))
	distrofacev1connect.RegisterRoleServiceHandler(rpcServer, services.NewRoleService(s.Store, s.Enforcer, s.Log))
	distrofacev1connect.RegisterTokenServiceHandler(rpcServer, services.NewTokenService(s.AuthManager, s.Enforcer, s.Log))
	distrofacev1connect.RegisterOrganizationServiceHandler(rpcServer, services.NewOrganizationService(s.Store, s.RegistryAccess, s.Enforcer, s.Resolver, s.Log))
	distrofacev1connect.RegisterWebhookServiceHandler(rpcServer, services.NewWebhookService(s.Store, s.Enforcer, s.WebhookDispatcher, s.Log))

	if s.PortalService != nil {
		distrofacev1connect.RegisterPortalServiceHandler(rpcServer, s.PortalService)
	}

	if s.ArtifactManager != nil {
		distrofacev1connect.RegisterArtifactServiceHandler(rpcServer, services.NewArtifactService(s.Store, s.ArtifactManager, s.Enforcer, s.MirrorMonitor, s.Log))
	}

	if s.MirrorMonitor != nil {
		distrofacev1connect.RegisterMirrorServiceHandler(rpcServer, services.NewMirrorService(s.MirrorMonitor, s.Enforcer,
			artifacts.NewAccess(s.Store, s.Enforcer), auth.NewRepoAccess(s.Store, s.Enforcer), s.Log))
	}

	// Registered even without a collector, it also serves storage usage
	distrofacev1connect.RegisterGCServiceHandler(rpcServer, services.NewGCService(s.GCCollector, s.Store, s.RegistryStoragePath, s.Resolver, s.Log))

	if s.CertService != nil {
		distrofacev1connect.RegisterCertificateServiceHandler(rpcServer, s.CertService)
	}

	if s.AuditService != nil {
		distrofacev1connect.RegisterAuditServiceHandler(rpcServer, s.AuditService)
	}
	connecthttp.Mount(mux, rpcServer)

	// Reflection on token only
	reflectServer := connect.NewServer(s.reflectionAuthInterceptor())
	grpcreflect.Register(reflectServer, grpcreflect.WithNamer(grpcreflect.NamerFunc(func() []string {
		return serviceNames(rpcServer)
	})))
	connecthttp.Mount(mux, reflectServer)

	// Serve frontend for non-RPC routes
	s.setupFrontend(mux)

	// Headers stay app only, proxied backends own their responses
	inner := utils.Headers(s.Resolver, s.httpsOnlyRedirect(mux))
	// Portal hosts get the whole app, org scoped by the resolved portal
	root := inner
	if s.PortalResolver != nil {
		root = s.PortalResolver.Middleware(s.primaryHostname, inner)
	}
	// Verified mtls identity rides the request context for auth and audit
	root = certs.ClientCertMiddleware(root)
	s.handler = h2c.NewHandler(root, &http2.Server{})
}

// Live public hostname for portal aware middleware
func (s *Server) primaryHostname() string {
	return s.Resolver.System(context.Background()).GetServer().GetPublicHostname()
}

// Cleartext app requests redirect when https only mode is on,
// enforcement waits until a certificate is actually servable
func (s *Server) httpsOnlyRedirect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Acme and the trust anchor must work before a client trusts our tls
		if strings.HasPrefix(r.URL.Path, "/acme/") || r.URL.Path == "/.well-known/distroface/ca.pem" {
			next.ServeHTTP(w, r)
			return
		}
		// Portals enforce their own scheme in portal middleware
		if r.TLS != nil || portal.FromContext(r.Context()) != nil {
			next.ServeHTTP(w, r)
			return
		}
		sys := s.Resolver.System(r.Context())
		if sys.GetTls().GetMode() != v1.TLSMode_TLS_MODE_HTTPS_ONLY {
			next.ServeHTTP(w, r)
			return
		}
		if s.CertEngine != nil && !s.CertEngine.AppCertReady(r.Context()) {
			next.ServeHTTP(w, r)
			return
		}
		host := sys.GetServer().GetPublicHostname()
		if host == "" {
			host = r.Host
		}
		http.Redirect(w, r, "https://"+host+r.URL.RequestURI(), http.StatusTemporaryRedirect)
	})
}

func (s *Server) Handler() http.Handler {
	return s.handler
}

// Distinct service names behind the registered procedures
func serviceNames(server *connect.Server) []string {
	seen := map[string]bool{}
	var names []string
	for spec := range server.Specs() {
		name := strings.TrimPrefix(spec.Procedure, "/")
		if i := strings.Index(name, "/"); i >= 0 {
			name = name[:i]
		}
		if !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	return names
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
			defer func() { _ = file.Close() }()
			stat, _ := file.Stat()
			http.ServeContent(w, r, path, stat.ModTime(), file)
			return
		}

		indexFile, err := fs.Open("/index.html")
		if err != nil {
			http.NotFound(w, r)
			return
		}
		defer func() { _ = indexFile.Close() }()

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
