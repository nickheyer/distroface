<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import AgentFormPage from '$lib/components/git/agent-form-page.svelte';
	import { authStore } from '$lib/stores/auth.svelte';

	const id = $derived(page.params.id ?? '');
</script>

<svelte:head>
	<title>Edit agent</title>
</svelte:head>

<AgentFormPage
	scope={{ kind: 'system', id: '' }}
	{id}
	listHref={resolve('/admin/agents/definitions')}
	canManage={authStore.canManageSettings}
/>
