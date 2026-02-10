package rpc

import (
	"context"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"connectrpc.com/grpcreflect"
	"github.com/nickheyer/distroface/internal/config"
	storage "github.com/nickheyer/distroface/internal/db"
	"github.com/nickheyer/distroface/internal/rpc/services"
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
}

func NewServer(store *storage.Store, cfg *config.Config, log *logger.Logger) *Server {
	s := &Server{
		store:  store,
		config: cfg,
		log:    log,
	}
	s.setupHandler()
	return s
}

func (s *Server) setupHandler() {
	mux := http.NewServeMux()

	interceptors := []connect.Interceptor{
		&loggingInterceptor{log: s.log},
	}

	opts := []connect.HandlerOption{
		connect.WithInterceptors(interceptors...),
	}

	// Register services
	healthService := services.NewHealthService(s.log)
	healthPath, healthHandler := distrofacev1connect.NewHealthServiceHandler(healthService, opts...)
	mux.Handle(healthPath, healthHandler)

	// gRPC reflection
	reflector := grpcreflect.NewStaticReflector(
		distrofacev1connect.HealthServiceName,
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

type loggingInterceptor struct {
	log *logger.Logger
}

func (i *loggingInterceptor) WrapUnary(next connect.UnaryFunc) connect.UnaryFunc {
	return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
		i.log.Info("RPC %s %s", req.Peer().Addr, req.Spec().Procedure)
		return next(ctx, req)
	}
}

func (i *loggingInterceptor) WrapStreamingClient(next connect.StreamingClientFunc) connect.StreamingClientFunc {
	return next
}

func (i *loggingInterceptor) WrapStreamingHandler(next connect.StreamingHandlerFunc) connect.StreamingHandlerFunc {
	return func(ctx context.Context, conn connect.StreamingHandlerConn) error {
		i.log.Info("RPC Stream open %s %s", conn.Peer().Addr, conn.Spec().Procedure)
		err := next(ctx, conn)
		i.log.Info("RPC Stream closed %s %s", conn.Peer().Addr, conn.Spec().Procedure)
		return err
	}
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
