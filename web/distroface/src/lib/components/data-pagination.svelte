<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import {
		Select, SelectContent, SelectItem, SelectTrigger
	} from '$lib/components/ui/select';
	import { ChevronLeft, ChevronRight, ChevronsLeft, ChevronsRight } from '@lucide/svelte';
	import type { Pager } from '$lib/pager.svelte';

	let {
		pager,
		onChange,
		pageSizeOptions = [10, 20, 50, 100],
		attached = false
	}: {
		pager: Pager;
		onChange: () => void;
		pageSizeOptions?: number[];
		attached?: boolean;
	} = $props();

	const totalPages = $derived(pager.totalPages);
	const hasPrev = $derived(pager.page > 1);
	const hasNext = $derived(pager.page < totalPages);
	const rangeStart = $derived((pager.page - 1) * pager.pageSize + 1);
	const rangeEnd = $derived(Math.min(pager.page * pager.pageSize, pager.totalCount));
	const visible = $derived(
		pager.totalCount > 0 && (totalPages > 1 || pager.totalCount > (pageSizeOptions[0] ?? 10))
	);

	function apply(changed: boolean) {
		if (changed) onChange();
	}

	// Deletions can strand the pager past the last page
	$effect(() => {
		if (pager.totalCount > 0 && pager.page > pager.totalPages) {
			apply(pager.goTo(pager.totalPages));
		}
	});
</script>

{#if visible}
	<div
		class="flex flex-wrap items-center justify-between gap-x-6 gap-y-2
			{attached ? 'border-t border-border/60 bg-muted/20 px-3 py-2' : 'pt-3'}"
	>
		<p class="text-[13px] text-muted-foreground tabular-nums whitespace-nowrap">
			{rangeStart}&ndash;{rangeEnd} of {pager.totalCount}
		</p>
		<div class="flex flex-wrap items-center gap-x-6 gap-y-2">
			<div class="hidden items-center gap-2 sm:flex">
				<span class="text-[13px] text-muted-foreground whitespace-nowrap">Rows per page</span>
				<Select
					type="single"
					value={String(pager.pageSize)}
					onValueChange={(v) => { if (v) apply(pager.setPageSize(Number(v))); }}
				>
					<SelectTrigger class="h-8 w-[4.5rem] text-[13px] tabular-nums" size="sm" aria-label="Rows per page">
						{pager.pageSize}
					</SelectTrigger>
					<SelectContent>
						{#each pageSizeOptions as opt (opt)}
							<SelectItem value={String(opt)}>{opt}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</div>
			<span class="text-[13px] text-muted-foreground tabular-nums whitespace-nowrap">
				Page {pager.page} of {totalPages}
			</span>
			<div class="flex items-center gap-1">
				<Button
					variant="outline" size="icon" class="hidden h-8 w-8 sm:inline-flex"
					disabled={!hasPrev} onclick={() => apply(pager.goTo(1))} aria-label="First page"
				>
					<ChevronsLeft class="h-4 w-4" />
				</Button>
				<Button
					variant="outline" size="icon" class="h-8 w-8"
					disabled={!hasPrev} onclick={() => apply(pager.prev())} aria-label="Previous page"
				>
					<ChevronLeft class="h-4 w-4" />
				</Button>
				<Button
					variant="outline" size="icon" class="h-8 w-8"
					disabled={!hasNext} onclick={() => apply(pager.next())} aria-label="Next page"
				>
					<ChevronRight class="h-4 w-4" />
				</Button>
				<Button
					variant="outline" size="icon" class="hidden h-8 w-8 sm:inline-flex"
					disabled={!hasNext} onclick={() => apply(pager.goTo(totalPages))} aria-label="Last page"
				>
					<ChevronsRight class="h-4 w-4" />
				</Button>
			</div>
		</div>
	</div>
{/if}
