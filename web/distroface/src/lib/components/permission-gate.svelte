<script lang="ts">
	import type { Snippet } from 'svelte';

	let {
		allowed,
		children,
		fallback
	}: {
		allowed: boolean;
		children: Snippet;
		fallback?: Snippet;
	} = $props();
</script>

{#if allowed}
	{@render children()}
{:else if fallback}
	{@render fallback()}
{/if}
