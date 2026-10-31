package registry

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/distribution/distribution/v3/configuration"
	"github.com/distribution/distribution/v3/health"
	"github.com/distribution/distribution/v3/registry/api/errcode"
	"github.com/distribution/distribution/v3/registry/handlers"

	"github.com/nickheyer/distroface/pkg/logger"
)

// Longest wait for the first redis answer at boot
const cachePingTimeout = 5 * time.Second

// Embedded distribution app with its wrapping pieces
type Engine struct {
	Gate    *WriteGate
	app     *handlers.App
	cache   *DescriptorCache
	checks  *health.Registry
	handler http.Handler
}

// Builds the embedded registry from normalized config, one per process
func New(ctx context.Context, opts Options) (*Engine, error) {
	log := opts.Log
	if log == nil {
		log = logger.New()
	}
	if err := os.MkdirAll(opts.StoragePath, 0o755); err != nil {
		return nil, fmt.Errorf("creating registry storage directory: %w", err)
	}
	if opts.CertPath == "" {
		log.Warn("SECURITY: registry api runs without token auth")
	}

	if opts.Redis.Enabled() {
		client, err := newRedisClient(opts.Redis)
		if err != nil {
			return nil, err
		}
		engineCache.useRedis(client)
		pingCtx, cancel := context.WithTimeout(ctx, cachePingTimeout)
		err = client.Ping(pingCtx).Err()
		cancel()
		if err != nil {
			log.Warn("Redis %s unreachable, descriptor lookups hit disk until it answers: %v", opts.Redis.Redacted(), err)
		} else {
			log.Info("Redis %s reachable", opts.Redis.Redacted())
		}
	} else {
		engineCache.useMemory(opts.BlobCacheSize)
	}

	app, err := newApp(ctx, BuildConfig(opts))
	if err != nil {
		return nil, errors.Join(err, engineCache.release())
	}
	checks := health.NewRegistry()
	app.RegisterHealthChecks(checks)

	gate := NewWriteGate()
	eng := &Engine{Gate: gate, app: app, cache: engineCache, checks: checks}
	eng.handler = gate.Handler(healthGate(checks, app))
	log.Info("Distribution v3 initialized (blob cache %s)", engineCache.Kind())
	return eng, nil
}

// Configuration errors surface as panics inside the engine
func newApp(ctx context.Context, cfg *configuration.Configuration) (app *handlers.App, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("distribution rejected its configuration: %v", r)
		}
	}()
	return handlers.NewApp(ctx, cfg), nil
}

// Registry api behind the write and health gates
func (e *Engine) Handler() http.Handler {
	return e.handler
}

// Drops every cached descriptor once a sweep finishes
func (e *Engine) ResetCache(ctx context.Context) error {
	return e.cache.Reset(ctx)
}

// Failing storage checks by name, empty while healthy
func (e *Engine) Health(ctx context.Context) map[string]string {
	return e.checks.CheckStatus(ctx)
}

// Stops purger and health polls, closes redis
func (e *Engine) Close() error {
	return errors.Join(e.app.Shutdown(), e.cache.release())
}

// Every request bounces while a storage check is failing
func healthGate(checks *health.Registry, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if failing := checks.CheckStatus(r.Context()); len(failing) != 0 {
			_ = errcode.ServeJSON(w, errcode.ErrorCodeUnavailable.WithMessage("registry storage failed its health check"))
			return
		}
		next.ServeHTTP(w, r)
	})
}
