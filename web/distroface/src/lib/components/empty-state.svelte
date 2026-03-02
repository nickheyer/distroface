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

<div class="flex flex-col items-center justify-center rounded-xl border border-dashed border-border/80 py-16 px-6 text-center">
	{#if icon}
		{@const Icon = icon}
		<div class="h-12 w-12 rounded-xl bg-muted/50 flex items-center justify-center mb-4">
			<Icon class="h-6 w-6 text-muted-foreground/50" />
		</div>
	{/if}
	<p class="text-sm font-medium text-muted-foreground">{message}</p>
	{#if description}
		<p class="text-[13px] text-muted-foreground/70 mt-1.5 max-w-sm">{description}</p>
	{/if}
	{#if actions}
		<div class="mt-5">
			{@render actions()}
		</div>
	{/if}
</div>
