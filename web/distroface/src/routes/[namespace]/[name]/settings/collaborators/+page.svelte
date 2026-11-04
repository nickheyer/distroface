<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import CollaboratorTable from '$lib/components/git/collaborator-table.svelte';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const addHref = $derived(
		resolve('/[namespace]/[name]/settings/collaborators/new', { namespace, name })
	);
</script>

{#if gitRepoStore.repo}
	{#key gitRepoStore.repo.fullName}
		<CollaboratorTable {namespace} {name} {addHref} />
	{/key}
{:else}
	<Skeleton class="h-40 w-full rounded-lg" />
{/if}
