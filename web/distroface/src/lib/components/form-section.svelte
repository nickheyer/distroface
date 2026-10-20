<script lang="ts">
	import type { Snippet } from 'svelte';
	import { cn } from '$lib/utils';

	let {
		title,
		description,
		children,
		footer,
		class: className
	}: {
		title: string;
		description?: string;
		children: Snippet;
		footer?: Snippet;
		class?: string;
	} = $props();
</script>

<section class={cn('form-split', className)}>
	<div class="min-w-0">
		<h3 class="text-base leading-6 font-semibold">{title}</h3>
		{#if description}
			<p class="mt-1 text-sm text-muted-foreground">{description}</p>
		{/if}
	</div>
	<div class="form-fields min-w-0">
		{@render children()}
		{#if footer}
			<div class="flex flex-wrap items-center gap-2 pt-2">
				{@render footer()}
			</div>
		{/if}
	</div>
</section>
