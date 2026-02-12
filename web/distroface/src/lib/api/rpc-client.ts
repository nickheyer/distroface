import { createClient, type Client, type Interceptor } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { HealthService } from '$lib/proto/distroface/v1/health_pb';
import { AuthService } from '$lib/proto/distroface/v1/auth_pb';
import { UserService } from '$lib/proto/distroface/v1/user_pb';
import { RepositoryService } from '$lib/proto/distroface/v1/repository_pb';
import { ConfigurationService } from '$lib/proto/distroface/v1/configuration_pb';

const SESSION_KEY = 'distroface_session';

const authInterceptor: Interceptor = (next) => async (req) => {
  const token = typeof window !== 'undefined' ? localStorage.getItem(SESSION_KEY) : null;
  if (token) {
    req.header.set('Authorization', `Bearer ${token}`);
  }
  return next(req);
};

const transport = createConnectTransport({
  baseUrl: "",
  interceptors: [authInterceptor]
});

export class RpcClient {
  public readonly health: Client<typeof HealthService>;
  public readonly auth: Client<typeof AuthService>;
  public readonly user: Client<typeof UserService>;
  public readonly repository: Client<typeof RepositoryService>;
  public readonly configuration: Client<typeof ConfigurationService>;

  constructor() {
    this.health = createClient(HealthService, transport);
    this.auth = createClient(AuthService, transport);
    this.user = createClient(UserService, transport);
    this.repository = createClient(RepositoryService, transport);
    this.configuration = createClient(ConfigurationService, transport);
  }
}

export const rpcClient = new RpcClient();
