<script lang="ts">
	import { resolve } from '$app/paths';
	import AgentFormPage from '$lib/components/git/agent-form-page.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
</script>

<svelte:head>
	<title>New agent</title>
</svelte:head>

<AgentFormPage
	scope={{ kind: 'system', id: '' }}
	listHref={resolve('/admin/agents/definitions')}
	canManage={authStore.canManageSettings}
/>
