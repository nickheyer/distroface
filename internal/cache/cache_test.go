package cache

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"
)

func newClient(t *testing.T, srv *miniredis.Miniredis) *Client {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	c, err := New(ctx, config.RedisConfig{Addrs: []string{srv.Addr()}}, logger.New())
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.Close()
		cancel()
	})
	return c
}

func TestNewStaysOffWithoutAddrs(t *testing.T) {
	c, err := New(context.Background(), config.RedisConfig{}, logger.New())
	if err != nil || c != nil {
		t.Fatalf("New() = %v, %v", c, err)
	}
	if c.Enabled() || c.Summary() != "memory" || c.ID() != "" {
		t.Errorf("nil client enabled=%v summary=%q id=%q", c.Enabled(), c.Summary(), c.ID())
	}
}

func TestNewRejectsMissingCA(t *testing.T) {
	_, err := New(context.Background(), config.RedisConfig{
		Addrs: []string{"127.0.0.1:1"}, TLS: true, TLSCAFile: filepath.Join(t.TempDir(), "none.pem"),
	}, logger.New())
	if err == nil {
		t.Fatal("missing ca accepted")
	}
}

func TestNewUniversalBoundsAStalledServer(t *testing.T) {
	client, err := newUniversal(config.RedisConfig{Addrs: []string{"127.0.0.1:1"}})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	opts := client.(*redis.Client).Options()
	if opts.DialTimeout != defaultDialTimeout || opts.ReadTimeout != defaultReadTimeout || opts.WriteTimeout != defaultWriteTimeout {
		t.Errorf("timeouts = %s/%s/%s", opts.DialTimeout, opts.ReadTimeout, opts.WriteTimeout)
	}
	if opts.MaxRetries != defaultMaxRetries || opts.ClientName != "distroface" || opts.OnConnect == nil {
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

func TestNewUniversalPassesTunablesThrough(t *testing.T) {
	client, err := newUniversal(config.RedisConfig{
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

func TestNewUniversalPicksSentinelAndCluster(t *testing.T) {
	sentinel, err := newUniversal(config.RedisConfig{Addrs: []string{"a:26379", "b:26379"}, MasterName: "primary"})
	if err != nil {
		t.Fatal(err)
	}
	defer sentinel.Close()
	if _, ok := sentinel.(*redis.Client); !ok {
		t.Errorf("sentinel client = %T", sentinel)
	}
	cluster, err := newUniversal(config.RedisConfig{Addrs: []string{"a:6379", "b:6379"}})
	if err != nil {
		t.Fatal(err)
	}
	defer cluster.Close()
	if _, ok := cluster.(*redis.ClusterClient); !ok {
		t.Errorf("cluster client = %T", cluster)
	}
}

func TestTLSConfig(t *testing.T) {
	if conf, err := tlsConfig(config.RedisConfig{Addrs: []string{"cache:6379"}}); err != nil || conf != nil {
		t.Errorf("tls off = %v, %v", conf, err)
	}
	// Host roots serve when no bundle is named
	conf, err := tlsConfig(config.RedisConfig{Addrs: []string{"cache:6379"}, TLS: true})
	if err != nil || conf == nil || conf.RootCAs != nil || len(conf.Certificates) != 0 {
		t.Errorf("tls with host roots = %+v, %v", conf, err)
	}
	if _, err := tlsConfig(config.RedisConfig{Addrs: []string{"cache:6379"}, TLS: true, TLSCAFile: t.TempDir() + "/missing.pem"}); err == nil {
		t.Error("missing ca file accepted")
	}
	if _, err := tlsConfig(config.RedisConfig{Addrs: []string{"cache:6379"}, TLS: true, TLSCertFile: "/nope.pem", TLSKeyFile: "/nope.key"}); err == nil {
		t.Error("missing client pair accepted")
	}
}

func TestNilClientIsANoop(t *testing.T) {
	var c *Client
	ctx := context.Background()
	var out struct{ A int }
	if hit, err := c.Get(ctx, "k", &out); hit || err != nil {
		t.Errorf("Get = %v, %v", hit, err)
	}
	if err := c.Set(ctx, "k", out, time.Minute); err != nil {
		t.Error(err)
	}
	if err := c.DeletePrefix(ctx, "k"); err != nil {
		t.Error(err)
	}
	lock, ok, err := c.TryLock(ctx, "x", time.Second)
	if !ok || err != nil || lock != nil {
		t.Errorf("TryLock = %v, %v, %v", lock, ok, err)
	}
	if err := lock.Unlock(ctx); err != nil {
		t.Error(err)
	}
	lock.KeepAlive(ctx)()
	allowed, count, _, err := c.Window(ctx, "w", 1, time.Minute, true)
	if !allowed || count != 0 || err != nil {
		t.Errorf("Window = %v, %d, %v", allowed, count, err)
	}
	if ids, err := c.Instances(ctx); ids != nil || err != nil {
		t.Errorf("Instances = %v, %v", ids, err)
	}
	if h := c.Health(ctx); len(h) != 0 {
		t.Errorf("Health = %v", h)
	}
	c.Subscribe(ctx, "x", func(string) {})
	if err := c.Publish(ctx, "x", ""); err != nil {
		t.Error(err)
	}
}

func TestKVRoundTripAndExpiry(t *testing.T) {
	srv := miniredis.RunT(t)
	c := newClient(t, srv)
	ctx := context.Background()
	type payload struct {
		Name string
		N    int
	}
	if err := c.Set(ctx, "df:t:k", payload{"a", 1}, time.Minute); err != nil {
		t.Fatal(err)
	}
	var got payload
	if hit, err := c.Get(ctx, "df:t:k", &got); err != nil || !hit || got != (payload{"a", 1}) {
		t.Fatalf("Get = %v, %v, %+v", hit, err, got)
	}
	srv.FastForward(2 * time.Minute)
	if hit, _ := c.Get(ctx, "df:t:k", &got); hit {
		t.Error("expired entry still served")
	}
	// Garbage is dropped, not served
	srv.Set("df:t:bad", "{not json")
	if hit, err := c.Get(ctx, "df:t:bad", &got); hit || err != nil {
		t.Errorf("corrupt Get = %v, %v", hit, err)
	}
	if srv.Exists("df:t:bad") {
		t.Error("corrupt entry kept")
	}
	if won, _ := c.SetNX(ctx, "df:t:nx", "1", time.Minute); !won {
		t.Error("first SetNX lost")
	}
	if won, _ := c.SetNX(ctx, "df:t:nx", "2", time.Minute); won {
		t.Error("second SetNX won")
	}
}

func TestGetManyMarksHits(t *testing.T) {
	srv := miniredis.RunT(t)
	c := newClient(t, srv)
	ctx := context.Background()
	srv.Set("a", "1")
	srv.Set("c", "3")
	vals, present, err := c.GetMany(ctx, "a", "b", "c")
	if err != nil {
		t.Fatal(err)
	}
	if vals[0] != "1" || present[1] || vals[2] != "3" || !present[0] || !present[2] {
		t.Errorf("GetMany = %v %v", vals, present)
	}
}

func TestDeleteMatchingKeepsForeignKeys(t *testing.T) {
	srv := miniredis.RunT(t)
	c := newClient(t, srv)
	ctx := context.Background()
	for _, key := range []string{
		"blobs::sha256:aaaa", "repository::ns/app::blobs", "repository::ns/app::blobs::sha256:aaaa", "session:abc",
	} {
		if err := srv.Set(key, "x"); err != nil {
			t.Fatal(err)
		}
	}
	if err := c.DeleteMatching(ctx, "blobs::*", "repository::*"); err != nil {
		t.Fatal(err)
	}
	if !srv.Exists("session:abc") {
		t.Error("flush removed a key outside the patterns")
	}
	for _, key := range srv.Keys() {
		if strings.HasPrefix(key, "blobs::") || strings.HasPrefix(key, "repository::") {
			t.Errorf("key %q survived the flush", key)
		}
	}
}

func TestDeletePrefixEscapesGlobs(t *testing.T) {
	srv := miniredis.RunT(t)
	c := newClient(t, srv)
	ctx := context.Background()
	srv.Set("df:m:a[1]:x", "1")
	srv.Set("df:m:a1:x", "2")
	if err := c.DeletePrefix(ctx, "df:m:a[1]:"); err != nil {
		t.Fatal(err)
	}
	if srv.Exists("df:m:a[1]:x") || !srv.Exists("df:m:a1:x") {
		t.Errorf("keys after prefix delete: %v", srv.Keys())
	}
}

func TestLockIsExclusiveAndRefreshable(t *testing.T) {
	srv := miniredis.RunT(t)
	a := newClient(t, srv)
	b := newClient(t, srv)
	ctx := context.Background()
	lock, ok, err := a.TryLock(ctx, "gc", time.Minute)
	if err != nil || !ok || lock == nil {
		t.Fatalf("first TryLock = %v, %v, %v", lock, ok, err)
	}
	if _, ok, _ := b.TryLock(ctx, "gc", time.Minute); ok {
		t.Fatal("second holder got the lock")
	}
	holder, err := b.LockHolder(ctx, "gc")
	if err != nil || holder != a.ID() {
		t.Errorf("LockHolder = %q, %v want %q", holder, err, a.ID())
	}
	if err := lock.Refresh(ctx); err != nil {
		t.Errorf("Refresh: %v", err)
	}
	if ttl := srv.TTL(lockPrefix + "gc"); ttl < 59*time.Second {
		t.Errorf("ttl after refresh = %s", ttl)
	}
	if err := lock.Unlock(ctx); err != nil {
		t.Fatal(err)
	}
	if holder, _ := b.LockHolder(ctx, "gc"); holder != "" {
		t.Errorf("holder after unlock = %q", holder)
	}
	// A stranger's unlock leaves the lease alone
	second, ok, _ := b.TryLock(ctx, "gc", time.Minute)
	if !ok {
		t.Fatal("relock failed")
	}
	if err := lock.Unlock(ctx); err != nil {
		t.Fatal(err)
	}
	if !srv.Exists(lockPrefix + "gc") {
		t.Error("stale token released another holder's lock")
	}
	if err := lock.Refresh(ctx); err == nil {
		t.Error("stale token refreshed another holder's lock")
	}
	_ = second.Unlock(ctx)
}

func TestLockKeepAliveExtendsUntilStopped(t *testing.T) {
	srv := miniredis.RunT(t)
	c := newClient(t, srv)
	ctx := context.Background()
	lock, ok, err := c.TryLock(ctx, "ka", 300*time.Millisecond)
	if err != nil || !ok {
		t.Fatal(err)
	}
	stop := lock.KeepAlive(ctx)
	deadline := time.Now().Add(700 * time.Millisecond)
	for time.Now().Before(deadline) {
		srv.FastForward(50 * time.Millisecond)
		time.Sleep(50 * time.Millisecond)
	}
	if !srv.Exists(lockPrefix + "ka") {
		t.Fatal("lease expired while kept alive")
	}
	stop()
	srv.FastForward(time.Second)
	if srv.Exists(lockPrefix + "ka") {
		t.Error("lease survived past its ttl after stop")
	}
}

func TestBusSkipsOwnMessages(t *testing.T) {
	srv := miniredis.RunT(t)
	a := newClient(t, srv)
	b := newClient(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var mu sync.Mutex
	got := map[string][]string{}
	record := func(name string) func(string) {
		return func(p string) {
			mu.Lock()
			got[name] = append(got[name], p)
			mu.Unlock()
		}
	}
	a.Subscribe(ctx, "settings", record("a"))
	b.Subscribe(ctx, "settings", record("b"))
	// Subscriptions settle asynchronously
	time.Sleep(100 * time.Millisecond)

	if err := a.Publish(ctx, "settings", "changed"); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(got["b"])
		mu.Unlock()
		if n == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(got["b"]) != 1 || got["b"][0] != "changed" {
		t.Errorf("peer received %v", got["b"])
	}
	if len(got["a"]) != 0 {
		t.Errorf("sender received its own message %v", got["a"])
	}
}

func TestWindowCountsAndRefuses(t *testing.T) {
	srv := miniredis.RunT(t)
	c := newClient(t, srv)
	ctx := context.Background()
	for i := range 3 {
		allowed, count, _, err := c.Window(ctx, "df:w:ip", 3, time.Minute, true)
		if err != nil || !allowed || count != i+1 {
			t.Fatalf("take %d = %v, %d, %v", i, allowed, count, err)
		}
	}
	allowed, count, oldest, err := c.Window(ctx, "df:w:ip", 3, time.Minute, true)
	if err != nil || allowed || count != 3 {
		t.Fatalf("fourth take = %v, %d, %v", allowed, count, err)
	}
	if time.Since(oldest) > 5*time.Second {
		t.Errorf("oldest = %s", oldest)
	}
	// Peeking never adds
	if _, count, _, _ := c.Window(ctx, "df:w:ip", 3, time.Minute, false); count != 3 {
		t.Errorf("peek count = %d", count)
	}
	// A zero limit records without refusing
	if allowed, count, _, _ := c.Window(ctx, "df:w:ip", 0, time.Minute, true); !allowed || count != 4 {
		t.Errorf("record = %v, %d", allowed, count)
	}
	srv.FastForward(2 * time.Minute)
	if _, count, _, _ := c.Window(ctx, "df:w:ip", 3, time.Minute, false); count != 0 {
		t.Errorf("count after window = %d", count)
	}
}

func TestInstancesTrackHeartbeats(t *testing.T) {
	srv := miniredis.RunT(t)
	a := newClient(t, srv)
	b := newClient(t, srv)
	ctx := context.Background()
	ids, err := a.Instances(ctx)
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, id := range ids {
		seen[id] = true
	}
	if !seen[a.ID()] || !seen[b.ID()] {
		t.Fatalf("instances = %v", ids)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	ids, _ = a.Instances(ctx)
	for _, id := range ids {
		if id == b.ID() {
			t.Error("closed instance still listed")
		}
	}
}

func TestHealthReportsProbeFailures(t *testing.T) {
	srv := miniredis.RunT(t)
	c := newClient(t, srv)
	ctx := context.Background()
	if h := c.Health(ctx); len(h) != 0 {
		t.Fatalf("healthy client reports %v", h)
	}
	srv.Close()
	_ = c.Ping(ctx)
	if h := c.Health(ctx); h["redis"] == "" {
		t.Errorf("dead server not reported: %v", h)
	}
}

func TestClaimDueOncePerInterval(t *testing.T) {
	srv := miniredis.RunT(t)
	a := newClient(t, srv)
	b := newClient(t, srv)
	ctx := context.Background()
	if due, err := a.ClaimDue(ctx, "job", time.Hour); err != nil || !due {
		t.Fatalf("first claim = %v, %v", due, err)
	}
	if due, _ := b.ClaimDue(ctx, "job", time.Hour); due {
		t.Error("peer claimed the same slot")
	}
	srv.FastForward(2 * time.Minute)
	if due, _ := b.ClaimDue(ctx, "job", time.Hour); due {
		t.Error("stamp ignored once the claim lapsed")
	}
	if due, _ := b.ClaimDue(ctx, "other", time.Hour); !due {
		t.Error("unrelated job blocked")
	}
	var nilClient *Client
	if _, err := nilClient.ClaimDue(ctx, "job", time.Hour); err == nil {
		t.Error("nil client claims to know the schedule")
	}
}

func TestTakeRemovesOnRead(t *testing.T) {
	srv := miniredis.RunT(t)
	c := newClient(t, srv)
	ctx := context.Background()
	srv.Set("df:n:1", "x")
	if val, ok, err := c.Take(ctx, "df:n:1"); err != nil || !ok || val != "x" {
		t.Fatalf("Take = %q, %v, %v", val, ok, err)
	}
	if _, ok, _ := c.Take(ctx, "df:n:1"); ok {
		t.Error("second take found the key")
	}
}
