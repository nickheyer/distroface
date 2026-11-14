package settings

import (
	v1 "github.com/nickheyer/distroface/pkg/proto/distroface/v1"
	"google.golang.org/protobuf/proto"
)

// Defaults returns the fully populated built in baseline
func Defaults() *v1.Settings {
	return &v1.Settings{
		Server: &v1.ServerSettings{
			PublicHostname: new("localhost:8080"),
		},
		Auth: &v1.AuthSettings{
			SessionTimeoutSeconds:    proto.Int32(86400),
			TokenExpirySeconds:       proto.Int32(900),
			AnonymousAccess:          new(false),
			LocalEnabled:             new(true),
			LocalAllowRegistration:   new(true),
			RequireEmailVerification: new(false),
			Oidc: &v1.OIDCSettings{
				Enabled:       new(false),
				IssuerUri:     new(""),
				ClientId:      new(""),
				RedirectUrl:   new(""),
				RoleClaim:     new(""),
				GroupClaim:    new("groups"),
				SkipTlsVerify: new(false),
			},
		},
		Tls: &v1.TLSSettings{
			Mode:          v1.TLSMode_TLS_MODE_DUAL.Enum(),
			PrimarySource: v1.CertSource_CERT_SOURCE_CONFIG.Enum(),
			MtlsMode:      v1.MTLSMode_MTLS_MODE_OFF.Enum(),
		},
		Acme: &v1.ACMESettings{
			Enabled:       new(false),
			Email:         new(""),
			DirectoryUrl:  new(""),
			ChallengePort: new("80"),
			RedirectHttp:  new(true),
		},
		Ca: &v1.CASettings{
			AcmeEnabled: new(false),
		},
		Portals: &v1.PortalPolicySettings{
			RequireHostnameApproval: new(false),
		},
		Artifacts: &v1.ArtifactSettings{
			MaxFileSizeMb:           proto.Int64(10240),
			V1Compat:                new(true),
			StaleUploadCleanupHours: proto.Int32(24),
			PrivateByDefault:        new(false),
			Retention: &v1.ArtifactRetentionSettings{
				Enabled:           new(false),
				MaxVersions:       proto.Int32(5),
				MaxAgeDays:        proto.Int32(0),
				MaxTotalSizeBytes: proto.Int64(0),
				ExcludeLatest:     new(true),
			},
			Reaper: &v1.ArtifactReaperSettings{
				Enabled:       new(false),
				IntervalHours: proto.Int32(24),
			},
		},
		Gc: &v1.GCSettings{
			Enabled:        new(false),
			IntervalHours:  proto.Int32(24),
			RemoveUntagged: new(false),
		},
		RateLimit: &v1.RateLimitSettings{
			AuthFailureLimit:         proto.Int32(10),
			AuthFailureWindowSeconds: proto.Int32(300),
			PullPerMinute:            proto.Int32(0),
			AnonPullPerMinute:        proto.Int32(0),
		},
		Webhooks: &v1.WebhookSettings{
			AllowPrivateNetworks: new(false),
		},
		Mirror: &v1.MirrorSettings{
			Enabled:                  new(true),
			DefaultIntervalMinutes:   proto.Int32(60),
			AllowPrivateNetworks:     new(false),
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
			Enabled:                new(true),
			SshEnabled:             new(false),
			SshPort:                proto.Int32(22),
			SshPublicHost:          new(""),
			SshPortalPorts:         new(false),
			LfsEnabled:             new(true),
			MaxLfsObjectMb:         proto.Int64(2048),
			MaxRepoSizeMb:          proto.Int64(0),
			PrivateByDefault:       new(false),
			DefaultBranch:          new("main"),
			ReleaseOnTag:           new(false),
			MaxReleaseAssetMb:      proto.Int64(2048),
			MaxConcurrentTransfers: proto.Int32(32),
			AllowForks:             new(true),
			Gc: &v1.GitGCSettings{
				Enabled:       new(true),
				IntervalHours: proto.Int32(168),
			},
			Signing: &v1.GitSigningSettings{
				Enabled: new(false),
			},
			Actions: &v1.GitActionsSettings{
				Enabled:               new(false),
				ArtifactRetentionDays: proto.Int32(90),
				LogRetentionDays:      proto.Int32(90),
				RunOnMirror:           new(false),
				DefaultRunsOn:         new("ubuntu-latest"),
				DefaultActionsUrl:     new("https://github.com"),
				StarterWorkflowsUrl:   new("https://github.com/actions/starter-workflows"),
				BuiltinRunner: &v1.BuiltinRunnerSettings{
					Enabled:         new(false),
					Labels:          new("ubuntu-latest"),
					Concurrency:     proto.Int32(2),
					Image:           new(""),
					ConnectUrl:      new(""),
					InsecureSkipTls: new(false),
				},
			},
			Agents: &v1.GitAgentsSettings{
				Enabled:               new(false),
				AllowCommands:         new(false),
				CommandsRunsOn:        new(""),
				MaxTurns:              proto.Int32(40),
				MaxSessionMinutes:     proto.Int32(0),
				MaxSessionsPerDay:     proto.Int32(0),
				MaxConcurrentSessions: proto.Int32(2),
				TokenBudgetPerSession: proto.Int64(1000000),
				LogRetentionDays:      proto.Int32(90),
				AllowPrivateNetworks:  new(true),
				AllowRepoProviders:    new(true),
				AllowRepoAgents:       new(true),
				AllowFileAgents:       new(true),
				CommandTimeoutMinutes: proto.Int32(20),
				DebugToolCalls:        new(false),
			},
		},
		Mail: &v1.MailSettings{
			Enabled:              new(false),
			SmtpHost:             new(""),
			SmtpPort:             proto.Int32(587),
			Security:             v1.MailSecurity_MAIL_SECURITY_STARTTLS.Enum(),
			Username:             new(""),
			FromAddress:          new(""),
			FromName:             new("Distroface"),
			ReplyTo:              new(""),
			AllowPrivateNetworks: new(true),
			SkipTlsVerify:        new(false),
			MaxPerMinute:         proto.Int32(0),
			Incoming: &v1.MailIncomingSettings{
				Enabled:      new(false),
				ImapHost:     new(""),
				ImapPort:     proto.Int32(993),
				Tls:          new(true),
				Username:     new(""),
				Mailbox:      new("INBOX"),
				ReplyAddress: new(""),
			},
		},
		Security: &v1.SecuritySettings{
			Headers: &v1.SecurityHeadersSettings{
				Enabled:               new(true),
				Hsts:                  new(false),
				HstsMaxAgeSeconds:     proto.Int32(31536000),
				ContentSecurityPolicy: new(""),
			},
			Audit: &v1.AuditSettings{
				Enabled:       new(true),
				RetentionDays: proto.Int32(90),
			},
		},
	}
}
