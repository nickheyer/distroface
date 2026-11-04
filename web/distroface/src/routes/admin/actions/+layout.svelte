<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import { authStore } from '$lib/stores/auth.svelte';

	let { children } = $props();

	const tabs = [
		{ href: resolve('/admin/actions'), label: 'Settings' },
		{ href: resolve('/admin/actions/runners'), label: 'Runners' }
	];

	onMount(() => {
		if (!authStore.hasPermission('settings', 'read')) goto(resolve('/admin'));
	});
</script>

<FormPage
	level="section"
	title="Actions"
	description="Workflow policy and the runners every organization and repository inherits."
	{tabs}
>
	{@render children?.()}
</FormPage>
