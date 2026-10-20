<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import { Webhook } from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import WebhookForm from '$lib/components/webhook-form.svelte';
	import { WebhookScope } from '$lib/proto/distroface/v1/types_pb';
	import type { Webhook as WebhookType } from '$lib/proto/distroface/v1/types_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');
	const id = $derived(page.params.id ?? '');
	const listHref = $derived(resolve('/orgs/[name]/webhooks', { name: orgName }));

	let webhook = $state<WebhookType | null>(null);
	let loaded = $state(false);
	let loadedFor = $state('');

	$effect(() => {
		if (!ctx.loading && ctx.org && !ctx.canAdmin) {
			goto(resolve('/orgs/[name]', { name: orgName }));
		}
	});

	$effect(() => {
		const org = ctx.org;
		if (org && org.id !== loadedFor) {
			loadedFor = org.id;
			void load(org.id);
		}
	});

	async function load(orgId: string) {
		const resp = await rpcClient.webhook.getWebhook({ id }, silentCallOptions).catch(() => null);
		const wh = resp?.webhook;
		webhook = wh && wh.scope === WebhookScope.ORGANIZATION && wh.orgId === orgId ? wh : null;
		loaded = true;
	}
</script>

<svelte:head>
	<title>Edit webhook - {orgName}</title>
</svelte:head>

{#if ctx.loading || !ctx.org || !loaded}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else if ctx.canAdmin}
	{#if !webhook}
		<EmptyState
			icon={Webhook}
			message="Webhook not found"
			description="It may have been deleted or belong to another organization."
		>
			{#snippet actions()}
				<Button variant="outline" href={listHref}>Back to webhooks</Button>
			{/snippet}
		</EmptyState>
	{:else}
		<FormPage
			level="section"
			title="Edit webhook"
			description="Changes apply to the next delivery."
			backHref={listHref}
			backLabel="Webhooks"
		>
			<WebhookForm
				scope={WebhookScope.ORGANIZATION}
				scopeId={ctx.org.id}
				{webhook}
				cancelHref={listHref}
				onSaved={() => goto(resolve('/orgs/[name]/webhooks', { name: orgName }))}
			/>
		</FormPage>
	{/if}
{/if}
