<script lang="ts">
	import { page } from '$app/state';
	import { onMount } from 'svelte';
	import RepoList from '$lib/components/repo-list.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import type { Repository } from '$lib/proto/distroface/v1/types_pb';

	const orgName = $derived(page.params.name ?? '');

	let repos = $state<Repository[]>([]);
	const pager = new Pager(20);
	const filter = new QueryFilter([
		{ key: 'name', label: 'Name' },
		{ key: 'description', label: 'Description' }
	]);
	let loading = $state(true);
	let loaded = $state(false);

	async function loadRepos() {
		loading = true;
		try {
			const resp = await rpcClient.repository.listRepositories({
				namespace: orgName,
				page: pager.request(filter.request())
			});
			repos = resp.repositories;
			pager.apply(resp.page);
		} catch {
			repos = [];
		} finally {
			loading = false;
			loaded = true;
		}
	}

	function filterChanged() {
		pager.reset();
		loadRepos();
	}

	onMount(loadRepos);
</script>

<div class="space-y-4">
	<div class="section-header">
		<h2 class="section-title">Repositories</h2>
		<div class="w-96">
			<QueryFilterBar {filter} placeholder="Search repositories..." onchange={filterChanged} />
		</div>
	</div>

	<RepoList
		{repos}
		totalCount={pager.totalCount}
		{loading}
		{loaded}
		showCount={false}
		page={pager.page}
		pageSize={pager.pageSize}
		onPrev={() => { if (pager.prev()) loadRepos(); }}
		onNext={() => { if (pager.next()) loadRepos(); }}
		emptyMessage={filter.active ? 'No matching repositories' : 'No repositories yet'}
		emptyDescription={filter.active
			? 'Try a different search.'
			: "Push images to this organization's namespace to create repositories."}
	/>
</div>
