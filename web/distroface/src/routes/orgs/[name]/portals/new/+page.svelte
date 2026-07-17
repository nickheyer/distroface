<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import PortalForm from '../portal-form.svelte';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';
	import { configStore } from '$lib/stores/config.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');
	const orgId = $derived(ctx.org?.id ?? '');
	const mainPort = $derived(Number(configStore.get('server.port', 0)) || 0);

	$effect(() => {
		if (!ctx.loading && ctx.org && !ctx.canAdmin) {
			goto(resolve('/orgs/[name]', { name: orgName }));
		}
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

	<PortalForm {orgName} {orgId} {mainPort} />
</div>
