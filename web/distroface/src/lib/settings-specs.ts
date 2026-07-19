import { CertSource } from '$lib/proto/distroface/v1/certificate_pb';
import { MTLSMode, TLSMode } from '$lib/proto/distroface/v1/settings_pb';

export type FieldKind = 'bool' | 'int' | 'text' | 'secret' | 'enum' | 'strlist' | 'map' | 'bytesmb';

export interface FieldSpec {
	path: string;
	label: string;
	kind: FieldKind;
	options?: { value: number; label: string }[];
	hint?: string;
}

export interface GroupSpec {
	title: string;
	note?: string;
	fields: FieldSpec[];
}

const acmeGroup = (systemOnly: boolean): GroupSpec => ({
	title: 'ACME',
	note: 'Automatic certificates from an ACME authority.',
	fields: [
		{ path: 'acme.enabled', label: 'Enabled', kind: 'bool' },
		{ path: 'acme.email', label: 'Account email', kind: 'text' },
		{
			path: 'acme.directory_url',
			label: 'Directory URL',
			kind: 'text',
			hint: systemOnly
				? "Empty means Let's Encrypt production."
				: "Empty means Let's Encrypt production. This instance's built-in directory is /acme/directory on the primary hostname."
		},
		...(systemOnly
			? ([
					{
						path: 'acme.challenge_port',
						label: 'Challenge port',
						kind: 'text',
						hint: 'Empty disables http-01 answering.'
					},
					{
						path: 'acme.redirect_http',
						label: 'Redirect challenge listener to https',
						kind: 'bool'
					},
					{
						path: 'acme.internal_enabled',
						label: 'Serve the built-in ACME directory',
						kind: 'bool',
						hint: 'Lets portals and downstream instances obtain certificates from this instance.'
					}
				] as FieldSpec[])
			: [])
	]
});

const artifactFields: FieldSpec[] = [
	{ path: 'artifacts.max_file_size_mb', label: 'Largest file, MB', kind: 'int', hint: 'Zero means unlimited.' },
	{ path: 'artifacts.stale_upload_cleanup_hours', label: 'Stale upload cleanup, hours', kind: 'int' },
	{ path: 'artifacts.private_by_default', label: 'New repositories start private', kind: 'bool' },
	{ path: 'artifacts.retention.enabled', label: 'Retention pruning', kind: 'bool' },
	{ path: 'artifacts.retention.max_versions', label: 'Versions kept', kind: 'int', hint: 'Zero means unlimited.' },
	{ path: 'artifacts.retention.max_age_days', label: 'Age limit, days', kind: 'int', hint: 'Zero disables.' },
	{ path: 'artifacts.retention.max_total_size_bytes', label: 'Total size limit, MB', kind: 'bytesmb', hint: 'Zero means unlimited.' },
	{ path: 'artifacts.retention.exclude_latest', label: 'Keep the newest version', kind: 'bool' }
];

export const orgGroups: GroupSpec[] = [
	acmeGroup(false),
	{
		title: 'Artifacts',
		note: 'Limits and pruning for artifact repositories in this namespace.',
		fields: artifactFields
	}
];

export const portalGroups: GroupSpec[] = [
	acmeGroup(false),
	{
		title: 'Client certificates',
		fields: [
			{
				path: 'tls.mtls_mode',
				label: 'Client certificate policy',
				kind: 'enum',
				options: [
					{ value: MTLSMode.MTLS_MODE_OFF, label: 'off' },
					{ value: MTLSMode.MTLS_MODE_OPTIONAL, label: 'optional' },
					{ value: MTLSMode.MTLS_MODE_REQUIRED, label: 'required' }
				],
				hint: 'Required refuses handshakes without a trusted client certificate.'
			}
		]
	}
];

