<script lang="ts">
	import { page } from '$app/state';
	import RunnerManager from '$lib/components/git/runner-manager.svelte';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
</script>

<RunnerManager
	scope={{ kind: 'repo', id: `${namespace}/${name}` }}
	includeInherited
	canManage={gitRepoStore.canAdmin}
	description="Every runner that can pick up this repository's jobs."
/>
