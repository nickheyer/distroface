package registry

import (
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/distribution/distribution/v3/configuration"
	"github.com/google/uuid"

	"github.com/nickheyer/distroface/pkg/config"
	"github.com/nickheyer/distroface/pkg/logger"

	// Registers the token auth and filesystem drivers
	_ "github.com/distribution/distribution/v3/registry/auth/token"
	_ "github.com/distribution/distribution/v3/registry/storage/driver/filesystem"
)

// Parser defaults the yaml path sets, structs do not
const (
	catalogMaxEntries = 1000
	tagsMaxEntries    = 1000
)

// Upstream defaults for abandoned upload sessions
const (
	defaultUploadPurgeAge      = 168 * time.Hour
	defaultUploadPurgeInterval = 24 * time.Hour
)

// Storage probe cadence from the upstream example config
const (
	storageCheckInterval  = 10 * time.Second
	storageCheckThreshold = 3
)

// Boot inputs for the embedded distribution app
type Options struct {
	config.RegistryConfig
	// Signing cert bundle, empty leaves the api open
	CertPath string
	// Bearer challenge address, portals rewrite it per request
	Addr string
	// Signs client upload state, empty means fresh per boot
	HTTPSecret string
	Log        *logger.Logger
}

// Distribution v3 configuration for the embedded registry
func BuildConfig(opts Options) *configuration.Configuration {
	secret := opts.HTTPSecret
	if secret == "" {
		secret = uuid.New().String()
	}
	purgeAge, purgeInterval := opts.UploadPurgeAge, opts.UploadPurgeInterval
	if purgeAge <= 0 {
		purgeAge = defaultUploadPurgeAge
	}
	if purgeInterval <= 0 {
		purgeInterval = defaultUploadPurgeInterval
	}

	// The descriptor cache arrives through RegisterOptions, see cache.go
	storage := configuration.Storage{
		"filesystem": configuration.Parameters{"rootdirectory": opts.StoragePath},
		"delete":     configuration.Parameters{"enabled": true},
		// Local disk never redirects, skip the per blob lookup
		"redirect": configuration.Parameters{"disable": true},
		"maintenance": configuration.Parameters{
			"uploadpurging": map[any]any{
				"enabled":  true,
				"age":      purgeAge.String(),
				"interval": purgeInterval.String(),
				"dryrun":   false,
			},
		},
	}
	if opts.TagLookupConcurrency > 0 {
		storage["tag"] = configuration.Parameters{"concurrencylimit": opts.TagLookupConcurrency}
	}

	cfg := &configuration.Configuration{
		Version: "0.1",
		Storage: storage,
		HTTP: configuration.HTTP{
			Secret: secret,
			// Proxies and portals terminate tls on other hosts
			RelativeURLs: true,
			Headers: http.Header{
				"X-Content-Type-Options": {"nosniff"},
			},
		},
		Catalog: configuration.Catalog{MaxEntries: catalogMaxEntries},
		Tags:    configuration.Tags{MaxTags: tagsMaxEntries},
		Validation: configuration.Validation{
			Manifests: configuration.ValidationManifests{
				// The engine rewrites these in place while compiling them
				URLs: configuration.URLs{
					Allow: slices.Clone(opts.ManifestURLsAllow),
					Deny:  slices.Clone(opts.ManifestURLsDeny),
				},
				Indexes: configuration.ValidationIndexes{
					Platforms:    configuration.Platforms(opts.IndexPlatforms),
					PlatformList: platformList(opts.IndexPlatformList),
				},
			},
		},
		Health: configuration.Health{
			StorageDriver: configuration.StorageDriver{
				Enabled:   true,
				Interval:  storageCheckInterval,
				Threshold: storageCheckThreshold,
			},
		},
		Middleware: map[string][]configuration.Middleware{
			"repository": {
				{Name: "distroface"},
			},
		},
	}

	if opts.CertPath != "" {
		cfg.Auth = configuration.Auth{
			"token": configuration.Parameters{
				"realm":          fmt.Sprintf("http://%s/auth/token", opts.Addr),
				"issuer":         "distroface",
				"service":        "distroface-registry",
				"rootcertbundle": opts.CertPath,
				// The token service signs es256 and nothing else
				"signingalgorithms": []any{"ES256"},
			},
		}
	}
	return cfg
}

// Splits os/arch entries into the yaml platformlist shape
func platformList(entries []string) []configuration.Platform {
	if len(entries) == 0 {
		return nil
	}
	out := make([]configuration.Platform, 0, len(entries))
	for _, entry := range entries {
		os, arch, _ := strings.Cut(entry, "/")
		out = append(out, configuration.Platform{OS: os, Architecture: arch})
	}
	return out
}
