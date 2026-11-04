// Throwaway redis backed cache clients for tests
package cachetest

import (
	"context"
	"testing"

	"github.com/alicebob/miniredis/v2"

	"github.com/nickheyer/distroface/internal/cache"
	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
)

// Fresh in process redis and a client bound to it
func New(tb testing.TB) (*cache.Client, *miniredis.Miniredis) {
	tb.Helper()
	srv := miniredis.RunT(tb)
	return Attach(tb, srv), srv
}

// Second client on an existing server, one process per client
func Attach(tb testing.TB, srv *miniredis.Miniredis) *cache.Client {
	tb.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c, err := cache.New(ctx, config.RedisConfig{Addrs: []string{srv.Addr()}}, logger.New())
	if err != nil {
		cancel()
		tb.Fatalf("cache client: %v", err)
	}
	tb.Cleanup(func() {
		_ = c.Close()
		cancel()
	})
	return c
}
