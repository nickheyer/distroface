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
	<div class="fixed bottom-6 left-1/2 -translate-x-1/2 z-40 page-enter">
		<div
			class="flex items-center gap-3 rounded-xl border border-border bg-background/95 backdrop-blur px-4 py-2.5 shadow-lg"
		>
			<span class="text-sm font-medium tabular-nums whitespace-nowrap">
				{count} selected
			</span>
			<div class="h-4 w-px bg-border"></div>
			<div class="flex items-center gap-1.5">
				{@render children()}
			</div>
			<Button variant="ghost" size="icon" class="h-7 w-7" onclick={onClear} title="Clear selection">
				<X class="h-3.5 w-3.5" />
			</Button>
		</div>
	</div>
{/if}
