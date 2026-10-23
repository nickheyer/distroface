<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import { authStore } from '$lib/stores/auth.svelte';

	let { children } = $props();

	const tabs = [
		{ href: resolve('/admin/agents'), label: 'Settings' },
		{ href: resolve('/admin/agents/definitions'), label: 'Agents' },
		{ href: resolve('/admin/agents/connectors'), label: 'Connectors' },
		{ href: resolve('/admin/agents/usage'), label: 'Usage' }
	];

	// Create and edit pages bring their own header
	const nested = $derived(/\/agents\/(definitions|connectors)\/[^/]+\/?$/.test(page.url.pathname));

	onMount(() => {
		if (!authStore.hasPermission('settings', 'read')) goto(resolve('/admin'));
	});
</script>

{#if nested}
	{@render children?.()}
{:else}
	<FormPage
		level="section"
		title="Agents"
		description="Policy, agents, and connectors every organization and repository inherits."
		{tabs}
	>
		{@render children?.()}
	</FormPage>
{/if}
