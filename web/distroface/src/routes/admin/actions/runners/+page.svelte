<script lang="ts">
	import RunnerManager from '$lib/components/git/runner-manager.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
</script>

<RunnerManager
	scope={{ kind: 'system', id: '' }}
	canManage={authStore.canManageSettings}
	description="Instance wide executors available to every repository."
/>
