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

<div class={cn('rounded-lg border bg-card', className)}>
	{#if title}
		<div class="flex items-start gap-3 border-b px-5 py-4">
			{#if icon}
				{@const Icon = icon}
				<Icon class="mt-0.5 size-4 shrink-0 text-muted-foreground" />
			{/if}
			<div class="min-w-0">
				<h3 class="text-sm leading-5 font-semibold">{title}</h3>
				{#if description}
					<p class="mt-0.5 text-xs text-muted-foreground">{description}</p>
				{/if}
			</div>
		</div>
	{/if}
	<div class="p-5">
		{@render children()}
	</div>
	{#if footer}
		<div class="flex items-center justify-end gap-2 rounded-b-lg border-t bg-muted/30 px-5 py-3">
			{@render footer()}
		</div>
	{/if}
</div>
