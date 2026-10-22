<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import WebhookManager from '$lib/components/webhook-manager.svelte';
	import { WebhookScope } from '$lib/proto/distroface/v1/types_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const baseHref = $derived(resolve('/[namespace]/[name]/settings/webhooks', { namespace, name }));
</script>

{#if gitRepoStore.repo}
	<div class="space-y-6">
		{#key gitRepoStore.repo.id}
			<WebhookManager
				scope={WebhookScope.GIT_REPOSITORY}
				scopeId={gitRepoStore.repo.id}
				newHref={`${baseHref}/new`}
				editHref={(id) => `${baseHref}/${id}`}
				emptyDescription="Add a webhook to get notified of pushes, pull requests, issues, and releases."
				createDescription="Receive HTTP POST notifications for repository events."
			/>
		{/key}
	</div>
{:else}
	<Skeleton class="h-40 w-full rounded-lg" />
{/if}
