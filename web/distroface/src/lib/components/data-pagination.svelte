<script lang="ts">
	import { Button } from '$lib/components/ui/button';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
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
		class="flex flex-wrap items-center justify-between gap-x-6 gap-y-2 text-xs text-muted-foreground {attached
			? 'border-t bg-muted/30 px-3 py-2'
			: 'pt-3'}"
	>
		<p class="whitespace-nowrap tabular-nums">
			{rangeStart}&ndash;{rangeEnd} of {pager.totalCount}
		</p>
		<div class="flex flex-wrap items-center gap-x-5 gap-y-2">
			<div class="hidden items-center gap-2 sm:flex">
				<span class="whitespace-nowrap">Rows per page</span>
				<Select
					type="single"
					value={String(pager.pageSize)}
					onValueChange={(v) => {
						if (v) apply(pager.setPageSize(Number(v)));
					}}
				>
					<SelectTrigger class="w-16 tabular-nums" size="sm" aria-label="Rows per page">
						{pager.pageSize}
					</SelectTrigger>
					<SelectContent>
						{#each pageSizeOptions as opt (opt)}
							<SelectItem value={String(opt)}>{opt}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</div>
			<span class="whitespace-nowrap tabular-nums">
				Page {pager.page} of {totalPages}
			</span>
			<div class="flex items-center gap-1">
				<Button
					variant="outline"
					size="icon-sm"
					class="hidden sm:inline-flex"
					disabled={!hasPrev}
					onclick={() => apply(pager.goTo(1))}
					aria-label="First page"
				>
					<ChevronsLeft />
				</Button>
				<Button
					variant="outline"
					size="icon-sm"
					disabled={!hasPrev}
					onclick={() => apply(pager.prev())}
					aria-label="Previous page"
				>
					<ChevronLeft />
				</Button>
				<Button
					variant="outline"
					size="icon-sm"
					disabled={!hasNext}
					onclick={() => apply(pager.next())}
					aria-label="Next page"
				>
					<ChevronRight />
				</Button>
				<Button
					variant="outline"
					size="icon-sm"
					class="hidden sm:inline-flex"
					disabled={!hasNext}
					onclick={() => apply(pager.goTo(totalPages))}
					aria-label="Last page"
				>
					<ChevronsRight />
				</Button>
			</div>
		</div>
	</div>
{/if}
