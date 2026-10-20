<script lang="ts">
	import { resolve } from '$app/paths';
	import AgentProviderFormPage from '$lib/components/git/agent-provider-form-page.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
</script>

<svelte:head>
	<title>New connector</title>
</svelte:head>

<AgentProviderFormPage
	scope={{ kind: 'system', id: '' }}
	listHref={resolve('/admin/agents/connectors')}
	canManage={authStore.canManageSettings}
/>
