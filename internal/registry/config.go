package registry

import (
	"fmt"
	"net/http"
	"time"

	"github.com/distribution/distribution/v3/configuration"
	"github.com/google/uuid"

	_ "github.com/distribution/distribution/v3/registry/auth/token"
	_ "github.com/distribution/distribution/v3/registry/storage/driver/filesystem"
)

// BuildConfig creates a Distribution v3 configuration for the embedded registry
func BuildConfig(storagePath, certPath, host, port string) *configuration.Configuration {
	addr := fmt.Sprintf("%s:%s", host, port)
	realm := fmt.Sprintf("http://%s/auth/token", addr)

	cfg := &configuration.Configuration{
		Version: "0.1",
		Storage: configuration.Storage{
			"filesystem": configuration.Parameters{
				"rootdirectory": storagePath,
			},
			"delete": configuration.Parameters{
				"enabled": true,
			},
			"cache": configuration.Parameters{
				"blobdescriptor": "inmemory",
			},
		},
		Auth: configuration.Auth{
			"token": configuration.Parameters{
				"realm":          realm,
				"issuer":         "distroface",
				"service":        "distroface-registry",
				"rootcertbundle": certPath,
			},
		},
		HTTP: configuration.HTTP{
			H2C: configuration.H2C{
				Enabled: true,
			},
			Secret: uuid.New().String(),
			Headers: http.Header{
				"X-Content-Type-Options": {"nosniff"},
			},
		},
		Notifications: configuration.Notifications{
			EventConfig: configuration.Events{
				IncludeReferences: true,
			},
			Endpoints: []configuration.Endpoint{
				{
					Name:    "internal",
					URL:     fmt.Sprintf("http://127.0.0.1:%s/internal/registry/events", port),
					Timeout: 5 * time.Second,
					Headers: http.Header{
						"Authorization": {"Bearer distroface-internal-webhook-secret"},
					},
					Threshold: 5,
					Backoff:   time.Second,
				},
			},
		},
	}

	return cfg
}
