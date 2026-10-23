<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import {
		REPO_AGENTS_CONTEXT_KEY,
		type RepoAgentsContext
	} from '$lib/components/git/repo-agents-context.svelte';
	import AgentFormPage from '$lib/components/git/agent-form-page.svelte';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';

	const ctx = getContext<RepoAgentsContext>(REPO_AGENTS_CONTEXT_KEY);
	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const id = $derived(page.params.id ?? '');
	const listHref = $derived(
		resolve('/[namespace]/[name]/settings/agents/definitions', { namespace, name })
	);
</script>

<svelte:head>
	<title>Edit agent - {namespace}/{name}</title>
</svelte:head>

<AgentFormPage
	scope={{ kind: 'repo', id: `${namespace}/${name}` }}
	{id}
	{listHref}
	canManage={gitRepoStore.canAdmin}
	orgId={ctx.orgId}
	allowCommands={ctx.overview?.allowCommands ?? true}
/>
