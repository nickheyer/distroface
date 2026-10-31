package config

import (
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestRedactedHidesEveryPasswordForm(t *testing.T) {
	cases := []string{
		"postgres://df:secret@db:5432/df?sslmode=disable",
		"postgres://df@db:5432/df?password=secret&sslmode=disable",
		"postgres://df@db:5432/df?PASSWORD=secret",
		"host=db user=df password=secret dbname=df",
		"host=db user=df passfile=secret dbname=df",
	}
	for _, dsn := range cases {
		got := (&DatabaseConfig{Driver: DatabasePostgres, DSN: dsn}).Redacted()
		if strings.Contains(got, "secret") {
			t.Errorf("Redacted(%q) = %q leaks the password", dsn, got)
		}
		if !strings.Contains(got, "db") {
			t.Errorf("Redacted(%q) = %q lost the host", dsn, got)
		}
	}
}

func TestRedactedSQLiteShowsPath(t *testing.T) {
	got := (&DatabaseConfig{Driver: DatabaseSQLite, Path: "/srv/df/app.db", DSN: "/srv/df/app.db"}).Redacted()
	if got != "/srv/df/app.db" {
		t.Errorf("Redacted() = %q", got)
	}
}

func TestRedisConfigResolve(t *testing.T) {
	cases := []struct {
		name string
		in   RedisConfig
		want RedisConfig
	}{
		{"bare url", RedisConfig{URL: "redis://cache"}, RedisConfig{Addrs: []string{"cache:6379"}}},
		{"credentials and db", RedisConfig{URL: "redis://app:s3cret@10.0.0.9:6380/2"},
			RedisConfig{Addrs: []string{"10.0.0.9:6380"}, Username: "app", Password: "s3cret", DB: 2}},
		{"rediss turns on tls", RedisConfig{URL: "rediss://cache:6379", TLSCAFile: "/ca.pem"},
			RedisConfig{Addrs: []string{"cache:6379"}, TLS: true, TLSCAFile: "/ca.pem"}},
		{"rediss alone keeps host roots", RedisConfig{URL: "rediss://cache"},
			RedisConfig{Addrs: []string{"cache:6379"}, TLS: true}},
		{"mtls pair alone keeps host roots", RedisConfig{Addrs: []string{"cache:6379"}, TLSCertFile: "/c.pem", TLSKeyFile: "/k.pem"},
			RedisConfig{Addrs: []string{"cache:6379"}, TLS: true, TLSCertFile: "/c.pem", TLSKeyFile: "/k.pem"}},
		{"explicit password wins", RedisConfig{URL: "redis://:fromurl@cache", Password: "explicit"},
			RedisConfig{Addrs: []string{"cache:6379"}, Password: "explicit"}},
		{"client query options", RedisConfig{URL: "redis://cache?dial_timeout=2s&read_timeout=250ms&max_retries=4&pool_size=7&protocol=2"},
			RedisConfig{Addrs: []string{"cache:6379"}, DialTimeout: 2 * time.Second, ReadTimeout: 250 * time.Millisecond, MaxRetries: 4, PoolSize: 7, Protocol: 2}},
		{"explicit tunable wins over query", RedisConfig{URL: "redis://cache?pool_size=7", PoolSize: 3},
			RedisConfig{Addrs: []string{"cache:6379"}, PoolSize: 3}},
		{"comma joined addrs", RedisConfig{Addrs: []string{"a:6379, b:6379", "c:6379"}},
			RedisConfig{Addrs: []string{"a:6379", "b:6379", "c:6379"}}},
		{"ca file implies tls", RedisConfig{Addrs: []string{"cache:6379"}, TLSCAFile: "/ca.pem"},
			RedisConfig{Addrs: []string{"cache:6379"}, TLSCAFile: "/ca.pem", TLS: true}},
		{"empty stays disabled", RedisConfig{}, RedisConfig{}},
	}
	for _, tc := range cases {
		got := tc.in
		if err := got.resolve(); err != nil {
			t.Errorf("%s: resolve() error %v", tc.name, err)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: resolve() = %+v, want %+v", tc.name, got, tc.want)
		}
		if got.Enabled() != (len(tc.want.Addrs) > 0) {
			t.Errorf("%s: Enabled() = %v", tc.name, got.Enabled())
		}
	}
}

// A second pass over folded config must not fail
func TestRedisConfigResolveTwice(t *testing.T) {
	cfg := RedisConfig{URL: "redis://:pw@cache:6379/1", PoolSize: 3}
	for i := range 2 {
		if err := cfg.resolve(); err != nil {
			t.Fatalf("resolve pass %d: %v", i+1, err)
		}
	}
	want := RedisConfig{Addrs: []string{"cache:6379"}, Password: "pw", DB: 1, PoolSize: 3}
	if !reflect.DeepEqual(cfg, want) {
		t.Errorf("resolve() twice = %+v, want %+v", cfg, want)
	}
}

func TestRedisConfigResolveRejects(t *testing.T) {
	cases := map[string]RedisConfig{
		"bad scheme":       {URL: "http://cache:6379"},
		"unix socket":      {URL: "unix:///var/run/redis.sock"},
		"unknown option":   {URL: "redis://cache?bogus=1"},
		"url plus addrs":   {URL: "redis://cache", Addrs: []string{"other:6379"}},
		"bad db":           {URL: "redis://cache/two"},
		"negative db":      {Addrs: []string{"cache:6379"}, DB: -1},
		"cert without key": {Addrs: []string{"cache:6379"}, TLSCertFile: "/c.pem"},
	}
	for name, in := range cases {
		if err := in.resolve(); err == nil {
			t.Errorf("%s: resolve() accepted %+v", name, in)
		}
	}
}

func TestRegistryConfigNormalize(t *testing.T) {
	cfg := RegistryConfig{
		ManifestURLsAllow: []string{`^https://mcr\.microsoft\.com/`},
		IndexPlatforms:    " LIST ",
		IndexPlatformList: []string{"linux/amd64"},
		UploadPurgeAge:    time.Hour,
	}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	if cfg.IndexPlatforms != "list" {
		t.Errorf("index_platforms = %q", cfg.IndexPlatforms)
	}
	if err := (&RegistryConfig{}).Normalize(); err != nil {
		t.Errorf("empty registry block rejected: %v", err)
	}

	rejects := map[string]RegistryConfig{
		"bad regex":             {ManifestURLsDeny: []string{"["}},
		"unknown platform mode": {IndexPlatforms: "sometimes"},
		"list without entries":  {IndexPlatforms: "list"},
		"entries without list":  {IndexPlatformList: []string{"linux/amd64"}},
		"bad entry":             {IndexPlatforms: "list", IndexPlatformList: []string{"linux"}},
		"negative purge":        {UploadPurgeInterval: -time.Minute},
		"negative concurrency":  {TagLookupConcurrency: -1},
		"negative cache":        {BlobCacheSize: -1},
	}
	for name, in := range rejects {
		if err := in.Normalize(); err == nil {
			t.Errorf("%s: Normalize() accepted %+v", name, in)
		}
	}
}

func TestRedisRedactedHidesPassword(t *testing.T) {
	cfg := RedisConfig{URL: "rediss://app:s3cret@cache:6380/3"}
	if err := cfg.resolve(); err != nil {
		t.Fatal(err)
	}
	got := cfg.Redacted()
	if strings.Contains(got, "s3cret") {
		t.Errorf("Redacted() = %q leaks the password", got)
	}
	for _, want := range []string{"rediss://", "app@", "cache:6380", "/3"} {
		if !strings.Contains(got, want) {
			t.Errorf("Redacted() = %q missing %q", got, want)
		}
	}
	if got := (&RedisConfig{}).Redacted(); got != "disabled" {
		t.Errorf("Redacted() on empty config = %q", got)
	}
}

func TestLoadBindsRegistryEnv(t *testing.T) {
	t.Setenv("DISTROFACE_DATA_DIR", t.TempDir())
	t.Setenv("DISTROFACE_REGISTRY_REDIS_URL", "redis://:pw@cache:6379/1")
	t.Setenv("DISTROFACE_REGISTRY_REDIS_READ_TIMEOUT", "500ms")
	t.Setenv("DISTROFACE_REGISTRY_BLOB_CACHE_SIZE", "250")
	t.Setenv("DISTROFACE_REGISTRY_UPLOAD_PURGE_AGE", "48h")
	t.Setenv("DISTROFACE_REGISTRY_MANIFEST_URLS_ALLOW", `^https://a/,^https://b/`)
	t.Setenv("DISTROFACE_REGISTRY_INDEX_PLATFORMS", "none")
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := cfg.Registry.Redis
	if !r.Enabled() || len(r.Addrs) != 1 || r.Addrs[0] != "cache:6379" || r.DB != 1 || r.Password != "pw" || r.ReadTimeout != 500*time.Millisecond {
		t.Errorf("redis from env = %+v", r)
	}
	reg := cfg.Registry
	if reg.BlobCacheSize != 250 || reg.UploadPurgeAge != 48*time.Hour || reg.IndexPlatforms != "none" ||
		!reflect.DeepEqual(reg.ManifestURLsAllow, []string{"^https://a/", "^https://b/"}) {
		t.Errorf("registry from env = %+v", reg)
	}
}

func TestLoadBindsRedisAddrsEnv(t *testing.T) {
	t.Setenv("DISTROFACE_DATA_DIR", t.TempDir())
	t.Setenv("DISTROFACE_REGISTRY_REDIS_ADDRS", "a:26379,b:26379")
	t.Setenv("DISTROFACE_REGISTRY_REDIS_MASTER_NAME", "primary")
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	r := cfg.Registry.Redis
	if !reflect.DeepEqual(r.Addrs, []string{"a:26379", "b:26379"}) || r.MasterName != "primary" {
		t.Errorf("sentinel redis from env = %+v", r)
	}
}

func TestLoadLeavesRedisDisabled(t *testing.T) {
	t.Setenv("DISTROFACE_DATA_DIR", t.TempDir())
	// The compose file passes the variable through even when unset
	t.Setenv("DISTROFACE_REGISTRY_REDIS_URL", "")
	cfg, err := Load(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Registry.Redis.Enabled() {
		t.Errorf("redis enabled without config: %+v", cfg.Registry.Redis)
	}
}