export const systemGroups: GroupSpec[] = [
	{
		title: 'Identity',
		fields: [
			{
				path: 'server.public_hostname',
				label: 'Public hostname',
				kind: 'text',
				hint: 'Host or host:port as clients reach the instance.'
			}
		]
	},
	{
		title: 'Authentication',
		fields: [
			{ path: 'auth.session_timeout_seconds', label: 'Session timeout, seconds', kind: 'int' },
			{ path: 'auth.token_expiry_seconds', label: 'Registry token expiry, seconds', kind: 'int' },
			{ path: 'auth.anonymous_access', label: 'Anonymous read access', kind: 'bool' },
			{ path: 'auth.local_enabled', label: 'Local accounts', kind: 'bool' },
			{ path: 'auth.local_allow_registration', label: 'Open registration', kind: 'bool' }
		]
	},
	{
		title: 'Identity provider',
		note: 'OIDC configuration for sign-in through an external provider.',
		fields: [
			{ path: 'auth.oidc.enabled', label: 'Enabled', kind: 'bool' },
			{ path: 'auth.oidc.issuer_uri', label: 'Issuer URI', kind: 'text' },
			{ path: 'auth.oidc.client_id', label: 'Client ID', kind: 'text' },
			{ path: 'auth.oidc.client_secret', label: 'Client secret', kind: 'secret' },
			{ path: 'auth.oidc.redirect_url', label: 'Redirect URL', kind: 'text' },
			{ path: 'auth.oidc.scopes', label: 'Scopes', kind: 'strlist' },
			{ path: 'auth.oidc.role_claim', label: 'Role claim', kind: 'text' },
			{
				path: 'auth.oidc.role_mapping',
				label: 'Role mapping',
				kind: 'map',
				hint: 'provider-role=system-role, one per line.'
			},
			{ path: 'auth.oidc.group_claim', label: 'Group claim', kind: 'text' },
			{
				path: 'auth.oidc.group_org_mapping',
				label: 'Group to organization',
				kind: 'map',
				hint: 'provider-group=org-name, one per line.'
			},
			{ path: 'auth.oidc.skip_tls_verify', label: 'Skip TLS verification', kind: 'bool' }
		]
	},
	{
		title: 'Serving TLS',
		fields: [
			{
				path: 'tls.mode',
				label: 'Listener mode',
				kind: 'enum',
				options: [
					{ value: TLSMode.TLS_MODE_DUAL, label: 'dual, tls when a certificate resolves' },
					{ value: TLSMode.TLS_MODE_HTTPS_ONLY, label: 'https only, cleartext redirects' },
					{ value: TLSMode.TLS_MODE_CLEARTEXT, label: 'cleartext, never terminate tls' }
				]
			},
			{
				path: 'tls.primary_source',
				label: 'Primary certificate source',
				kind: 'enum',
				options: [
					{ value: CertSource.CONFIG, label: 'config file pair' },
					{ value: CertSource.MANUAL, label: 'uploaded' },
					{ value: CertSource.ACME, label: 'ACME' },
					{ value: CertSource.APP_CA, label: 'issued from the instance CA' }
				],
				hint: 'ACME and instance CA sources can also be issued from the trust page.'
			},
			{
				path: 'tls.mtls_mode',
				label: 'Client certificate policy',
				kind: 'enum',
				options: [
					{ value: MTLSMode.MTLS_MODE_OFF, label: 'off' },
					{ value: MTLSMode.MTLS_MODE_OPTIONAL, label: 'optional' },
					{ value: MTLSMode.MTLS_MODE_REQUIRED, label: 'required' }
				]
			}
		]
	},
	acmeGroup(true),
	{
		title: 'Portals',
		fields: [
			{
				path: 'portals.hostname_blacklist',
				label: 'Hostname blacklist',
				kind: 'strlist',
				hint: 'Exact names or *.suffix patterns, one per line.'
			},
			{
				path: 'portals.require_hostname_approval',
				label: 'Hostname requests need approval',
				kind: 'bool'
			}
		]
	},
	{
		title: 'Artifacts',
		fields: [
			...artifactFields,
			{ path: 'artifacts.v1_compat', label: 'V1 API compatibility', kind: 'bool' },
			{ path: 'artifacts.reaper.enabled', label: 'Scheduled retention sweep', kind: 'bool' },
			{ path: 'artifacts.reaper.interval_hours', label: 'Sweep interval, hours', kind: 'int' }
		]
	},
	{
		title: 'Garbage collection',
		note: 'Scheduled mark and sweep of unreferenced registry blobs.',
		fields: [
			{ path: 'gc.enabled', label: 'Scheduled collection', kind: 'bool' },
			{ path: 'gc.interval_hours', label: 'Interval, hours', kind: 'int' },
			{ path: 'gc.remove_untagged', label: 'Remove untagged manifests', kind: 'bool' }
		]
	},
	{
		title: 'Rate limits',
		note: 'Zero disables each limit.',
		fields: [
			{ path: 'rate_limit.auth_failure_limit', label: 'Auth failures allowed', kind: 'int' },
			{ path: 'rate_limit.auth_failure_window_seconds', label: 'Failure window, seconds', kind: 'int' },
			{ path: 'rate_limit.pull_per_minute', label: 'Pulls per minute', kind: 'int' },
			{ path: 'rate_limit.anon_pull_per_minute', label: 'Anonymous pulls per minute', kind: 'int' }
		]
	},
	{
		title: 'Webhook delivery',
		fields: [
			{ path: 'webhooks.allow_private_networks', label: 'Allow private network targets', kind: 'bool' }
		]
	},
	{
		title: 'Security',
		fields: [
			{ path: 'security.headers.enabled', label: 'Security headers', kind: 'bool' },
			{ path: 'security.headers.hsts', label: 'HSTS', kind: 'bool' },
			{ path: 'security.headers.hsts_max_age_seconds', label: 'HSTS max age, seconds', kind: 'int' },
			{
				path: 'security.headers.content_security_policy',
				label: 'Content security policy',
				kind: 'text',
				hint: 'Empty keeps the built-in policy.'
			},
			{ path: 'security.audit.enabled', label: 'Audit trail', kind: 'bool' },
			{
				path: 'security.audit.retention_days',
				label: 'Audit retention, days',
				kind: 'int',
				hint: 'Zero keeps history forever.'
			}
		]
	}
];
