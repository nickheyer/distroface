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

	get gitEnabled(): boolean {
		return this.settings?.git?.enabled === true;
	}

	get sshEnabled(): boolean {
		return this.gitEnabled && this.settings?.git?.sshEnabled === true;
	}

	// Host or host:port shown in ssh clone urls
	get sshPublicHost(): string {
		const configured = this.settings?.git?.sshPublicHost;
		if (configured) return configured;
		const host = this.publicHostname.replace(/:\d+$/, '');
		const port = this.sshPort;
		return port && port !== 22 ? `${host}:${port}` : host;
	}

	get sshPort(): number {
		return this.settings?.git?.sshPort ?? 22;
	}

	get lfsEnabled(): boolean {
		return this.settings?.git?.lfsEnabled === true;
	}

	get actionsEnabled(): boolean {
		return this.gitEnabled && this.settings?.git?.actions?.enabled === true;
	}
}

export const configStore = new ConfigStore();
