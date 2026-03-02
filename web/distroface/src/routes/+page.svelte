<script lang="ts">
	import { onMount } from 'svelte';
	import { Package, Search } from '@lucide/svelte';
	import { Input } from '$lib/components/ui/input';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { pageToToken } from '$lib/utils';
	import RepoCard from '$lib/components/repo-card.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import type { Repository } from '$lib/proto/distroface/v1/types_pb';

	let repos = $state<Repository[]>([]);
	let repoLoading = $state(true);
	let repoTotalCount = $state(0);
	let repoPage = $state(1);
	const repoPageSize = 20;

	let searchQuery = $state('');
	let searchTimeout: ReturnType<typeof setTimeout> | undefined;

	async function loadRepos() {
		repoLoading = true;
		try {
			const response = await rpcClient.repository.listRepositories({
				pageSize: repoPageSize,
				pageToken: pageToToken(repoPage, repoPageSize),
				query: searchQuery
			});
			repos = response.repositories;
			repoTotalCount = response.totalCount;
		} catch {
			repos = [];
			repoTotalCount = 0;
		} finally {
			repoLoading = false;
		}
	}

	function handleSearchInput() {
		clearTimeout(searchTimeout);
		searchTimeout = setTimeout(() => { repoPage = 1; loadRepos(); }, 300);
	}

	onMount(loadRepos);
</script>

<div class="space-y-6">
	<div class="flex items-center gap-4 pb-2">
		<div class="h-12 w-12 rounded-xl bg-primary/10 flex items-center justify-center shrink-0">
			<Package class="h-6 w-6 text-primary" />
		</div>
		<div>
			<h1 class="text-2xl font-bold tracking-tight">Explore</h1>
			<p class="text-[13px] text-muted-foreground mt-0.5">
				{#if authStore.isAuthenticated}
					Welcome back, {authStore.user?.displayName || authStore.user?.username}
				{:else}
					Browse container images
				{/if}
			</p>
		</div>
	</div>

	<div class="relative max-w-xl">
		<Search class="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
		<Input
			placeholder="Search repositories..."
			class="pl-9 h-10"
			bind:value={searchQuery}
			oninput={handleSearchInput}
		/>
	</div>

	{#if !repoLoading && repoTotalCount > 0}
		<p class="text-[13px] text-muted-foreground">{repoTotalCount} repositor{repoTotalCount === 1 ? 'y' : 'ies'}</p>
	{/if}

	{#if repoLoading}
		<div class="space-y-2">
			{#each Array(5) as _}
				<Skeleton class="h-[68px] w-full rounded-xl" />
			{/each}
		</div>
	{:else if repos.length === 0}
		<EmptyState
			icon={Package}
			message={searchQuery ? 'No repositories found' : 'No repositories yet'}
			description={searchQuery ? `No results for "${searchQuery}"` : undefined}
		>
			{#snippet actions()}
				{#if !searchQuery}
					{#if authStore.isAuthenticated}
						<div class="text-center space-y-2">
							<p class="text-[13px] text-muted-foreground">Push your first image:</p>
							<code class="code-inline block text-xs">
								docker push {configStore.get('registryHost', 'localhost:8080')}/{authStore.user?.username}/myimage:latest
							</code>
						</div>
					{:else}
						<p class="text-[13px] text-muted-foreground">
							<a href="/login" class="text-primary underline-offset-4 hover:underline">Sign in</a> to push images
						</p>
					{/if}
				{/if}
			{/snippet}
		</EmptyState>
	{:else}
		<div class="space-y-2">
			{#each repos as repo}
				<RepoCard {repo} />
			{/each}
		</div>

		<DataPagination
			page={repoPage} pageSize={repoPageSize} totalCount={repoTotalCount}
			onPrev={() => { if (repoPage > 1) { repoPage--; loadRepos(); } }}
			onNext={() => { if (repoPage * repoPageSize < repoTotalCount) { repoPage++; loadRepos(); } }}
		/>
	{/if}
</div>
