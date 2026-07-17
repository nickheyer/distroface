<script lang="ts">
	import { page } from '$app/state';
	import { onMount } from 'svelte';
	import RepoList from '$lib/components/repo-list.svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { pageToToken } from '$lib/utils';
	import type { Repository } from '$lib/proto/distroface/v1/types_pb';

	const orgName = $derived(page.params.name ?? '');

	let repos = $state<Repository[]>([]);
	let totalCount = $state(0);
	let currentPage = $state(1);
	const pageSize = 20;
	let loading = $state(true);
	let searchQuery = $state('');

	async function loadRepos() {
		loading = true;
		try {
			const resp = await rpcClient.repository.listRepositories({
				namespace: orgName,
				query: searchQuery.trim(),
				pageSize,
				pageToken: pageToToken(currentPage, pageSize)
			});
			repos = resp.repositories;
			totalCount = resp.totalCount;
		} catch {
			repos = [];
		} finally {
			loading = false;
		}
	}

	onMount(loadRepos);
</script>

<div class="space-y-4">
	<div class="section-header">
		<h2 class="section-title">Repositories</h2>
	</div>

	<RepoList
		{repos}
		{totalCount}
		{loading}
		page={currentPage}
		{pageSize}
		showSearch
		bind:searchQuery
		onSearch={() => { currentPage = 1; loadRepos(); }}
		onPageChange={(newPage) => { currentPage = newPage; loadRepos(); }}
		emptyMessage={searchQuery ? 'No matching repositories' : 'No repositories yet'}
		emptyDescription={searchQuery
			? 'Try a different search.'
			: "Push images to this organization's namespace to create repositories."}
	/>
</div>
