<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import AgentProviderManager from '$lib/components/git/agent-provider-manager.svelte';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const params = $derived({ namespace, name });
</script>

<AgentProviderManager
	scope={{ kind: 'repo', id: `${namespace}/${name}` }}
	newHref={resolve('/[namespace]/[name]/settings/agents/connectors/new', params)}
	editHref={(id) =>
		resolve('/[namespace]/[name]/settings/agents/connectors/[id]', { ...params, id })}
	includeInherited
	canManage={gitRepoStore.canAdmin}
	description="Inference endpoints this repository can use. The nearest default answers for agents without one."
/>
