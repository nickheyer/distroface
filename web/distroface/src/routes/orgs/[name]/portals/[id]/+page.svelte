<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
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
		} catch { portal = null; }
		finally { loading = false; }
	}

	// Refetch when navigating between portal ids on the same route
	$effect(() => {
		load(portalId);
	});
</script>

{#if loading}
	<div class="space-y-4">
		<Skeleton class="h-8 w-56" />
		<Skeleton class="h-96 w-full rounded-xl" />
	</div>
{:else if portal}
	<div class="space-y-4">
		<div class="section-header">
			<div class="min-w-0 space-y-1">
				<h2 class="section-title">Edit Portal</h2>
				<p class="section-subtitle max-w-2xl">Changes apply to live traffic immediately.</p>
			</div>
		</div>

		{#key portal.id}
			<PortalForm {orgName} {orgId} {mainPort} {portal} />
		{/key}
	</div>
{:else}
	<div class="text-center py-12">
		<div class="h-12 w-12 rounded-xl bg-muted/50 flex items-center justify-center mx-auto mb-4">
			<Globe class="h-6 w-6 text-muted-foreground/50" />
		</div>
		<h2 class="text-lg font-semibold">Portal not found</h2>
		<p class="text-[13px] text-muted-foreground mt-1">It may have been deleted.</p>
		<Button
			variant="outline"
			class="mt-4"
			onclick={() => goto(resolve('/orgs/[name]/portals', { name: orgName }))}
		>
			Back to Portals
		</Button>
	</div>
{/if}
