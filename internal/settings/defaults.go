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
			SessionTimeoutSeconds:    proto.Int32(86400),
			TokenExpirySeconds:       proto.Int32(900),
			AnonymousAccess:          proto.Bool(false),
			LocalEnabled:             proto.Bool(true),
			LocalAllowRegistration:   proto.Bool(true),
			RequireEmailVerification: proto.Bool(false),
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
			MtlsMode:      v1.MTLSMode_MTLS_MODE_OFF.Enum(),
		},
		Acme: &v1.ACMESettings{
			Enabled:       proto.Bool(false),
			Email:         proto.String(""),
			DirectoryUrl:  proto.String(""),
			ChallengePort: proto.String("80"),
			RedirectHttp:  proto.Bool(true),
		},
		Ca: &v1.CASettings{
			AcmeEnabled: proto.Bool(false),
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
		Mirror: &v1.MirrorSettings{
			Enabled:                  proto.Bool(true),
			DefaultIntervalMinutes:   proto.Int32(60),
			AllowPrivateNetworks:     proto.Bool(false),
			MinIntervalMinutes:       proto.Int32(5),
			MaxConcurrentSyncs:       proto.Int32(3),
			PerHostSpacingMs:         proto.Int32(750),
			SyncTimeoutMinutes:       proto.Int32(45),
			RateLimitCooldownMinutes: proto.Int32(15),
			MaxCooldownMinutes:       proto.Int32(360),
			FailureBackoffMinutes:    proto.Int32(5),
			MaxSyncDepth:             proto.Int32(0),
		},
		Git: &v1.GitSettings{
			Enabled:                proto.Bool(true),
			SshEnabled:             proto.Bool(false),
			SshPort:                proto.Int32(22),
			SshPublicHost:          proto.String(""),
			LfsEnabled:             proto.Bool(true),
			MaxLfsObjectMb:         proto.Int64(2048),
			MaxRepoSizeMb:          proto.Int64(0),
			PrivateByDefault:       proto.Bool(false),
			DefaultBranch:          proto.String("main"),
			ReleaseOnTag:           proto.Bool(false),
			MaxReleaseAssetMb:      proto.Int64(2048),
			MaxConcurrentTransfers: proto.Int32(32),
			AllowForks:             proto.Bool(true),
			Gc: &v1.GitGCSettings{
				Enabled:       proto.Bool(true),
				IntervalHours: proto.Int32(168),
			},
			Signing: &v1.GitSigningSettings{
				Enabled: proto.Bool(false),
			},
			Actions: &v1.GitActionsSettings{
				Enabled:               proto.Bool(false),
				ArtifactRetentionDays: proto.Int32(90),
				LogRetentionDays:      proto.Int32(90),
				RunOnMirror:           proto.Bool(false),
				DefaultRunsOn:         proto.String("ubuntu-latest"),
				DefaultActionsUrl:     proto.String("https://github.com"),
				StarterWorkflowsUrl:   proto.String("https://github.com/actions/starter-workflows"),
				BuiltinRunner: &v1.BuiltinRunnerSettings{
					Enabled:     proto.Bool(false),
					Labels:      proto.String("ubuntu-latest"),
					Concurrency: proto.Int32(2),
					Image:       proto.String(""),
				},
			},
			Agents: &v1.GitAgentsSettings{
				Enabled:               proto.Bool(false),
				AllowCommands:         proto.Bool(false),
				CommandsRunsOn:        proto.String(""),
				MaxTurns:              proto.Int32(40),
				MaxSessionMinutes:     proto.Int32(0),
				MaxSessionsPerDay:     proto.Int32(0),
				MaxConcurrentSessions: proto.Int32(2),
				TokenBudgetPerSession: proto.Int64(1000000),
				LogRetentionDays:      proto.Int32(90),
				AllowPrivateNetworks:  proto.Bool(true),
				AllowRepoProviders:    proto.Bool(true),
				AllowRepoAgents:       proto.Bool(true),
				AllowFileAgents:       proto.Bool(true),
				CommandTimeoutMinutes: proto.Int32(20),
				DebugToolCalls:        proto.Bool(false),
			},
		},
		Mail: &v1.MailSettings{
			Enabled:              proto.Bool(false),
			SmtpHost:             proto.String(""),
			SmtpPort:             proto.Int32(587),
			Security:             v1.MailSecurity_MAIL_SECURITY_STARTTLS.Enum(),
			Username:             proto.String(""),
			FromAddress:          proto.String(""),
			FromName:             proto.String("Distroface"),
			ReplyTo:              proto.String(""),
			AllowPrivateNetworks: proto.Bool(true),
			SkipTlsVerify:        proto.Bool(false),
			MaxPerMinute:         proto.Int32(0),
			Incoming: &v1.MailIncomingSettings{
				Enabled:      proto.Bool(false),
				ImapHost:     proto.String(""),
				ImapPort:     proto.Int32(993),
				Tls:          proto.Bool(true),
				Username:     proto.String(""),
				Mailbox:      proto.String("INBOX"),
				ReplyAddress: proto.String(""),
			},
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
