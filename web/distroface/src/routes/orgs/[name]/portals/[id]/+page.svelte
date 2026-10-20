<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import { Globe } from '@lucide/svelte';
	import PortalForm from '../portal-form.svelte';
	import type { RegistryPortal } from '$lib/proto/distroface/v1/portal_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';
	import { configStore } from '$lib/stores/config.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');
	const orgId = $derived(ctx.org?.id ?? '');
	const portalId = $derived(page.params.id ?? '');
	const mainPort = $derived(configStore.mainPort);
	const listHref = $derived(resolve('/orgs/[name]/portals', { name: orgName }));

	let portal = $state<RegistryPortal | null>(null);
	let loading = $state(true);

	$effect(() => {
		if (!ctx.loading && ctx.org && !ctx.canAdmin) {
			goto(resolve('/orgs/[name]', { name: orgName }));
		}
	});

	async function load(id: string) {
		loading = true;
		try {
			const resp = await rpcClient.portal.getPortal({ orgId, id });
			portal = resp.portal ?? null;
		} catch {
			portal = null;
		} finally {
			loading = false;
		}
	}

	// Refetch when navigating between portal ids on the same route
	$effect(() => {
		load(portalId);
	});
</script>

{#if loading}
	<div class="space-y-6">
		<div class="space-y-2">
			<Skeleton class="h-5 w-40" />
			<Skeleton class="h-4 w-64" />
		</div>
		<Skeleton class="h-96 w-full rounded-lg" />
	</div>
{:else if portal}
	<FormPage
		level="section"
		title="Edit {portal.name}"
		description="Changes apply to live traffic immediately."
		backHref={listHref}
		backLabel="Portals"
	>
		{#key portal.id}
			<PortalForm {orgName} {orgId} {mainPort} {portal} />
		{/key}
	</FormPage>
{:else}
	<EmptyState icon={Globe} message="Portal not found" description="It may have been deleted.">
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to portals</Button>
		{/snippet}
	</EmptyState>
{/if}
