import { rpc, hush } from '$lib/rpc';
import { TLSMode, type Settings } from '$lib/proto/distroface/v1/settings_pb';

const FALLBACK_HOSTNAME = 'localhost:8080';

// Effective system settings, public subset before sign-in
class Site {
	settings = $state<Settings | undefined>();

	async init() {
		try {
			const r = await rpc.settings.getEffectiveSettings({}, hush);
			this.settings = r.settings;
		} catch {
			// Anonymous callers may be refused entirely
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

export const site = new Site();

// Portal identity of the serving host, empty on the primary host
class Gate {
	isPortal = $state(false);
	orgName = $state('');
	orgDisplayName = $state('');
	portalName = $state('');
	allowPush = $state(true);
	requireAuth = $state(false);
	mapUnqualified = $state(false);
	primaryHost = $state('');
	primaryScheme = $state('http');

	displayName = $derived(this.orgDisplayName || this.orgName);

	async init() {
		try {
			const r = await rpc.portal.resolvePortal({}, hush);
			this.isPortal = r.isPortal;
			this.orgName = r.orgName;
			this.orgDisplayName = r.orgDisplayName;
			this.portalName = r.portalName;
			this.allowPush = r.allowPush;
			this.requireAuth = r.requireAuth;
			this.mapUnqualified = r.mapUnqualified;
			this.primaryHost = r.primaryHost;
			if (r.primaryScheme) this.primaryScheme = r.primaryScheme;
		} catch {
			// Treated as the primary host on failure
		}
	}

	get primaryOrigin(): string {
		if (!this.primaryHost) return '';
		return `${this.primaryScheme}://${this.primaryHost}`;
	}

	// Registry host for docker examples, the browsed host is authoritative on portals
	host(): string {
		if (this.isPortal && typeof window !== 'undefined') return window.location.host;
		return site.publicHostname;
	}

	// Repo path as pulled through this host, org prefix dropped when mapped
	imageRef(namespace: string, name: string): string {
		if (this.isPortal && this.mapUnqualified && namespace === this.orgName) return name;
		return `${namespace}/${name}`;
	}
}

export const gate = new Gate();
