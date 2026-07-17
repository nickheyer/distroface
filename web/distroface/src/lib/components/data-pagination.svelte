<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import { ChevronLeft, ChevronRight } from '@lucide/svelte';

	let {
		page,
		pageSize,
		totalCount,
		onPrev,
		onNext
	}: {
		page: number;
		pageSize: number;
		totalCount: number;
		onPrev: () => void;
		onNext: () => void;
	} = $props();

	const totalPages = $derived(Math.ceil(totalCount / pageSize));
	const hasNext = $derived(page < totalPages);
	const hasPrev = $derived(page > 1);
	const rangeStart = $derived((page - 1) * pageSize + 1);
	const rangeEnd = $derived(Math.min(page * pageSize, totalCount));
</script>

{#if totalCount > pageSize}
	<div class="flex items-center justify-between pt-2">
		<p class="text-[13px] text-muted-foreground tabular-nums">
			{rangeStart}-{rangeEnd} of {totalCount}
		</p>
		<div class="flex items-center gap-1.5">
			<span class="text-[13px] text-muted-foreground mr-1">
				Page {page} of {totalPages}
			</span>
			<Button variant="outline" size="icon" class="h-8 w-8" disabled={!hasPrev} onclick={onPrev}>
				<ChevronLeft class="h-4 w-4" />
			</Button>
			<Button variant="outline" size="icon" class="h-8 w-8" disabled={!hasNext} onclick={onNext}>
				<ChevronRight class="h-4 w-4" />
			</Button>
		</div>
	</div>
{/if}
