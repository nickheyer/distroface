<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import AgentProviderFormPage from '$lib/components/git/agent-provider-form-page.svelte';
	import { authStore } from '$lib/stores/auth.svelte';

	const id = $derived(page.params.id ?? '');
</script>

<svelte:head>
	<title>Edit connector</title>
</svelte:head>

<AgentProviderFormPage
	scope={{ kind: 'system', id: '' }}
	{id}
	listHref={resolve('/admin/agents/connectors')}
	canManage={authStore.canManageSettings}
/>
