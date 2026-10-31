package registry

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/nickheyer/distroface/pkg/config"
)

func TestFlushRedisCacheKeepsForeignKeys(t *testing.T) {
	srv := miniredis.RunT(t)
	client, err := newRedisClient(config.RedisConfig{Addrs: []string{srv.Addr()}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{
		"blobs::sha256:aaaa", "repository::ns/app::blobs", "repository::ns/app::blobs::sha256:aaaa", "session:abc",
	} {
		if err := srv.Set(key, "x"); err != nil {
			t.Fatal(err)
		}
	}

	if err := flushRedisCache(ctx, client); err != nil {
		t.Fatal(err)
	}
	if !srv.Exists("session:abc") {
		t.Error("flush removed a key outside the registry cache")
	}
	for _, key := range srv.Keys() {
		if strings.HasPrefix(key, "blobs::") || strings.HasPrefix(key, "repository::") {
			t.Errorf("key %q survived the flush", key)
		}
	}
}

func TestNewRedisClientBoundsAStalledServer(t *testing.T) {
	client, err := newRedisClient(config.RedisConfig{Addrs: []string{"127.0.0.1:1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	opts := client.(*redis.Client).Options()
	if opts.DialTimeout != redisDialTimeout || opts.ReadTimeout != redisReadTimeout || opts.WriteTimeout != redisWriteTimeout {
		t.Errorf("timeouts = %s/%s/%s", opts.DialTimeout, opts.ReadTimeout, opts.WriteTimeout)
	}
	if opts.MaxRetries != redisMaxRetries || opts.ClientName != "distroface" || opts.OnConnect == nil {
		t.Errorf("retries=%d name=%q onconnect=%v", opts.MaxRetries, opts.ClientName, opts.OnConnect != nil)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	start := time.Now()
	if err := client.Ping(ctx).Err(); err == nil {
		t.Fatal("ping succeeded against a closed port")
	}
	if took := time.Since(start); took > 5*time.Second {
		t.Errorf("ping against a dead server took %s", took)
	}
}

func TestNewRedisClientPassesTunablesThrough(t *testing.T) {
	client, err := newRedisClient(config.RedisConfig{
		Addrs: []string{"127.0.0.1:1"}, DB: 3, Username: "app", Password: "pw", Protocol: 2,
		// A negative retry count lands as zero
		MaxRetries: -1, DialTimeout: 2 * time.Second, ReadTimeout: 250 * time.Millisecond, WriteTimeout: 300 * time.Millisecond,
		PoolSize: 7, PoolTimeout: 4 * time.Second, MinIdleConns: 1, MaxIdleConns: 5, ConnMaxIdleTime: time.Minute, ConnMaxLifetime: time.Hour,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	o := client.(*redis.Client).Options()
	if o.DB != 3 || o.Username != "app" || o.Password != "pw" || o.Protocol != 2 || o.MaxRetries != 0 ||
		o.DialTimeout != 2*time.Second || o.ReadTimeout != 250*time.Millisecond || o.WriteTimeout != 300*time.Millisecond ||
		o.PoolSize != 7 || o.PoolTimeout != 4*time.Second || o.MinIdleConns != 1 || o.MaxIdleConns != 5 ||
		o.ConnMaxIdleTime != time.Minute || o.ConnMaxLifetime != time.Hour {
		t.Errorf("options = %+v", o)
	}
}

func TestNewRedisClientPicksSentinelAndCluster(t *testing.T) {
	sentinel, err := newRedisClient(config.RedisConfig{Addrs: []string{"a:26379", "b:26379"}, MasterName: "primary"})
	if err != nil {
		t.Fatal(err)
	}
	defer sentinel.Close()
	if _, ok := sentinel.(*redis.Client); !ok {
		t.Errorf("sentinel client = %T", sentinel)
	}
	cluster, err := newRedisClient(config.RedisConfig{Addrs: []string{"a:6379", "b:6379"}})
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.Close()
	if _, ok := cluster.(*redis.ClusterClient); !ok {
		t.Errorf("cluster client = %T", cluster)
	}
}

func TestRedisTLS(t *testing.T) {
	if conf, err := redisTLS(config.RedisConfig{Addrs: []string{"cache:6379"}}); err != nil || conf != nil {
		t.Errorf("tls off = %v, %v", conf, err)
	}
	// Host roots serve when no bundle is named
	conf, err := redisTLS(config.RedisConfig{Addrs: []string{"cache:6379"}, TLS: true})
	if err != nil || conf == nil || conf.RootCAs != nil || len(conf.Certificates) != 0 {
		t.Errorf("tls with host roots = %+v, %v", conf, err)
	}
	if _, err := redisTLS(config.RedisConfig{Addrs: []string{"cache:6379"}, TLS: true, TLSCAFile: t.TempDir() + "/missing.pem"}); err == nil {
		t.Error("missing ca file accepted")
	}
	if _, err := redisTLS(config.RedisConfig{Addrs: []string{"cache:6379"}, TLS: true, TLSCertFile: "/nope.pem", TLSKeyFile: "/nope.key"}); err == nil {
		t.Error("missing client pair accepted")
	}
}
