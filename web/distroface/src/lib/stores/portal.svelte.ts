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
	primaryScheme = $state('http');

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
			if (resp.primaryScheme) this.primaryScheme = resp.primaryScheme;
		} catch {
			// Treated as the primary host on failure
		}
	}

	displayName = $derived(this.orgDisplayName || this.orgName);

	// Absolute URL of the primary UI, scheme comes from the server
	get primaryOrigin(): string {
		if (!this.primaryHost) return '';
		return `${this.primaryScheme}://${this.primaryHost}`;
	}

	// Registry host for docker/api examples, the host being browsed IS the
	// portal's address so the browser host is authoritative on portals
	host(fallback: string): string {
		if (this.isPortal && typeof window !== 'undefined') {
			return window.location.host;
		}
		return fallback;
	}

	// Scheme matching host(), the portal's live one or the primary's
	scheme(): string {
		if (this.isPortal && typeof window !== 'undefined') {
			return window.location.protocol.replace(':', '');
		}
		return this.primaryScheme || 'http';
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
