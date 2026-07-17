import { rpcClient } from '$lib/api/rpc-client';
import { authStore } from '$lib/stores/auth.svelte';
import { OrgRole } from '$lib/proto/distroface/v1/types_pb';
import type { Organization } from '$lib/proto/distroface/v1/types_pb';

export const ORG_CONTEXT_KEY = Symbol('org-page-context');

// Org loaded once by the layout, shared with every org sub-page
export class OrgContext {
	name = $state('');
	org = $state<Organization | null>(null);
	loading = $state(true);

	// Mirrors backend requireOrgAdmin, owner/admin membership or global grant
	get canAdmin(): boolean {
		const role = this.org?.currentUserRole;
		const id = this.org?.id ?? '';
		return (
			role === OrgRole.OWNER ||
			role === OrgRole.ADMIN ||
			authStore.hasPermission('organizations', 'update', id) ||
			authStore.hasPermission('organizations', 'manage', id)
		);
	}

	get canDelete(): boolean {
		const id = this.org?.id ?? '';
		return (
			this.org?.currentUserRole === OrgRole.OWNER ||
			authStore.hasPermission('organizations', 'delete', id) ||
			authStore.hasPermission('organizations', 'manage', id)
		);
	}

	async load(name: string) {
		this.name = name;
		this.loading = true;
		try {
			const resp = await rpcClient.organization.getOrganization({ name });
			this.org = resp.organization ?? null;
		} catch {
			this.org = null;
		} finally {
			this.loading = false;
		}
	}

	async refresh() {
		if (this.name) await this.load(this.name);
	}
}
