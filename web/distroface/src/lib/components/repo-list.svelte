<script lang="ts">
	import { Search, Package } from '@lucide/svelte';
	import { Input } from '$lib/components/ui/input';
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
		page,
		pageSize = 20,
		showSearch = false,
		searchQuery = $bindable(''),
		onSearch,
		onPageChange,
		emptyMessage = 'No repositories yet',
		emptyDescription,
		emptyActions
	}: {
		repos: Repository[];
		totalCount: number;
		loading: boolean;
		page: number;
		pageSize?: number;
		showSearch?: boolean;
		searchQuery?: string;
		onSearch?: () => void;
		onPageChange: (newPage: number) => void;
		emptyMessage?: string;
		emptyDescription?: string;
		emptyActions?: Snippet;
	} = $props();

	let searchTimeout: ReturnType<typeof setTimeout> | undefined;

	function handleSearchInput() {
		clearTimeout(searchTimeout);
		searchTimeout = setTimeout(() => {
			onSearch?.();
		}, 300);
	}
</script>

<div class="space-y-4">
	{#if showSearch}
		<div class="relative max-w-md">
			<Search class="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground/50" />
			<Input
				placeholder="Search repositories..."
				class="pl-9 h-9 bg-muted/30 border-border/50 focus-visible:bg-background"
				bind:value={searchQuery}
				oninput={handleSearchInput}
			/>
		</div>
	{/if}

	{#if loading}
		<div class="space-y-2">
			{#each Array(4) as _}
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
		{#if totalCount > 0 && !showSearch}
			<p class="text-[12px] text-muted-foreground/60 tabular-nums">{totalCount} repositor{totalCount === 1 ? 'y' : 'ies'}</p>
		{/if}

		<div class="space-y-2">
			{#each repos as repo}
				<RepoCard {repo} />
			{/each}
		</div>

		<DataPagination
			{page}
			{pageSize}
			{totalCount}
			onPrev={() => onPageChange(page - 1)}
			onNext={() => onPageChange(page + 1)}
		/>
	{/if}
</div>
