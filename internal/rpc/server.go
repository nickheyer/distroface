package rpc

import (
	"context"
	"net/http"
	"strings"

	"connectrpc.com/connect/v2"
	"connectrpc.com/connect/v2/connecthttp"
	"connectrpc.com/grpcreflect/v2"
	"github.com/nickheyer/distroface/internal/actions"
	"github.com/nickheyer/distroface/internal/activity"
	"github.com/nickheyer/distroface/internal/admin"
	"github.com/nickheyer/distroface/internal/agents"
	"github.com/nickheyer/distroface/internal/artifacts"
	"github.com/nickheyer/distroface/internal/audit"
	"github.com/nickheyer/distroface/internal/auth"
	"github.com/nickheyer/distroface/internal/certs"
	"github.com/nickheyer/distroface/internal/db/stores"
	"github.com/nickheyer/distroface/internal/forge"
	"github.com/nickheyer/distroface/internal/ghapi"
	"github.com/nickheyer/distroface/internal/gitrepo"
	"github.com/nickheyer/distroface/internal/mailer"
	"github.com/nickheyer/distroface/internal/mirror"
	"github.com/nickheyer/distroface/internal/portal"
	"github.com/nickheyer/distroface/internal/rbac"
	"github.com/nickheyer/distroface/internal/registry"
	"github.com/nickheyer/distroface/internal/rpc/services"
	"github.com/nickheyer/distroface/internal/runner"
	"github.com/nickheyer/distroface/internal/settings"
	"github.com/nickheyer/distroface/internal/webhook"
	"github.com/nickheyer/distroface/pkg/logger"
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"github.com/nickheyer/distroface/pkg/proto/distroface/v1/distrofacev1connect"
	"github.com/nickheyer/distroface/pkg/utils"
	web "github.com/nickheyer/distroface/web/distroface"
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
	GitHTTP             *gitrepo.HTTP       // Nil disables git over http
	GitManager          *gitrepo.Manager    // Nil hides the git services
	GitSSH              *gitrepo.SSHManager // Nil disables the ssh port probe
	ForgeManager        *forge.Manager
	ActionsManager      *actions.Manager
	Runners             *runner.Registry
	AgentsManager       *agents.Manager
	MailService         *mailer.Service
	Activity            *activity.Recorder // Nil keeps the feed quiet
}

