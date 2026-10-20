<script lang="ts">
	import { page } from '$app/state';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import ActionsSecrets from '$lib/components/git/actions-secrets.svelte';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const scope = $derived({ kind: 'repo', id: `${namespace}/${name}` });
</script>

{#if !repo}
	<Skeleton class="h-72 w-full rounded-lg" />
{:else}
	<div class="space-y-6">
		<div>
			<h2 class="section-title">Secrets and variables</h2>
			<p class="section-subtitle">
				Values this repository's workflows read as secrets.NAME and vars.NAME. Organization values
				apply unless a repository one shares the name.
			</p>
		</div>
		{#key `${namespace}/${name}`}
			<ActionsSecrets {scope} canManage={gitRepoStore.canAdmin} />
		{/key}
	</div>
{/if}
