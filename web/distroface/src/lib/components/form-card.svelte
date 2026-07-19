<script lang="ts">
	import type { Snippet, Component } from 'svelte';
	import { cn } from '$lib/utils';

	let {
		title,
		description,
		icon,
		children,
		footer,
		class: className
	}: {
		title?: string;
		description?: string;
		icon?: Component<{ class?: string }>;
		children: Snippet;
		footer?: Snippet;
		class?: string;
	} = $props();
</script>

<div class={cn('rounded-xl border border-border/60 bg-card overflow-hidden', className)}>
	{#if title}
		<div class="flex items-center gap-3 px-6 py-4 border-b border-border/40 bg-muted/20">
			{#if icon}
				{@const Icon = icon}
				<div class="h-8 w-8 rounded-lg bg-primary/10 flex items-center justify-center shrink-0">
					<Icon class="h-4 w-4 text-primary" />
				</div>
			{/if}
			<div class="min-w-0">
				<h3 class="text-sm font-semibold">{title}</h3>
				{#if description}
					<p class="text-xs text-muted-foreground mt-0.5">{description}</p>
				{/if}
			</div>
		</div>
	{/if}
	<div class="p-6">
		{@render children()}
	</div>
	{#if footer}
		<div class="flex items-center justify-end gap-3 px-6 py-4 border-t border-border/40 bg-muted/20">
			{@render footer()}
		</div>
	{/if}
</div>
