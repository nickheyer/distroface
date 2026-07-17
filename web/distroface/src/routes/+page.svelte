<script lang="ts">
	import { onMount } from 'svelte';
	import { Package } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import { pageToToken } from '$lib/utils';
	import RepoList from '$lib/components/repo-list.svelte';
	import PageHeader from '$lib/components/page-header.svelte';
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

<PageHeader
	title="Explore"
	subtitle={authStore.isAuthenticated
		? `Welcome back, ${authStore.user?.displayName || authStore.user?.username}`
		: 'Browse container images'}
	icon={Package}
>
	{#snippet actions()}
		{#if !repoLoading && repoTotalCount > 0}
			<span class="text-[12px] text-muted-foreground/60 tabular-nums">{repoTotalCount} repositor{repoTotalCount === 1 ? 'y' : 'ies'}</span>
		{/if}
	{/snippet}
</PageHeader>

<div class="space-y-6">
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
							docker push {portalStore.host(
								configStore.get('server.hostname', 'localhost:8080') as string
							)}/{portalStore.imageRef(
								portalStore.isPortal ? portalStore.orgName : (authStore.user?.username ?? ''),
								'myimage'
							)}:latest
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
