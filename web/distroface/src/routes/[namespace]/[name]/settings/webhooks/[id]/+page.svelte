<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Webhook } from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import WebhookForm from '$lib/components/webhook-form.svelte';
	import { WebhookScope } from '$lib/proto/distroface/v1/types_pb';
	import type { Webhook as WebhookType } from '$lib/proto/distroface/v1/types_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const id = $derived(page.params.id ?? '');
	const repo = $derived(gitRepoStore.repo);
	const listHref = $derived(resolve('/[namespace]/[name]/settings/webhooks', { namespace, name }));

	let webhook = $state<WebhookType | null>(null);
	let loaded = $state(false);
	let loadedFor = $state('');

	$effect(() => {
		if (repo && repo.fullName !== loadedFor) {
			loadedFor = repo.fullName;
			void load(repo.id);
		}
	});

	async function load(repoId: string) {
		const resp = await rpcClient.webhook.getWebhook({ id }, silentCallOptions).catch(() => null);
		const wh = resp?.webhook;
		webhook = wh && wh.scope === WebhookScope.GIT_REPOSITORY && wh.repoId === repoId ? wh : null;
		loaded = true;
	}
</script>

<svelte:head>
	<title>Edit webhook - {namespace}/{name}</title>
</svelte:head>

{#if !loaded || !repo}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else if !webhook}
	<EmptyState
		icon={Webhook}
		message="Webhook not found"
		description="It may have been deleted or belong to another repository."
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
			scope={WebhookScope.GIT_REPOSITORY}
			scopeId={repo.id}
			{webhook}
			cancelHref={listHref}
			onSaved={() => goto(resolve('/[namespace]/[name]/settings/webhooks', { namespace, name }))}
		/>
	</FormPage>
{/if}
