<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount, getContext } from 'svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { silentCallOptions } from '$lib/api/rpc-client';
	import PortalForm from '../portal-form.svelte';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');

	let mainPort = $state(0);

	$effect(() => {
		if (!ctx.loading && ctx.org && !ctx.canAdmin) {
			goto(resolve('/orgs/[name]', { name: orgName }));
		}
	});

	onMount(async () => {
		try {
			const resp = await rpcClient.portal.listPortals({ orgName }, silentCallOptions);
			mainPort = resp.mainPort;
		} catch { /* preview just omits the port hint */ }
	});
</script>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0 space-y-1">
			<h2 class="section-title">New Portal</h2>
			<p class="section-subtitle max-w-2xl">
				Give {orgName} its own address. Clients on it only ever see this organization.
			</p>
		</div>
	</div>

	<PortalForm {orgName} {mainPort} />
</div>
