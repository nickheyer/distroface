import { rpc, hush } from '$lib/rpc';
import { OrgRole, type Organization } from '$lib/proto/distroface/v1/types_pb';

// One org loaded by name, shared by the org layout's pages
export class OrgCtx {
	org = $state<Organization | null>(null);
	missing = $state(false);
	name = '';

	isAdmin = $derived(
		this.org?.currentUserRole === OrgRole.OWNER || this.org?.currentUserRole === OrgRole.ADMIN
	);
	isOwner = $derived(this.org?.currentUserRole === OrgRole.OWNER);

	async load(name: string) {
		this.name = name;
		this.missing = false;
		this.org = null;
		try {
			const r = await rpc.organization.getOrganization({ name }, hush);
			this.org = r.organization ?? null;
			this.missing = !this.org;
		} catch {
			this.missing = true;
		}
	}

	async refresh() {
		if (this.name) await this.load(this.name);
	}
}

export const ORG_CTX = Symbol('org-ctx');
