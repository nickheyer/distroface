<script lang="ts">
	import { resolve } from '$app/paths';
	import AgentProviderManager from '$lib/components/git/agent-provider-manager.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
</script>

<AgentProviderManager
	scope={{ kind: 'system', id: '' }}
	newHref={resolve('/admin/agents/connectors/new')}
	editHref={(id) => resolve('/admin/agents/connectors/[id]', { id })}
	canManage={authStore.canManageSettings}
	description="Inference endpoints available to every repository. Private network endpoints need the switch under Settings."
/>
