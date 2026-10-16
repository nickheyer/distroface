<script lang="ts">
	import { GitBranch } from '@lucide/svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import GitRepoCard from './git-repo-card.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import type { Pager } from '$lib/pager.svelte';
	import type { GitRepository } from '$lib/proto/distroface/v1/git_pb';
	import type { Snippet } from 'svelte';

	let {
		repos,
		pager,
		onChange,
		loading,
		loaded = undefined,
		showNamespace = true,
		emptyMessage = 'No repositories yet',
		emptyDescription,
		emptyActions
	}: {
		repos: GitRepository[];
		pager: Pager;
		onChange: () => void;
		loading: boolean;
		loaded?: boolean;
		showNamespace?: boolean;
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
		<EmptyState icon={GitBranch} message={emptyMessage} description={emptyDescription} actions={emptyActions} />
	{:else}
		<div class="space-y-2 transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			{#each repos as repo (repo.id)}
				<GitRepoCard {repo} {showNamespace} />
			{/each}
		</div>

		<DataPagination {pager} {onChange} />
	{/if}
</div>
