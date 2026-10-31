package registry

import (
	"reflect"
	"testing"
	"time"

	"github.com/distribution/distribution/v3/configuration"

	"github.com/nickheyer/distroface/pkg/config"
)

func TestBuildConfigFillsParserDefaults(t *testing.T) {
	cfg := BuildConfig(Options{
		RegistryConfig: config.RegistryConfig{StoragePath: "/srv/reg"},
		CertPath:       "/srv/cert.pem",
		Addr:           "0.0.0.0:8080",
		HTTPSecret:     "abc",
	})
	if cfg.Storage.Type() != "filesystem" || cfg.Storage.Parameters()["rootdirectory"] != "/srv/reg" {
		t.Errorf("storage = %v", cfg.Storage)
	}
	if cfg.HTTP.Secret != "abc" || !cfg.HTTP.RelativeURLs {
		t.Errorf("http = %+v", cfg.HTTP)
	}
	// A zero cap empties the catalog and rejects n
	if cfg.Catalog.MaxEntries <= 0 || cfg.Tags.MaxTags <= 0 {
		t.Errorf("catalog cap %d, tags cap %d", cfg.Catalog.MaxEntries, cfg.Tags.MaxTags)
	}
	auth := cfg.Auth.Parameters()
	if cfg.Auth.Type() != "token" || auth["realm"] != "http://0.0.0.0:8080/auth/token" || auth["rootcertbundle"] != "/srv/cert.pem" {
		t.Errorf("auth = %v", cfg.Auth)
	}
	// The engine asserts this exact slice type
	if algos, ok := auth["signingalgorithms"].([]any); !ok || len(algos) != 1 || algos[0] != "ES256" {
		t.Errorf("signingalgorithms = %#v", auth["signingalgorithms"])
	}
	// A cache block would override the RegisterOptions provider
	if _, ok := cfg.Storage["cache"]; ok {
		t.Errorf("cache block handed to the engine: %v", cfg.Storage["cache"])
	}
	if cfg.Storage["delete"]["enabled"] != true || cfg.Storage["redirect"]["disable"] != true {
		t.Errorf("delete = %v redirect = %v", cfg.Storage["delete"], cfg.Storage["redirect"])
	}
	if _, ok := cfg.Storage["tag"]; ok {
		t.Error("unset tag concurrency reached the engine")
	}
	if !cfg.Health.StorageDriver.Enabled || cfg.Health.StorageDriver.Threshold <= 0 {
		t.Errorf("health = %+v", cfg.Health)
	}
	if cfg.HTTP.H2C.Enabled {
		t.Error("h2c set, only the upstream listener reads it")
	}
}

func TestBuildConfigUploadPurging(t *testing.T) {
	purge := func(cfg *configuration.Configuration) map[any]any {
		t.Helper()
		// The engine asserts this exact map type
		m, ok := cfg.Storage["maintenance"]["uploadpurging"].(map[any]any)
		if !ok {
			t.Fatalf("uploadpurging = %#v", cfg.Storage["maintenance"]["uploadpurging"])
		}
		return m
	}
	m := purge(BuildConfig(Options{RegistryConfig: config.RegistryConfig{StoragePath: "/srv/reg"}}))
	if m["enabled"] != true || m["dryrun"] != false || m["age"] != "168h0m0s" || m["interval"] != "24h0m0s" {
		t.Errorf("default purge = %v", m)
	}
	for _, key := range []string{"age", "interval"} {
		if _, err := time.ParseDuration(m[key].(string)); err != nil {
			t.Errorf("%s = %v: %v", key, m[key], err)
		}
	}
	m = purge(BuildConfig(Options{RegistryConfig: config.RegistryConfig{
		StoragePath: "/srv/reg", UploadPurgeAge: 2 * time.Hour, UploadPurgeInterval: 30 * time.Minute,
	}}))
	if m["age"] != "2h0m0s" || m["interval"] != "30m0s" {
		t.Errorf("custom purge = %v", m)
	}
}

func TestBuildConfigValidationAndTags(t *testing.T) {
	allow := []string{`^https://mcr\.microsoft\.com/`}
	cfg := BuildConfig(Options{RegistryConfig: config.RegistryConfig{
		StoragePath:          "/srv/reg",
		ManifestURLsAllow:    allow,
		ManifestURLsDeny:     []string{`^http://`},
		IndexPlatforms:       "list",
		IndexPlatformList:    []string{"linux/amd64", "windows/amd64"},
		TagLookupConcurrency: 8,
	}})
	v := cfg.Validation.Manifests
	if !reflect.DeepEqual(v.URLs.Allow, allow) || !reflect.DeepEqual(v.URLs.Deny, []string{`^http://`}) {
		t.Errorf("urls = %+v", v.URLs)
	}
	// NewApp rewrites the slices it is handed
	v.URLs.Allow[0] = "mutated"
	if allow[0] == "mutated" {
		t.Error("caller slice shared with the engine")
	}
	if v.Indexes.Platforms != "list" || !reflect.DeepEqual(v.Indexes.PlatformList, []configuration.Platform{
		{OS: "linux", Architecture: "amd64"}, {OS: "windows", Architecture: "amd64"},
	}) {
		t.Errorf("indexes = %+v", v.Indexes)
	}
	if cfg.Storage["tag"]["concurrencylimit"] != 8 {
		t.Errorf("tag = %v", cfg.Storage["tag"])
	}
}

func TestBuildConfigWithoutCertLeavesAuthOff(t *testing.T) {
	cfg := BuildConfig(Options{RegistryConfig: config.RegistryConfig{StoragePath: "/srv/reg"}})
	if cfg.Auth.Type() != "" {
		t.Errorf("auth = %v", cfg.Auth)
	}
	if cfg.HTTP.Secret == "" {
		t.Error("empty http secret")
	}
}
