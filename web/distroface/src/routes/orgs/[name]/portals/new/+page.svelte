<script lang="ts">
	import { getContext } from 'svelte';
	import { goto } from '$app/navigation';
	import { rpc } from '$lib/rpc';
	import { OrgCtx, ORG_CTX } from '$lib/state/orgctx.svelte';
	import { errata } from '$lib/state/errata.svelte';
	import Leaf from '$lib/bits/Leaf.svelte';
	import PortalForm, { type PortalFields } from '$lib/bits/PortalForm.svelte';

	const ctx = getContext<OrgCtx>(ORG_CTX);

	let busy = $state(false);

	async function raise(f: PortalFields) {
		if (!ctx.org) return;
		busy = true;
		try {
			const r = await rpc.portal.createPortal({
				orgId: ctx.org.id,
				name: f.name,
				hostname: f.hostname,
				port: f.port,
				mapUnqualified: f.mapUnqualified,
				rules: f.rules,
				allowPush: f.allowPush,
				requireAuth: f.requireAuth,
				tls: f.tls,
				certSource: f.certSource
			});
			errata.remark(`Portal ${f.name} created.`);
			goto(`/orgs/${ctx.org.name}/portals/${r.portal?.id ?? ''}`);
		} catch {
			// Interceptor reports
		} finally {
			busy = false;
		}
	}
</script>

<Leaf no="01" title="New portal">
	<PortalForm submitLabel="Create portal" {busy} onsave={raise} />
</Leaf>
