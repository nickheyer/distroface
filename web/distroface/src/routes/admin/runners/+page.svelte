<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import RunnerManager from '$lib/components/git/runner-manager.svelte';
	import { authStore } from '$lib/stores/auth.svelte';

	onMount(() => {
		if (!authStore.hasPermission('settings', 'read')) goto(resolve('/admin'));
	});
</script>

<RunnerManager
	scope={{ kind: 'system', id: '' }}
	cliScope="system"
	canManage={authStore.canManageSettings}
	description="Instance wide executors available to every repository."
/>
