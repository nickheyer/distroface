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

<div class="mb-6 flex items-center gap-3 border-b pb-5">
	{#if icon}
		{@const Icon = icon}
		<Icon class="size-5 shrink-0 text-muted-foreground" />
	{/if}
	<div class="min-w-0 flex-1">
		<h1 class="text-xl leading-7 font-semibold tracking-tight">{title}</h1>
		{#if subtitle}
			<p class="mt-0.5 text-sm text-muted-foreground">{subtitle}</p>
		{/if}
	</div>
	{#if actions}
		<div class="flex shrink-0 items-center gap-2">
			{@render actions()}
		</div>
	{/if}
</div>