// Largest request message any procedure accepts, uploads stream over plain http
const rpcReadMaxBytes = 64 << 20

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

	var gh http.Handler
	if s.GitManager != nil && s.ForgeManager != nil && s.ArtifactManager != nil {
		// gh flavored octokit tooling api
		gh = ghapi.New(s.Store, s.AuthManager, s.GitManager, s.ForgeManager, s.ArtifactManager, s.ActionsManager, s.Enforcer, s.Log)
		mux.Handle(ghapi.Prefix+"/", http.StripPrefix(ghapi.Prefix, gh))
	}

	// V1 artifact facade for old dfcli and ci, gated per request
	// Everything else under /api/v1 is the GitHub api act points jobs at
	var v1mux *http.ServeMux
	if s.ArtifactV1Facade != nil {
		v1mux = http.NewServeMux()
		s.ArtifactV1Facade.RegisterAuth(v1mux)
		s.ArtifactV1Facade.RegisterArtifacts(v1mux)
	}
	mux.Handle("/api/v1/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/v1/artifacts") || strings.HasPrefix(r.URL.Path, "/api/v1/auth/") {
			if v1mux == nil || !s.Resolver.System(r.Context()).GetArtifacts().GetV1Compat() {
				http.NotFound(w, r)
				return
			}
			v1mux.ServeHTTP(w, r)
			return
		}
		if gh == nil {
			http.NotFound(w, r)
			return
		}
		http.StripPrefix("/api/v1", gh).ServeHTTP(w, r)
	}))

	// Uploaded profile images, cached and public
	mux.Handle("GET /api/avatars/{id}", s.avatarHandler())

	// Register RPC services
	distrofacev1connect.RegisterHealthServiceHandler(rpcServer, services.NewHealthService(s.Log))
	distrofacev1connect.RegisterAuthServiceHandler(rpcServer, services.NewAuthService(s.Store, s.AuthManager, s.Enforcer, s.OIDCHandler, s.MailService, s.Log))
	distrofacev1connect.RegisterUserServiceHandler(rpcServer, services.NewUserService(s.Store, s.AuthManager, s.Enforcer, s.GitManager, s.Log))
	repoService := services.NewRepositoryService(s.Store, s.RegistryAccess, s.Enforcer, s.MirrorMonitor, s.GitManager, s.Log)
	repoService.SetActivity(s.Activity)
	distrofacev1connect.RegisterRepositoryServiceHandler(rpcServer, repoService)
	distrofacev1connect.RegisterSettingsServiceHandler(rpcServer, services.NewSettingsService(s.Store, s.Resolver, s.Enforcer, s.GitManager, s.GitSSH, s.Log))
	distrofacev1connect.RegisterRoleServiceHandler(rpcServer, services.NewRoleService(s.Store, s.Enforcer, s.Log))
	distrofacev1connect.RegisterTokenServiceHandler(rpcServer, services.NewTokenService(s.AuthManager, s.Enforcer, s.GitManager, s.Log))
	orgService := services.NewOrganizationService(s.Store, s.RegistryAccess, s.Enforcer, s.Resolver, s.GitManager, s.Log)
	orgService.SetActivity(s.Activity)
	distrofacev1connect.RegisterOrganizationServiceHandler(rpcServer, orgService)
	distrofacev1connect.RegisterWebhookServiceHandler(rpcServer, services.NewWebhookService(s.Store, s.Enforcer, s.WebhookDispatcher, s.GitManager, s.Log))
	distrofacev1connect.RegisterActivityServiceHandler(rpcServer, services.NewActivityService(s.Store, s.GitManager, s.Enforcer, s.Log))

	if s.PortalService != nil {
		distrofacev1connect.RegisterPortalServiceHandler(rpcServer, s.PortalService)
	}

	if s.ArtifactManager != nil {
		artifactService := services.NewArtifactService(s.Store, s.ArtifactManager, s.Enforcer, s.MirrorMonitor, s.GitManager, s.Log)
		artifactService.SetActivity(s.Activity)
		distrofacev1connect.RegisterArtifactServiceHandler(rpcServer, artifactService)
	}

	if s.MirrorMonitor != nil {
		artifactAccess := artifacts.NewAccess(s.Store, s.Enforcer)
		repoAccess := auth.NewRepoAccess(s.Store, s.Enforcer)
		if s.GitManager != nil {
			artifactAccess.SetLinkedAccess(s.GitManager.Access())
			repoAccess.SetLinkedAccess(s.GitManager.Access())
		}
		distrofacev1connect.RegisterMirrorServiceHandler(rpcServer, services.NewMirrorService(s.MirrorMonitor, s.Enforcer, artifactAccess, repoAccess, s.Log))
	}

	// Git hosting and the forge features ride on the git manager
	if s.GitManager != nil {
		gitService := services.NewGitService(s.Store, s.GitManager, s.ForgeManager, s.MirrorMonitor, s.Enforcer, s.Log)
		gitService.SetActivity(s.Activity)
		distrofacev1connect.RegisterGitServiceHandler(rpcServer, gitService)
		distrofacev1connect.RegisterGitRefServiceHandler(rpcServer, services.NewGitRefService(s.Store, s.GitManager, s.Log))
		distrofacev1connect.RegisterGitFileServiceHandler(rpcServer, services.NewGitFileService(s.Store, s.GitManager, s.Log))
		distrofacev1connect.RegisterWikiServiceHandler(rpcServer, services.NewWikiService(s.Store, s.GitManager, s.Log))
		distrofacev1connect.RegisterKeyServiceHandler(rpcServer, services.NewKeyService(s.Store, s.GitManager, s.Log))
		if s.ForgeManager != nil {
			distrofacev1connect.RegisterReleaseServiceHandler(rpcServer, services.NewReleaseService(s.Store, s.GitManager, s.ForgeManager, s.ArtifactManager, s.RegistryAccess, s.Log))
			distrofacev1connect.RegisterIssueServiceHandler(rpcServer, services.NewIssueService(s.Store, s.ForgeManager, s.Log))
			distrofacev1connect.RegisterPullRequestServiceHandler(rpcServer, services.NewPullRequestService(s.Store, s.ForgeManager, s.Log))
			distrofacev1connect.RegisterProjectServiceHandler(rpcServer, services.NewProjectService(s.Store, s.ForgeManager, s.Log))
			distrofacev1connect.RegisterNotificationServiceHandler(rpcServer, services.NewNotificationService(s.Store, s.ForgeManager, s.Log))
		}
		if s.MailService != nil {
			distrofacev1connect.RegisterMailServiceHandler(rpcServer, services.NewMailService(s.Store, s.MailService, s.GitManager, s.Enforcer, s.Log))
		}
		if s.ActionsManager != nil {
			distrofacev1connect.RegisterActionsServiceHandler(rpcServer, services.NewActionsService(s.Store, s.ActionsManager, s.Runners, s.GitManager, s.RegistryAccess, s.Log))
			distrofacev1connect.RegisterRunnerServiceHandler(rpcServer, services.NewRunnerService(s.Store, s.ActionsManager, s.Runners, s.Log))

			// Legacy artifactcache protocol for actions/cache and setup-* actions
			mux.Handle("/api/actions/cache/", http.StripPrefix("/api/actions/cache", s.ActionsManager.CacheHandler()))
			// Results api behind upload-artifact and download-artifact, plus signed links for everyone else
			mux.Handle(actions.ResultsAPIPrefix+"/", s.ActionsManager.ResultsHandler())
			mux.Handle(actions.ArtifactDownloadPrefix+"/", s.ActionsManager.ArtifactDownloadHandler())
			// Pipeline protocol behind upload-artifact v1 to v3 and the setup actions' tool caches
			mux.Handle(actions.PipelineAPIPrefix+"/", http.StripPrefix(actions.PipelineAPIPrefix, s.ActionsManager.PipelineHandler()))
		}
		if s.AgentsManager != nil {
			distrofacev1connect.RegisterAgentsServiceHandler(rpcServer, services.NewAgentsService(s.Store, s.AgentsManager, s.GitManager, s.Log))
		}
	}

	// Registered even without a collector, it also serves storage usage
	distrofacev1connect.RegisterGCServiceHandler(rpcServer, services.NewGCService(s.GCCollector, s.Store, s.RegistryStoragePath, s.Resolver, s.Log))

	if s.CertService != nil {
		distrofacev1connect.RegisterCertificateServiceHandler(rpcServer, s.CertService)
	}

	if s.AuditService != nil {
		distrofacev1connect.RegisterAuditServiceHandler(rpcServer, s.AuditService)
	}
	connecthttp.Mount(mux, rpcServer, connecthttp.WithReadMaxBytes(rpcReadMaxBytes))

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
	s.handler = root
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
		// Portals enforce their own scheme, in process calls have none
		if r.TLS != nil || portal.FromContext(r.Context()) != nil || runner.InProcess(r.Context()) {
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

// Git data plane paths are claimed ahead of the spa catch all
func (s *Server) setupFrontend(mux *http.ServeMux) {
	fs := s.getFrontendFS()
	var root http.Handler
	if fs == nil {
		s.Log.Warn("No frontend found - API only mode")
		root = http.NotFoundHandler()
	} else {
		root = s.createFrontendHandler(fs)
	}
	if s.GitHTTP != nil {
		root = s.GitHTTP.Wrap(root)
	}
	mux.Handle("/", root)
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
