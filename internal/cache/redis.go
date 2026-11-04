package cache

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/nickheyer/distroface/pkg/config"
)

// Bounds a stalled redis, every miss falls back to local state
const (
	defaultDialTimeout  = time.Second
	defaultReadTimeout  = time.Second
	defaultWriteTimeout = time.Second
	defaultMaxRetries   = 1
)

// Pool from config, go-redis picks simple, sentinel or cluster
func newUniversal(cfg config.RedisConfig) (redis.UniversalClient, error) {
	tlsConf, err := tlsConfig(cfg)
	if err != nil {
		return nil, err
	}
	opts := &redis.UniversalOptions{
		Addrs:            cfg.Addrs,
		ClientName:       "distroface",
		DB:               cfg.DB,
		Protocol:         cfg.Protocol,
		Username:         cfg.Username,
		Password:         cfg.Password,
		SentinelUsername: cfg.SentinelUsername,
		SentinelPassword: cfg.SentinelPassword,
		MasterName:       cfg.MasterName,
		MaxRetries:       cfg.MaxRetries,
		DialTimeout:      cfg.DialTimeout,
		ReadTimeout:      cfg.ReadTimeout,
		WriteTimeout:     cfg.WriteTimeout,
		PoolSize:         cfg.PoolSize,
		PoolTimeout:      cfg.PoolTimeout,
		MinIdleConns:     cfg.MinIdleConns,
		MaxIdleConns:     cfg.MaxIdleConns,
		MaxActiveConns:   cfg.MaxActiveConns,
		ConnMaxIdleTime:  cfg.ConnMaxIdleTime,
		ConnMaxLifetime:  cfg.ConnMaxLifetime,
		RouteByLatency:   cfg.RouteByLatency,
		RouteRandomly:    cfg.RouteRandomly,
		TLSConfig:        tlsConf,
		// Same handshake the distribution pool runs
		OnConnect: func(ctx context.Context, cn *redis.Conn) error {
			return cn.Ping(ctx).Err()
		},
	}
	if opts.MaxRetries == 0 {
		opts.MaxRetries = defaultMaxRetries
	}
	if opts.DialTimeout == 0 {
		opts.DialTimeout = defaultDialTimeout
	}
	if opts.ReadTimeout == 0 {
		opts.ReadTimeout = defaultReadTimeout
	}
	if opts.WriteTimeout == 0 {
		opts.WriteTimeout = defaultWriteTimeout
	}
	return redis.NewUniversalClient(opts), nil
}

// Client tls, host roots unless a bundle is named
func tlsConfig(cfg config.RedisConfig) (*tls.Config, error) {
	if !cfg.TLS {
		return nil, nil
	}
	tlsConf := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.TLSCertFile != "" {
		pair, err := tls.LoadX509KeyPair(cfg.TLSCertFile, cfg.TLSKeyFile)
		if err != nil {
			return nil, fmt.Errorf("loading redis client certificate: %w", err)
		}
		tlsConf.Certificates = []tls.Certificate{pair}
	}
	if cfg.TLSCAFile != "" {
		pem, err := os.ReadFile(cfg.TLSCAFile)
		if err != nil {
			return nil, fmt.Errorf("reading redis ca file: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates found in %s", cfg.TLSCAFile)
		}
		tlsConf.RootCAs = pool
	}
	return tlsConf, nil
}
