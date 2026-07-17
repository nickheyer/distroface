<script lang="ts">
	import { onMount } from 'svelte';
	import { Package } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import RepoList from '$lib/components/repo-list.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import PageHeader from '$lib/components/page-header.svelte';
	import type { Repository } from '$lib/proto/distroface/v1/types_pb';
  import { resolve } from '$app/paths';

	let repos = $state<Repository[]>([]);
	let repoLoading = $state(true);
	let repoLoaded = $state(false);
	const repoPager = new Pager(20);
	const filter = new QueryFilter([
		{ key: 'name', label: 'Name' },
		{ key: 'namespace', label: 'Namespace' },
		{ key: 'description', label: 'Description' }
	]);

	async function loadRepos() {
		repoLoading = true;
		try {
			const response = await rpcClient.repository.listRepositories({
				page: repoPager.request(filter.request())
			});
			repos = response.repositories;
			repoPager.apply(response.page);
		} catch {
			repos = [];
			repoPager.apply();
		} finally {
			repoLoading = false;
			repoLoaded = true;
		}
	}

	function filterChanged() {
		repoPager.reset();
		loadRepos();
	}

	const emptyMessage = $derived(filter.active ? 'No repositories found' : 'No repositories yet');
	const emptyDescription = $derived(
		filter.active ? 'No results match the current filter' : undefined
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
		{#if !repoLoading && repoPager.totalCount > 0}
			<span class="text-[12px] text-muted-foreground/60 tabular-nums">{repoPager.totalCount} repositor{repoPager.totalCount === 1 ? 'y' : 'ies'}</span>
		{/if}
	{/snippet}
</PageHeader>

<div class="space-y-6">
	<div class="max-w-md">
		<QueryFilterBar {filter} placeholder="Search repositories..." onchange={filterChanged} />
	</div>

	<RepoList
		{repos}
		totalCount={repoPager.totalCount}
		loading={repoLoading}
		loaded={repoLoaded}
		showCount={false}
		page={repoPager.page}
		pageSize={repoPager.pageSize}
		onPrev={() => { if (repoPager.prev()) loadRepos(); }}
		onNext={() => { if (repoPager.next()) loadRepos(); }}
		{emptyMessage}
		{emptyDescription}
	>
		{#snippet emptyActions()}
			{#if !filter.active}
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
