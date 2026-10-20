<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { Component } from 'svelte';

	let {
		message,
		description,
		icon,
		actions
	}: {
		message: string;
		description?: string;
		icon?: Component<{ class?: string }>;
		actions?: Snippet;
	} = $props();
</script>

<div
	class="flex flex-col items-center justify-center rounded-lg border bg-card px-6 py-14 text-center"
>
	{#if icon}
		{@const Icon = icon}
		<Icon class="mb-3 size-6 text-muted-foreground/70" />
	{/if}
	<p class="text-sm font-semibold">{message}</p>
	{#if description}
		<p class="mt-1 max-w-sm text-sm text-muted-foreground">{description}</p>
	{/if}
	{#if actions}
		<div class="mt-4 flex items-center gap-2">
			{@render actions()}
		</div>
	{/if}
</div>
