<script lang="ts" module>
	import type { Component } from 'svelte';

	export type SideNavItem = {
		href: string;
		label: string;
		icon?: Component<{ class?: string }>;
		danger?: boolean;
	};
</script>

<script lang="ts">
	import { cn } from '$lib/utils';

	let {
		items,
		isActive,
		onNavigate,
		class: className
	}: {
		items: SideNavItem[];
		isActive: (href: string) => boolean;
		onNavigate?: () => void;
		class?: string;
	} = $props();
</script>

<nav class={cn('shrink-0 md:w-52', className)} aria-label="Section navigation">
	<div
		class="flex gap-0.5 overflow-x-auto pb-2 md:sticky md:top-20 md:flex-col md:overflow-visible md:pb-0"
	>
		{#each items as item (item.href)}
			{@const active = isActive(item.href)}
			<!-- eslint-disable svelte/no-navigation-without-resolve -->
			<a
				href={item.href}
				aria-current={active ? 'page' : undefined}
				onclick={onNavigate}
				class={cn(
					'relative flex items-center gap-2.5 rounded-md px-3 py-1.5 text-sm whitespace-nowrap transition-colors',
					active
						? 'bg-muted font-medium text-foreground'
						: 'text-muted-foreground hover:bg-muted/60 hover:text-foreground',
					item.danger &&
						(active ? 'text-destructive' : 'text-destructive/80 hover:text-destructive')
				)}
			>
				{#if active}
					<span
						class={cn(
							'absolute inset-y-1.5 left-0 hidden w-0.5 rounded-full md:block',
							item.danger ? 'bg-destructive' : 'bg-primary'
						)}
						aria-hidden="true"
					></span>
				{/if}
				{#if item.icon}
					<item.icon class="size-4 shrink-0 opacity-80" />
				{/if}
				{item.label}
			</a>
			<!-- eslint-enable svelte/no-navigation-without-resolve -->
		{/each}
	</div>
</nav>
