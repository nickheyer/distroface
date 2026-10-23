<script lang="ts">
	import ActionsSettingsCard from '$lib/components/git/actions-settings-card.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { systemScope } from '$lib/settings-utils';
</script>

<ActionsSettingsCard scope={systemScope} canEdit={authStore.canUpdateSettings} />
