<script lang="ts">
	import { page } from '$app/state';
	import AgentUsage from '$lib/components/git/agent-usage.svelte';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
</script>

<AgentUsage scope={{ kind: 'repo', id: `${namespace}/${name}` }} />
