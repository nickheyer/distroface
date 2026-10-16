<script lang="ts">
	import { page } from '$app/state';
	import { onMount } from 'svelte';
	import { Plus } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import {
		Select, SelectContent, SelectItem, SelectTrigger
	} from '$lib/components/ui/select';
	import GitRepoList from '$lib/components/git/git-repo-list.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { GitRepoType } from '$lib/proto/distroface/v1/types_pb';
	import type { GitRepository } from '$lib/proto/distroface/v1/git_pb';

	const orgName = $derived(page.params.name ?? '');

	let repos = $state<GitRepository[]>([]);
	let loading = $state(true);
	let loaded = $state(false);
	const pager = new Pager(20);
	const filter = new QueryFilter([
		{ key: 'name', label: 'Name' },
		{ key: 'description', label: 'Description' },
		{ key: 'topic', label: 'Topic' }
	]);
	let typeFilter = $state<GitRepoType>(GitRepoType.UNSPECIFIED);

	const typeOptions = [
		{ value: GitRepoType.UNSPECIFIED, label: 'All types' },
		{ value: GitRepoType.STANDARD, label: 'Standard' },
		{ value: GitRepoType.MIRROR, label: 'Mirrors' },
		{ value: GitRepoType.FORK, label: 'Forks' },
		{ value: GitRepoType.TEMPLATE, label: 'Templates' }
	];

	async function loadRepos() {
		loading = true;
		const req = filter.request();
		const topic = req.filters.find((f) => f.field === 'topic')?.value ?? '';
		req.filters = req.filters.filter((f) => f.field !== 'topic');
		try {
			const resp = await rpcClient.git.listGitRepositories(
				{ namespace: orgName, page: pager.request(req, 'updated_at desc'), type: typeFilter, topic },
				silentCallOptions
			);
			repos = resp.repositories;
			pager.apply(resp.page);
		} catch {
			repos = [];
			pager.apply();
		} finally {
			loading = false;
			loaded = true;
		}
	}

	function filterChanged() {
		pager.reset();
		loadRepos();
	}

	onMount(() => {
		loadRepos();
		mirrorSyncStore.ensure();
	});

	let syncSeqSeen = 0;
	$effect(() => {
		const seq = mirrorSyncStore.finishedSeq;
		if (seq === syncSeqSeen) return;
		syncSeqSeen = seq;
		const ev = mirrorSyncStore.lastFinished;
		if ((ev?.kind === 'git' || ev?.kind === 'import') && ev.namespace === orgName) loadRepos();
	});
</script>

<div class="space-y-4">
	<div class="section-header">
		<h2 class="section-title">Git</h2>
		{#if authStore.isAuthenticated}
			<Button size="sm" href="/git?new=1&namespace={encodeURIComponent(orgName)}">
				<Plus class="h-4 w-4 mr-1.5" />New Repository
			</Button>
		{/if}
	</div>

	<div class="flex flex-wrap items-center gap-2">
		<div class="w-full max-w-md">
			<QueryFilterBar {filter} placeholder="Search repositories..." onchange={filterChanged} />
		</div>
		<Select type="single" value={String(typeFilter)} onValueChange={(v) => { if (v !== undefined) { typeFilter = Number(v) as GitRepoType; filterChanged(); } }}>
			<SelectTrigger class="h-9 w-36 text-[13px]" aria-label="Type">
				{typeOptions.find((o) => o.value === typeFilter)?.label}
			</SelectTrigger>
			<SelectContent>
				{#each typeOptions as o (o.value)}
					<SelectItem value={String(o.value)}>{o.label}</SelectItem>
				{/each}
			</SelectContent>
		</Select>
	</div>

	<GitRepoList
		{repos}
		{pager}
		onChange={loadRepos}
		{loading}
		{loaded}
		showNamespace={false}
		emptyMessage={filter.active || typeFilter !== GitRepoType.UNSPECIFIED ? 'No git repositories found' : 'No git repositories yet'}
		emptyDescription={filter.active ? 'No results match the current filter' : undefined}
	/>
</div>
