<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import FormPage from '$lib/components/form-page.svelte';
	import WebhookForm from '$lib/components/webhook-form.svelte';
	import { WebhookScope } from '$lib/proto/distroface/v1/types_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const listHref = $derived(resolve('/[namespace]/[name]/settings/webhooks', { namespace, name }));
</script>

<svelte:head>
	<title>Add webhook - {namespace}/{name}</title>
</svelte:head>

{#if repo}
	<FormPage
		level="section"
		title="Add webhook"
		description="Receive HTTP POST notifications for repository events."
		backHref={listHref}
		backLabel="Webhooks"
	>
		<WebhookForm
			scope={WebhookScope.GIT_REPOSITORY}
			scopeId={repo.id}
			cancelHref={listHref}
			onSaved={() => goto(resolve('/[namespace]/[name]/settings/webhooks', { namespace, name }))}
		/>
	</FormPage>
{:else}
	<Skeleton class="h-72 w-full rounded-lg" />
{/if}
