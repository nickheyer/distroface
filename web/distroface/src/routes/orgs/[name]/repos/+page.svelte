<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { onMount, getContext } from 'svelte';
	import { Plus } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import GitRepoList from '$lib/components/git/git-repo-list.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { GitRepoType } from '$lib/proto/distroface/v1/types_pb';
	import type { GitRepository } from '$lib/proto/distroface/v1/git_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');
	const newHref = $derived(`${resolve('/git/new')}?namespace=${encodeURIComponent(orgName)}`);

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
				{
					namespace: orgName,
					page: pager.request(req, 'updated_at desc'),
					type: typeFilter,
					topic
				},
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
		<div class="min-w-0">
			<h2 class="section-title">Git Repositories</h2>
			<p class="section-subtitle">Git repositories owned by {orgName}.</p>
		</div>
		{#if ctx.canCreateRepos}
			<Button class="shrink-0" href={newHref}>
				<Plus />
				New repository
			</Button>
		{/if}
	</div>

	<div class="flex flex-wrap items-center gap-2">
		<div class="w-full sm:max-w-md sm:flex-1">
			<QueryFilterBar {filter} placeholder="Search repositories" onchange={filterChanged} />
		</div>
		<Select
			type="single"
			value={String(typeFilter)}
			onValueChange={(v) => {
				if (v !== undefined) {
					typeFilter = Number(v) as GitRepoType;
					filterChanged();
				}
			}}
		>
			<SelectTrigger class="w-36" aria-label="Type">
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
		emptyMessage={filter.active || typeFilter !== GitRepoType.UNSPECIFIED
			? 'No git repositories found'
			: 'No git repositories yet'}
		emptyDescription={filter.active ? 'No results match the current filter' : undefined}
	>
		{#snippet emptyActions()}
			{#if !filter.active && typeFilter === GitRepoType.UNSPECIFIED && ctx.canCreateRepos}
				<Button variant="outline" href={newHref}>
					<Plus />
					New repository
				</Button>
			{/if}
		{/snippet}
	</GitRepoList>
</div>
