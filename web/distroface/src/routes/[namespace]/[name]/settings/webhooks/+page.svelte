<script lang="ts">
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import WebhookManager from '$lib/components/webhook-manager.svelte';
	import { WebhookScope } from '$lib/proto/distroface/v1/types_pb';
</script>

{#if gitRepoStore.repo}
	<div class="space-y-4">
		<div>
			<h2 class="section-title">Webhooks</h2>
			<p class="section-subtitle">HTTP callbacks for pushes, pull requests, issues, and releases.</p>
		</div>
		{#key gitRepoStore.repo.id}
			<WebhookManager
				scope={WebhookScope.GIT_REPOSITORY}
				scopeId={gitRepoStore.repo.id}
				emptyDescription="Add a webhook to get notified of pushes, pull requests, issues, and releases."
				createDescription="Receive HTTP POST notifications for repository events."
			/>
		{/key}
	</div>
{:else}
	<Skeleton class="h-40 w-full rounded-xl" />
{/if}
