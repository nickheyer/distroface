<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import { X } from '@lucide/svelte';
	import type { Snippet } from 'svelte';

	let {
		count,
		onClear,
		children
	}: {
		count: number;
		onClear: () => void;
		children: Snippet;
	} = $props();
</script>

{#if count > 0}
	<div class="page-enter fixed bottom-6 left-1/2 z-40 -translate-x-1/2">
		<div
			class="flex items-center gap-3 rounded-lg border bg-popover px-3 py-2 text-popover-foreground shadow-xl dark:ring-1 dark:ring-white/5"
		>
			<span class="text-sm font-medium whitespace-nowrap tabular-nums">
				{count} selected
			</span>
			<div class="h-4 w-px bg-border"></div>
			<div class="flex items-center gap-1">
				{@render children()}
			</div>
			<Button variant="ghost-muted" size="icon-sm" onclick={onClear} title="Clear selection">
				<X />
			</Button>
		</div>
	</div>
{/if}
