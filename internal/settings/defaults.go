package settings

import (
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/proto"
)

// Defaults returns the fully populated built in baseline
func Defaults() *v1.Settings {
	return &v1.Settings{
		Server: &v1.ServerSettings{
			PublicHostname: proto.String("localhost:8080"),
		},
		Auth: &v1.AuthSettings{
			SessionTimeoutSeconds:  proto.Int32(86400),
			TokenExpirySeconds:     proto.Int32(900),
			AnonymousAccess:        proto.Bool(false),
			LocalEnabled:           proto.Bool(true),
			LocalAllowRegistration: proto.Bool(true),
			Oidc: &v1.OIDCSettings{
				Enabled:       proto.Bool(false),
				IssuerUri:     proto.String(""),
				ClientId:      proto.String(""),
				RedirectUrl:   proto.String(""),
				RoleClaim:     proto.String(""),
				GroupClaim:    proto.String("groups"),
				SkipTlsVerify: proto.Bool(false),
			},
		},
		Tls: &v1.TLSSettings{
			Mode:          v1.TLSMode_TLS_MODE_DUAL.Enum(),
			PrimarySource: v1.CertSource_CERT_SOURCE_CONFIG.Enum(),
		},
		Acme: &v1.ACMESettings{
			Enabled:         proto.Bool(false),
			Email:           proto.String(""),
			DirectoryUrl:    proto.String(""),
			ChallengePort:   proto.String("80"),
			RedirectHttp:    proto.Bool(true),
			InternalEnabled: proto.Bool(false),
		},
		Portals: &v1.PortalPolicySettings{
			RequireHostnameApproval: proto.Bool(false),
		},
		Artifacts: &v1.ArtifactSettings{
			MaxFileSizeMb:           proto.Int64(10240),
			V1Compat:                proto.Bool(true),
			StaleUploadCleanupHours: proto.Int32(24),
			PrivateByDefault:        proto.Bool(false),
			Retention: &v1.ArtifactRetentionSettings{
				Enabled:           proto.Bool(false),
				MaxVersions:       proto.Int32(5),
				MaxAgeDays:        proto.Int32(0),
				MaxTotalSizeBytes: proto.Int64(0),
				ExcludeLatest:     proto.Bool(true),
			},
			Reaper: &v1.ArtifactReaperSettings{
				Enabled:       proto.Bool(false),
				IntervalHours: proto.Int32(24),
			},
		},
		Gc: &v1.GCSettings{
			Enabled:        proto.Bool(false),
			IntervalHours:  proto.Int32(24),
			RemoveUntagged: proto.Bool(false),
		},
		RateLimit: &v1.RateLimitSettings{
			AuthFailureLimit:         proto.Int32(10),
			AuthFailureWindowSeconds: proto.Int32(300),
			PullPerMinute:            proto.Int32(0),
			AnonPullPerMinute:        proto.Int32(0),
		},
		Webhooks: &v1.WebhookSettings{
			AllowPrivateNetworks: proto.Bool(false),
		},
		Security: &v1.SecuritySettings{
			Headers: &v1.SecurityHeadersSettings{
				Enabled:               proto.Bool(true),
				Hsts:                  proto.Bool(false),
				HstsMaxAgeSeconds:     proto.Int32(31536000),
				ContentSecurityPolicy: proto.String(""),
			},
			Audit: &v1.AuditSettings{
				Enabled:       proto.Bool(true),
				RetentionDays: proto.Int32(90),
			},
		},
	}
}
