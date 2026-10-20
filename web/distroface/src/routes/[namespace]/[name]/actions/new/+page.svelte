<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { ChevronLeft, Lock } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import WorkflowStarter from '$lib/components/git/workflow-starter.svelte';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);

	$effect(() => {
		if (namespace && name) gitRepoStore.load(namespace, name);
	});

	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.CODE) && !repo?.isArchived);
</script>

<svelte:head>
	<title>New workflow - {namespace}/{name}</title>
</svelte:head>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">New workflow</h2>
			<p class="section-subtitle">
				Pick a starting point, it opens in the editor under
				<span class="font-mono">.distroface/workflows</span>
			</p>
		</div>
		<Button
			variant="outline"
			class="shrink-0"
			href={resolve('/[namespace]/[name]/actions', { namespace, name })}
		>
			<ChevronLeft />Actions
		</Button>
	</div>

	{#if !repo}
		<Skeleton class="h-64 w-full rounded-lg" />
	{:else}
		{#if !canWrite}
			<Alert>
				<Lock />
				<AlertDescription>
					{repo.isArchived
						? 'This repository is archived and read only.'
						: 'You need write access to add workflows.'}
				</AlertDescription>
			</Alert>
		{/if}
		<WorkflowStarter {namespace} {name} defaultBranch={repo.defaultBranch} {canWrite} />
	{/if}
</div>
