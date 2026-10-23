<script lang="ts">
	import AgentsSettingsCard from '$lib/components/git/agents-settings-card.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { systemScope } from '$lib/settings-utils';
</script>

<AgentsSettingsCard scope={systemScope} canEdit={authStore.canUpdateSettings} />
