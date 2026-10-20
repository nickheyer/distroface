<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import WebhookManager from '$lib/components/webhook-manager.svelte';
	import { WebhookScope } from '$lib/proto/distroface/v1/types_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');
	const baseHref = $derived(resolve('/orgs/[name]/webhooks', { name: orgName }));

	$effect(() => {
		if (!ctx.loading && ctx.org && !ctx.canAdmin) {
			goto(resolve('/orgs/[name]', { name: orgName }));
		}
	});
</script>

{#if ctx.loading || !ctx.org}
	<Skeleton class="h-48 w-full rounded-lg" />
{:else if ctx.canAdmin}
	<WebhookManager
		scope={WebhookScope.ORGANIZATION}
		scopeId={ctx.org.id}
		newHref={`${baseHref}/new`}
		editHref={(id) => `${baseHref}/${id}`}
		emptyDescription="Add a webhook to get notified when images are pushed, pulled, or deleted in any repository under this organization."
		createDescription="Receive HTTP POST notifications for all repositories in this organization."
	/>
{/if}
