<script lang="ts">
	import type { Component } from 'svelte';

	let {
		label,
		value,
		icon,
		href
	}: {
		label: string;
		value: string | number;
		icon?: Component<{ class?: string }>;
		href?: string;
	} = $props();
</script>

{#snippet inner()}
	<div class="flex items-center gap-3">
		{#if icon}
			{@const Icon = icon}
			<div class="h-10 w-10 rounded-lg bg-primary/8 flex items-center justify-center shrink-0">
				<Icon class="h-5 w-5 text-primary/70" />
			</div>
		{/if}
		<div class="min-w-0">
			<p class="text-[11px] font-medium text-muted-foreground uppercase tracking-wider">{label}</p>
			<p class="text-2xl font-bold tracking-tight mt-0.5">{value}</p>
		</div>
	</div>
{/snippet}

{#if href}
	<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
	<a {href} class="block group">
		<div class="rounded-xl border border-border/60 bg-card p-4 transition-all hover:border-primary/20 hover:shadow-sm">
			{@render inner()}
		</div>
	</a>
{:else}
	<div class="rounded-xl border border-border/60 bg-card p-4">
		{@render inner()}
	</div>
{/if}
