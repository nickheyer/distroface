<script lang="ts">
	import type { Snippet } from 'svelte';
	import type { Component } from 'svelte';

	let {
		title,
		subtitle,
		icon,
		actions
	}: {
		title: string;
		subtitle?: string;
		icon?: Component<{ class?: string }>;
		actions?: Snippet;
	} = $props();
</script>

<div class="flex items-center gap-4 pb-6">
	{#if icon}
		{@const Icon = icon}
		<div
			class="h-12 w-12 rounded-xl bg-linear-to-br from-primary/15 to-primary/5 border border-primary/10 flex items-center justify-center shrink-0"
		>
			<Icon class="h-6 w-6 text-primary" />
		</div>
	{/if}
	<div class="flex-1 min-w-0">
		<h1 class="text-2xl font-bold tracking-tight">{title}</h1>
		{#if subtitle}
			<p class="text-sm text-muted-foreground mt-0.5">{subtitle}</p>
		{/if}
	</div>
	{#if actions}
		{@render actions()}
	{/if}
</div>
