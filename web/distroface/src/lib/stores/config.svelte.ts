import { rpcClient } from '$lib/api/rpc-client';
import { TLSMode, type Settings } from '$lib/proto/distroface/v1/settings_pb';

const FALLBACK_HOSTNAME = 'localhost:8080';

// Effective system settings, public subset before sign-in
class ConfigStore {
	settings = $state<Settings | undefined>();

	async init() {
		try {
			const resp = await rpcClient.settings.getEffectiveSettings({});
			this.settings = resp.settings;
		} catch {
			// nada on failure
		}
	}

	get publicHostname(): string {
		return this.settings?.server?.publicHostname || FALLBACK_HOSTNAME;
	}

	// Port embedded in the public hostname, zero when absent
	get mainPort(): number {
		const idx = this.publicHostname.lastIndexOf(':');
		const tail = idx > -1 ? this.publicHostname.slice(idx + 1) : '';
		return /^\d+$/.test(tail) ? Number(tail) : 0;
	}

	get httpsOnly(): boolean {
		return this.settings?.tls?.mode === TLSMode.TLS_MODE_HTTPS_ONLY;
	}
}

export const configStore = new ConfigStore();
