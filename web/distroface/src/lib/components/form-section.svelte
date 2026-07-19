<script lang="ts">
	import type { Snippet } from 'svelte';
	import { cn } from '$lib/utils';

	let {
		title,
		description,
		children,
		actions,
		class: className
	}: {
		title?: string;
		description?: string;
		children: Snippet;
		actions?: Snippet;
		class?: string;
	} = $props();
</script>

<section class={cn('space-y-3', className)}>
	{#if title}
		<div class="pb-2 border-b border-border/40">
			<div class="flex items-center justify-between gap-4">
				<div>
					<h3 class="text-sm font-semibold tracking-tight">{title}</h3>
					{#if description}
						<p class="text-[13px] text-muted-foreground mt-0.5">{description}</p>
					{/if}
				</div>
				{#if actions}
					<div class="shrink-0">
						{@render actions()}
					</div>
				{/if}
			</div>
		</div>
	{/if}
	{@render children()}
</section>
