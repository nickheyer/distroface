package container

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/pprof"
	"runtime"
	"time"

	"github.com/nickheyer/distroface/pkg/logger"
)

// Env var naming the address the profiler listens on, empty keeps it off
const pprofAddrEnv = "DISTROFACE_PPROF_ADDR"

// One in five mutex contention events
const pprofMutexFraction = 5

// Block sampling at 100us keeps profiling cheap under load
const pprofBlockRate = 100_000

// Private listener serving runtime profiles
type profiler struct {
	srv *http.Server
	ln  net.Listener
}

// Binds the profiler, nil when no address is set
func startProfiler(addr string, log *logger.Logger) (*profiler, error) {
	if addr == "" {
		return nil, nil
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, err
	}
	runtime.SetMutexProfileFraction(pprofMutexFraction)
	runtime.SetBlockProfileRate(pprofBlockRate)

	mux := http.NewServeMux()
	mux.HandleFunc("/debug/pprof/", pprof.Index)
	mux.HandleFunc("/debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("/debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("/debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("/debug/pprof/trace", pprof.Trace)
	p := &profiler{
		srv: &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second},
		ln:  ln,
	}
	go func() {
		if err := p.srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("pprof listener stopped: %v", err)
		}
	}()
	if host, _, err := net.SplitHostPort(ln.Addr().String()); err == nil && !isLoopback(host) {
		log.Warn("SECURITY: pprof listens on %s, profiles expose internals, keep it on loopback or firewalled", ln.Addr())
	}
	log.Info("pprof listening on http://%s/debug/pprof/", ln.Addr())
	return p, nil
}

func isLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Address the profiler bound to
func (p *profiler) Addr() string {
	return p.ln.Addr().String()
}

func (p *profiler) Close() {
	if p == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = p.srv.Shutdown(ctx)
}
