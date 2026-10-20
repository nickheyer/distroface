<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import AgentProviderFormPage from '$lib/components/git/agent-provider-form-page.svelte';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const listHref = $derived(
		resolve('/[namespace]/[name]/settings/agents/connectors', { namespace, name })
	);
</script>

<svelte:head>
	<title>New connector - {namespace}/{name}</title>
</svelte:head>

<AgentProviderFormPage
	scope={{ kind: 'repo', id: `${namespace}/${name}` }}
	{listHref}
	canManage={gitRepoStore.canAdmin}
/>
