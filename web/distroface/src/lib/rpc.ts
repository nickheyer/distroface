import { createClient, type Client, type Interceptor, ConnectError, type CallOptions } from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import { AuthService } from '$lib/proto/distroface/v1/auth_pb';
import { UserService } from '$lib/proto/distroface/v1/user_pb';
import { RepositoryService } from '$lib/proto/distroface/v1/repository_pb';
import { SettingsService } from '$lib/proto/distroface/v1/settings_pb';
import { TokenService } from '$lib/proto/distroface/v1/token_pb';
import { OrganizationService } from '$lib/proto/distroface/v1/organization_pb';
import { RoleService } from '$lib/proto/distroface/v1/role_pb';
import { WebhookService } from '$lib/proto/distroface/v1/webhook_pb';
import { PortalService } from '$lib/proto/distroface/v1/portal_pb';
import { ArtifactService } from '$lib/proto/distroface/v1/artifact_pb';
import { GCService } from '$lib/proto/distroface/v1/gc_pb';
import { CertificateService } from '$lib/proto/distroface/v1/certificate_pb';
import { AuditService } from '$lib/proto/distroface/v1/audit_pb';
import { HealthService } from '$lib/proto/distroface/v1/health_pb';
import { errata } from '$lib/state/errata.svelte';

export const SESSION_KEY = 'distroface_session';

export function sessionToken(): string | null {
	return typeof window !== 'undefined' ? localStorage.getItem(SESSION_KEY) : null;
}

const authInterceptor: Interceptor = (next) => async (req) => {
	const token = sessionToken();
	if (token) req.header.set('Authorization', `Bearer ${token}`);
	return next(req);
};

const errataInterceptor: Interceptor = (next) => async (req) => {
	try {
		return await next(req);
	} catch (err) {
		if (!req.header.get('X-Silent-Request') && err instanceof ConnectError) {
			errata.report(err.rawMessage || err.message || 'The request failed');
		}
		throw err;
	}
};

const transport = createConnectTransport({
	baseUrl: '',
	interceptors: [authInterceptor, errataInterceptor]
});

// Suppresses the errata slip for expected failures
export const hush: CallOptions = {
	headers: new Headers({ 'X-Silent-Request': '1' })
};

export const rpc = {
	auth: createClient(AuthService, transport) as Client<typeof AuthService>,
	user: createClient(UserService, transport) as Client<typeof UserService>,
	repository: createClient(RepositoryService, transport) as Client<typeof RepositoryService>,
	settings: createClient(SettingsService, transport) as Client<typeof SettingsService>,
	token: createClient(TokenService, transport) as Client<typeof TokenService>,
	organization: createClient(OrganizationService, transport) as Client<typeof OrganizationService>,
	role: createClient(RoleService, transport) as Client<typeof RoleService>,
	webhook: createClient(WebhookService, transport) as Client<typeof WebhookService>,
	portal: createClient(PortalService, transport) as Client<typeof PortalService>,
	artifact: createClient(ArtifactService, transport) as Client<typeof ArtifactService>,
	gc: createClient(GCService, transport) as Client<typeof GCService>,
	certificate: createClient(CertificateService, transport) as Client<typeof CertificateService>,
	audit: createClient(AuditService, transport) as Client<typeof AuditService>,
	health: createClient(HealthService, transport) as Client<typeof HealthService>
};
