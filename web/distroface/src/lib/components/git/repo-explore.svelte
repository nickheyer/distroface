<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { GitBranch, Plus } from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { authStore } from '$lib/stores/auth.svelte';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { MatchKind } from '$lib/proto/distroface/v1/pagination_pb';
	import { Button } from '$lib/components/ui/button';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import PageHeader from '$lib/components/page-header.svelte';
	import GitRepoList from './git-repo-list.svelte';
	import { GitRepoType } from '$lib/proto/distroface/v1/types_pb';
	import type { GitRepository } from '$lib/proto/distroface/v1/git_pb';

	let repos = $state<GitRepository[]>([]);
	let loading = $state(true);
	let loaded = $state(false);
	let browseDenied = $state(false);
	const pager = new Pager(20);
	const filter = new QueryFilter([
		{ key: 'name', label: 'Name' },
		{ key: 'namespace', label: 'Namespace' },
		{ key: 'description', label: 'Description' },
		{ key: 'topic', label: 'Topic' }
	]);
	let typeFilter = $state<GitRepoType>(GitRepoType.UNSPECIFIED);
	let orderBy = $state('updated_at desc');

	const typeFilterOptions = [
		{ value: GitRepoType.UNSPECIFIED, label: 'All types' },
		{ value: GitRepoType.STANDARD, label: 'Standard' },
		{ value: GitRepoType.MIRROR, label: 'Mirrors' },
		{ value: GitRepoType.FORK, label: 'Forks' },
		{ value: GitRepoType.TEMPLATE, label: 'Templates' }
	];
	const orderOptions = [
		{ value: 'updated_at desc', label: 'Recently updated' },
		{ value: 'created_at desc', label: 'Newest' },
		{ value: 'stars desc', label: 'Most stars' },
		{ value: 'name asc', label: 'Name' }
	];

	async function loadRepos() {
		loading = true;
		browseDenied = false;
		const req = filter.request();
		// Topic filters ride the dedicated request field
		const topic = req.filters.find((f) => f.field === 'topic')?.value ?? '';
		req.filters = req.filters.filter((f) => f.field !== 'topic');
		try {
			const resp = await rpcClient.git.listGitRepositories(
				{ page: pager.request(req, orderBy), type: typeFilter, topic },
				authStore.isAuthenticated ? undefined : silentCallOptions
			);
			repos = resp.repositories;
			pager.apply(resp.page);
		} catch (err) {
			repos = [];
			pager.apply();
			browseDenied =
				!authStore.isAuthenticated &&
				err instanceof ConnectError &&
				err.code === Code.PermissionDenied;
		} finally {
			loading = false;
			loaded = true;
		}
	}

	function filterChanged() {
		pager.reset();
		loadRepos();
	}

	const emptyMessage = $derived(
		browseDenied
			? 'Sign in to browse git repositories'
			: filter.active || typeFilter !== GitRepoType.UNSPECIFIED
				? 'No git repositories found'
				: 'No git repositories yet'
	);
	const emptyDescription = $derived(
		browseDenied
			? 'Anonymous browsing is not permitted here'
			: filter.active
				? 'No results match the current filter'
				: authStore.isAuthenticated
					? 'Create one here, then push to the clone url it shows you.'
					: undefined
	);

	const newHref = resolve('/git/new');

	onMount(() => {
		const params = page.url.searchParams;
		const topic = params.get('topic');
		if (topic) filter.add('topic', MatchKind.EQUALS, topic);
		loadRepos();
		if (params.get('new')) {
			const ns = params.get('namespace');
			goto(resolve(`/git/new${ns ? `?namespace=${encodeURIComponent(ns)}` : ''}` as `/${string}`), {
				replaceState: true
			});
		}
		mirrorSyncStore.ensure();
	});

	let syncSeqSeen = 0;
	$effect(() => {
		const seq = mirrorSyncStore.finishedSeq;
		if (seq === syncSeqSeen) return;
		syncSeqSeen = seq;
		const kind = mirrorSyncStore.lastFinished?.kind;
		if (kind === 'git' || kind === 'import') loadRepos();
	});
</script>

<PageHeader title="Git" subtitle="Source repositories served over https and ssh" icon={GitBranch}>
	{#snippet actions()}
		{#if !loading && pager.totalCount > 0}
			<span class="text-xs text-muted-foreground tabular-nums"
				>{pager.totalCount} repositor{pager.totalCount === 1 ? 'y' : 'ies'}</span
			>
		{/if}
		{#if authStore.isAuthenticated}
			<Button href={newHref}>
				<Plus />
				New repository
			</Button>
		{/if}
	{/snippet}
</PageHeader>

<div class="space-y-4">
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
				{typeFilterOptions.find((o) => o.value === typeFilter)?.label}
			</SelectTrigger>
			<SelectContent>
				{#each typeFilterOptions as o (o.value)}
					<SelectItem value={String(o.value)}>{o.label}</SelectItem>
				{/each}
			</SelectContent>
		</Select>
		<Select
			type="single"
			value={orderBy}
			onValueChange={(v) => {
				if (v) {
					orderBy = v;
					filterChanged();
				}
			}}
		>
			<SelectTrigger class="w-44" aria-label="Sort">
				{orderOptions.find((o) => o.value === orderBy)?.label}
			</SelectTrigger>
			<SelectContent>
				{#each orderOptions as o (o.value)}
					<SelectItem value={o.value}>{o.label}</SelectItem>
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
		{emptyMessage}
		{emptyDescription}
	>
		{#snippet emptyActions()}
			{#if !filter.active && !browseDenied && typeFilter === GitRepoType.UNSPECIFIED}
				{#if authStore.isAuthenticated}
					<Button variant="outline" href={newHref}>
						<Plus />New repository
					</Button>
				{:else}
					<p class="text-sm text-muted-foreground">
						<a href={resolve('/login')} class="link">Sign in</a> to create repositories
					</p>
				{/if}
			{/if}
		{/snippet}
	</GitRepoList>
</div>
