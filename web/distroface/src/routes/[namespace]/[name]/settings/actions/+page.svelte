<script lang="ts">
	import { page } from '$app/state';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import ActionsSecrets from '$lib/components/git/actions-secrets.svelte';
	import RunnerManager from '$lib/components/git/runner-manager.svelte';
	import { Info } from '@lucide/svelte';

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
			<h2 class="section-title">Actions</h2>
			<p class="section-subtitle">
				Secrets, variables, and runners available to this repository's workflows.
			</p>
		</div>

		{#if !configStore.actionsEnabled}
			<Alert>
				<Info />
				<AlertDescription
					>Workflows are turned off for this instance. Values saved here apply once an administrator
					enables Actions.</AlertDescription
				>
			</Alert>
		{:else if !repo.units?.actions}
			<Alert>
				<Info />
				<AlertDescription
					>The Actions unit is disabled for this repository. Turn it on under General, Features.</AlertDescription
				>
			</Alert>
		{/if}

		<ActionsSecrets {scope} />

		<section class="border-t pt-6">
			{#key `${namespace}/${name}`}
				<RunnerManager
					{scope}
					includeInherited
					canManage={gitRepoStore.canAdmin}
					description="Every runner that can pick up this repository's jobs. Organization and instance runners are managed where they were registered."
				/>
			{/key}
		</section>
	</div>
{/if}
