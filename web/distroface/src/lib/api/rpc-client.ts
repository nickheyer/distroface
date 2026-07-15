import {
	createClient,
	type Client,
	type Interceptor,
	ConnectError,
	type CallOptions
} from '@connectrpc/connect';
import { createConnectTransport } from '@connectrpc/connect-web';
import { HealthService } from '$lib/proto/distroface/v1/health_pb';
import { AuthService } from '$lib/proto/distroface/v1/auth_pb';
import { UserService } from '$lib/proto/distroface/v1/user_pb';
import { RepositoryService } from '$lib/proto/distroface/v1/repository_pb';
import { ConfigurationService } from '$lib/proto/distroface/v1/configuration_pb';
import { TokenService } from '$lib/proto/distroface/v1/token_pb';
import { OrganizationService } from '$lib/proto/distroface/v1/organization_pb';
import { RoleService } from '$lib/proto/distroface/v1/role_pb';
import { WebhookService } from '$lib/proto/distroface/v1/webhook_pb';
import { PortalService } from '$lib/proto/distroface/v1/portal_pb';
import { ArtifactService } from '$lib/proto/distroface/v1/artifact_pb';
import { GCService } from '$lib/proto/distroface/v1/gc_pb';
import { toast } from 'svelte-sonner';

const SESSION_KEY = 'distroface_session';

const authInterceptor: Interceptor = (next) => async (req) => {
	const token = typeof window !== 'undefined' ? localStorage.getItem(SESSION_KEY) : null;
	if (token) {
		req.header.set('Authorization', `Bearer ${token}`);
	}
	return next(req);
};

const errorInterceptor: Interceptor = (next) => async (req) => {
	try {
		return await next(req);
	} catch (err) {
		if (req.header.get('X-Silent-Request')) {
			throw err;
		}
		if (err instanceof ConnectError) {
			const message = err.rawMessage || err.message || 'An unexpected error occurred';
			toast.error(message);
		}
		throw err;
	}
};

const transport = createConnectTransport({
	baseUrl: '',
	interceptors: [authInterceptor, errorInterceptor]
});

export const silentCallOptions: CallOptions = {
	headers: new Headers({ 'X-Silent-Request': '1' })
};

export class RpcClient {
	public readonly health: Client<typeof HealthService>;
	public readonly auth: Client<typeof AuthService>;
	public readonly user: Client<typeof UserService>;
	public readonly repository: Client<typeof RepositoryService>;
	public readonly configuration: Client<typeof ConfigurationService>;
	public readonly token: Client<typeof TokenService>;
	public readonly organization: Client<typeof OrganizationService>;
	public readonly role: Client<typeof RoleService>;
	public readonly webhook: Client<typeof WebhookService>;
	public readonly portal: Client<typeof PortalService>;
	public readonly artifact: Client<typeof ArtifactService>;
	public readonly gc: Client<typeof GCService>;

	constructor() {
		this.health = createClient(HealthService, transport);
		this.auth = createClient(AuthService, transport);
		this.user = createClient(UserService, transport);
		this.repository = createClient(RepositoryService, transport);
		this.configuration = createClient(ConfigurationService, transport);
		this.token = createClient(TokenService, transport);
		this.organization = createClient(OrganizationService, transport);
		this.role = createClient(RoleService, transport);
		this.webhook = createClient(WebhookService, transport);
		this.portal = createClient(PortalService, transport);
		this.artifact = createClient(ArtifactService, transport);
		this.gc = createClient(GCService, transport);
	}
}

export const rpcClient = new RpcClient();
