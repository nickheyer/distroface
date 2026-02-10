import { createClient, type Client } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { HealthService } from '$lib/proto/distroface/v1/health_pb';

const transport = createConnectTransport({
  baseUrl: "",
  interceptors: []
});

export class RpcClient {
  public readonly health: Client<typeof HealthService>;

  constructor() {
    this.health = createClient(HealthService, transport);
  }
}

export const rpcClient = new RpcClient();
