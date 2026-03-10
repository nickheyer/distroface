<script lang="ts">
	import { onMount } from 'svelte';
	import { Package } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { pageToToken } from '$lib/utils';
	import RepoList from '$lib/components/repo-list.svelte';
	import type { Repository } from '$lib/proto/distroface/v1/types_pb';
  import { resolve } from '$app/paths';

	let repos = $state<Repository[]>([]);
	let repoLoading = $state(true);
	let repoTotalCount = $state(0);
	let repoPage = $state(1);
	const repoPageSize = 20;
	let searchQuery = $state('');

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

	function handleSearch() {
		repoPage = 1;
		loadRepos();
	}

	function handlePageChange(newPage: number) {
		repoPage = newPage;
		loadRepos();
	}

	const emptyMessage = $derived(searchQuery ? 'No repositories found' : 'No repositories yet');
	const emptyDescription = $derived(
		searchQuery ? `No results for "${searchQuery}"` : undefined
	);

	onMount(loadRepos);
</script>

<div class="space-y-6">
	<div class="flex items-center gap-4">
		<div class="h-12 w-12 rounded-xl bg-linear-to-br from-primary/15 to-primary/5 flex items-center justify-center shrink-0 border border-primary/10">
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
		{#if !repoLoading && repoTotalCount > 0}
			<div class="ml-auto">
				<span class="text-[12px] text-muted-foreground/60 tabular-nums">{repoTotalCount} repositor{repoTotalCount === 1 ? 'y' : 'ies'}</span>
			</div>
		{/if}
	</div>

	<RepoList
		{repos}
		totalCount={repoTotalCount}
		loading={repoLoading}
		page={repoPage}
		pageSize={repoPageSize}
		showSearch={true}
		bind:searchQuery
		onSearch={handleSearch}
		onPageChange={handlePageChange}
		{emptyMessage}
		{emptyDescription}
	>
		{#snippet emptyActions()}
			{#if !searchQuery}
				{#if authStore.isAuthenticated}
					<div class="text-center space-y-2">
						<p class="text-[13px] text-muted-foreground">Push your first image:</p>
						<code class="code-inline block text-xs">
							docker push {configStore.get('server.hostname', 'localhost:8080')}/{authStore.user?.username}/myimage:latest
						</code>
					</div>
				{:else}
					<p class="text-[13px] text-muted-foreground">
						<a href={resolve("/login")} class="text-primary underline-offset-4 hover:underline">Sign in</a> to push images
					</p>
				{/if}
			{/if}
		{/snippet}
	</RepoList>
</div>
