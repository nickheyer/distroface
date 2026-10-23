<script lang="ts">
	import type { Component } from 'svelte';

	let {
		label,
		value,
		icon,
		href,
		hint
	}: {
		label: string;
		value: string | number;
		icon?: Component<{ class?: string }>;
		href?: string;
		hint?: string;
	} = $props();
</script>

{#snippet inner()}
	<div class="flex items-center justify-between gap-2">
		<p class="text-xs font-medium text-muted-foreground">{label}</p>
		{#if icon}
			{@const Icon = icon}
			<Icon class="size-4 text-muted-foreground/70" />
		{/if}
	</div>
	<p class="mt-2 text-2xl leading-none font-semibold tracking-tight tabular-nums">{value}</p>
	{#if hint}
		<p class="mt-1.5 text-xs text-muted-foreground tabular-nums">{hint}</p>
	{/if}
{/snippet}

{#if href}
	<!-- eslint-disable svelte/no-navigation-without-resolve -->
	<a
		{href}
		class="block rounded-lg border bg-card p-4 transition-colors hover:border-border-strong hover:bg-muted/30"
	>
		{@render inner()}
	</a>
	<!-- eslint-enable svelte/no-navigation-without-resolve -->
{:else}
	<div class="rounded-lg border bg-card p-4">
		{@render inner()}
	</div>
{/if}
