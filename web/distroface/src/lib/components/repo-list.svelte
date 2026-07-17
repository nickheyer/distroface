<script lang="ts">
	import { Package } from '@lucide/svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import RepoCard from './repo-card.svelte';
	import EmptyState from './empty-state.svelte';
	import DataPagination from './data-pagination.svelte';
	import type { Repository } from '$lib/proto/distroface/v1/types_pb';
	import type { Snippet } from 'svelte';

	let {
		repos,
		totalCount,
		loading,
		loaded = undefined,
		page,
		pageSize = 20,
		showCount = true,
		onPrev,
		onNext,
		emptyMessage = 'No repositories yet',
		emptyDescription,
		emptyActions
	}: {
		repos: Repository[];
		totalCount: number;
		loading: boolean;
		loaded?: boolean;
		page: number;
		pageSize?: number;
		showCount?: boolean;
		onPrev: () => void;
		onNext: () => void;
		emptyMessage?: string;
		emptyDescription?: string;
		emptyActions?: Snippet;
	} = $props();

	const showSkeleton = $derived(loaded === undefined ? loading : !loaded);
</script>

<div class="space-y-4">
	{#if showSkeleton}
		<div class="space-y-2">
			{#each { length: 4 }, i (i)}
				<div class="rounded-xl border border-border/40 p-4">
					<div class="flex items-start gap-3.5">
						<Skeleton class="h-10 w-10 rounded-lg shrink-0" />
						<div class="flex-1 space-y-2.5">
							<Skeleton class="h-4 w-48" />
							<Skeleton class="h-3 w-72" />
							<div class="flex gap-4">
								<Skeleton class="h-3 w-16" />
								<Skeleton class="h-3 w-16" />
								<Skeleton class="h-3 w-20" />
							</div>
						</div>
					</div>
				</div>
			{/each}
		</div>
	{:else if repos.length === 0}
		<EmptyState icon={Package} message={emptyMessage} description={emptyDescription} actions={emptyActions} />
	{:else}
		{#if totalCount > 0 && showCount}
			<p class="text-[12px] text-muted-foreground/60 tabular-nums">{totalCount} repositor{totalCount === 1 ? 'y' : 'ies'}</p>
		{/if}

		<div class="space-y-2 transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			{#each repos as repo (repo.id)}
				<RepoCard {repo} />
			{/each}
		</div>

		<DataPagination
			{page}
			{pageSize}
			{totalCount}
			{onPrev}
			{onNext}
		/>
	{/if}
</div>
