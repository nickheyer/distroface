<script lang="ts">
	import { resolve } from '$app/paths';
	import AgentManager from '$lib/components/git/agent-manager.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
</script>

<AgentManager
	scope={{ kind: 'system', id: '' }}
	newHref={resolve('/admin/agents/definitions/new')}
	editHref={(id) => resolve('/admin/agents/definitions/[id]', { id })}
	canManage={authStore.canManageSettings}
	description="Agents every repository may use. Each gets a bot account."
/>
