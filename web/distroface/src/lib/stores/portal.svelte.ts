import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';

// Portal identity of the host serving the app, empty on the primary host
class PortalStore {
	isPortal = $state(false);
	orgName = $state('');
	orgDisplayName = $state('');
	portalName = $state('');
	allowPush = $state(true);
	mapUnqualified = $state(false);
	primaryHost = $state('');

	async init() {
		try {
			const resp = await rpcClient.portal.resolvePortal({}, silentCallOptions);
			this.isPortal = resp.isPortal;
			this.orgName = resp.orgName;
			this.orgDisplayName = resp.orgDisplayName;
			this.portalName = resp.portalName;
			this.allowPush = resp.allowPush;
			this.mapUnqualified = resp.mapUnqualified;
			this.primaryHost = resp.primaryHost;
		} catch {
			// Treated as the primary host on failure
		}
	}

	displayName = $derived(this.orgDisplayName || this.orgName);

	// Absolute URL of the primary UI, empty when unknown
	get primaryOrigin(): string {
		if (!this.primaryHost || typeof window === 'undefined') return '';
		return `${window.location.protocol}//${this.primaryHost}`;
	}

	// Registry host for docker/api examples, portals answer on their own host
	host(fallback: string): string {
		if (this.isPortal && typeof window !== 'undefined') {
			return window.location.host;
		}
		return fallback;
	}

	// Repo path as pulled through this host, org prefix dropped when mapped
	imageRef(namespace: string, name: string): string {
		if (this.isPortal && this.mapUnqualified && namespace === this.orgName) {
			return name;
		}
		return `${namespace}/${name}`;
	}
}

export const portalStore = new PortalStore();
