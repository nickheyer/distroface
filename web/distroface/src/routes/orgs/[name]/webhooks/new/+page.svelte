<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import FormPage from '$lib/components/form-page.svelte';
	import WebhookForm from '$lib/components/webhook-form.svelte';
	import { WebhookScope } from '$lib/proto/distroface/v1/types_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');
	const listHref = $derived(resolve('/orgs/[name]/webhooks', { name: orgName }));

	$effect(() => {
		if (!ctx.loading && ctx.org && !ctx.canAdmin) {
			goto(resolve('/orgs/[name]', { name: orgName }));
		}
	});
</script>

<svelte:head>
	<title>Add webhook - {orgName}</title>
</svelte:head>

{#if ctx.loading || !ctx.org}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else if ctx.canAdmin}
	<FormPage
		level="section"
		title="Add webhook"
		description="Receive HTTP POST notifications for all repositories in this organization."
		backHref={listHref}
		backLabel="Webhooks"
	>
		<WebhookForm
			scope={WebhookScope.ORGANIZATION}
			scopeId={ctx.org.id}
			cancelHref={listHref}
			onSaved={() => goto(resolve('/orgs/[name]/webhooks', { name: orgName }))}
		/>
	</FormPage>
{/if}
