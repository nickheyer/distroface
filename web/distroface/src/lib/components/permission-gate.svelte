<script lang="ts">
	import type { Snippet } from 'svelte';
	import { authStore } from '$lib/stores/auth.svelte';

	let {
		resource,
		action,
		objectId,
		allowed,
		children,
		fallback
	}: {
		resource?: string;
		action?: string;
		objectId?: string;
		allowed?: boolean;
		children: Snippet;
		fallback?: Snippet;
	} = $props();

	const permitted = $derived(
		allowed !== undefined
			? allowed
			: !!(resource && action && authStore.hasPermission(resource, action, objectId))
	);
</script>

{#if permitted}
	{@render children()}
{:else if fallback}
	{@render fallback()}
{/if}
