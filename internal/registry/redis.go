package registry

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

// Keys the engine's redis descriptor cache writes
var redisCachePatterns = []string{"blobs::*", "repository::*"}

// Bounds a stalled redis, misses fall back to disk
const (
	redisDialTimeout  = time.Second
	redisReadTimeout  = time.Second
	redisWriteTimeout = time.Second
	redisMaxRetries   = 1
)

// Cache pool, go-redis picks simple, sentinel or cluster
func newRedisClient(cfg config.RedisConfig) (redis.UniversalClient, error) {
	tlsConf, err := redisTLS(cfg)
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
		// Same handshake the upstream pool runs
		OnConnect: func(ctx context.Context, cn *redis.Conn) error {
			return cn.Ping(ctx).Err()
		},
	}
	if opts.MaxRetries == 0 {
		opts.MaxRetries = redisMaxRetries
	}
	if opts.DialTimeout == 0 {
		opts.DialTimeout = redisDialTimeout
	}
	if opts.ReadTimeout == 0 {
		opts.ReadTimeout = redisReadTimeout
	}
	if opts.WriteTimeout == 0 {
		opts.WriteTimeout = redisWriteTimeout
	}
	return redis.NewUniversalClient(opts), nil
}

// Client tls, host roots unless a bundle is named
func redisTLS(cfg config.RedisConfig) (*tls.Config, error) {
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

// Drops descriptor keys, each cluster master scans itself
func flushRedisCache(ctx context.Context, client redis.UniversalClient) error {
	if cluster, ok := client.(*redis.ClusterClient); ok {
		return cluster.ForEachMaster(ctx, func(ctx context.Context, node *redis.Client) error {
			return unlinkMatching(ctx, node)
		})
	}
	return unlinkMatching(ctx, client)
}

// Pipelined single key unlinks, clusters refuse multi key ones
func unlinkMatching(ctx context.Context, client redis.Cmdable) error {
	const batch = 500
	for _, pattern := range redisCachePatterns {
		iter := client.Scan(ctx, 0, pattern, batch).Iterator()
		pipe := client.Pipeline()
		queued := 0
		flush := func() error {
			if queued == 0 {
				return nil
			}
			queued = 0
			_, err := pipe.Exec(ctx)
			return err
		}
		for iter.Next(ctx) {
			pipe.Unlink(ctx, iter.Val())
			queued++
			if queued >= batch {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		if err := iter.Err(); err != nil {
			return fmt.Errorf("scanning %s: %w", pattern, err)
		}
		if err := flush(); err != nil {
			return err
		}
	}
	return nil
}
