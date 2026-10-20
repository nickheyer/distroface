<script lang="ts">
	import { onMount } from 'svelte';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { Package, Plus } from '$lib/icons';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { Button } from '$lib/components/ui/button';
	import RepoList from '$lib/components/repo-list.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import PageHeader from '$lib/components/page-header.svelte';
	import type { Repository } from '$lib/proto/distroface/v1/types_pb';
	import { resolve } from '$app/paths';

	let repos = $state<Repository[]>([]);
	let repoLoading = $state(true);
	let repoLoaded = $state(false);
	// Anonymous role lacks the read grant, the list stays quiet
	let browseDenied = $state(false);
	const repoPager = new Pager(20);
	const filter = new QueryFilter([
		{ key: 'name', label: 'Name' },
		{ key: 'namespace', label: 'Namespace' },
		{ key: 'description', label: 'Description' }
	]);
	const newHref = resolve('/images/new');

	async function loadRepos() {
		repoLoading = true;
		browseDenied = false;
		try {
			const response = await rpcClient.repository.listRepositories(
				{ page: repoPager.request(filter.request()) },
				authStore.isAuthenticated ? undefined : silentCallOptions
			);
			repos = response.repositories;
			repoPager.apply(response.page);
		} catch (err) {
			repos = [];
			repoPager.apply();
			browseDenied =
				!authStore.isAuthenticated &&
				err instanceof ConnectError &&
				err.code === Code.PermissionDenied;
		} finally {
			repoLoading = false;
			repoLoaded = true;
		}
	}

	function filterChanged() {
		repoPager.reset();
		loadRepos();
	}

	const emptyMessage = $derived(
		browseDenied
			? 'Sign in to browse images'
			: filter.active
				? 'No image repositories found'
				: 'No image repositories yet'
	);
	const emptyDescription = $derived(
		browseDenied
			? 'Anonymous browsing is not permitted here'
			: filter.active
				? 'No results match the current filter'
				: undefined
	);

	onMount(loadRepos);

	// Finished syncs refresh the list live
	let syncSeqSeen = 0;
	$effect(() => {
		const seq = mirrorSyncStore.finishedSeq;
		if (seq === syncSeqSeen) return;
		syncSeqSeen = seq;
		if (mirrorSyncStore.lastFinished?.kind === 'image') loadRepos();
	});
</script>

<PageHeader title="Images" subtitle="Container image repositories on this instance" icon={Package}>
	{#snippet actions()}
		{#if !repoLoading && repoPager.totalCount > 0}
			<span class="text-xs text-muted-foreground tabular-nums">
				{repoPager.totalCount} repositor{repoPager.totalCount === 1 ? 'y' : 'ies'}
			</span>
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
	<div class="max-w-md">
		<QueryFilterBar {filter} placeholder="Search repositories" onchange={filterChanged} />
	</div>

	<RepoList
		{repos}
		pager={repoPager}
		onChange={loadRepos}
		loading={repoLoading}
		loaded={repoLoaded}
		{emptyMessage}
		{emptyDescription}
	>
		{#snippet emptyActions()}
			{#if !filter.active && !browseDenied}
				{#if authStore.isAuthenticated}
					<div class="space-y-3 text-center">
						<Button variant="outline" href={newHref}>
							<Plus />New repository
						</Button>
						<p class="text-sm text-muted-foreground">Or push your first image</p>
						<code class="code-inline block">
							docker push {portalStore.host(configStore.publicHostname)}/{portalStore.imageRef(
								portalStore.isPortal ? portalStore.orgName : (authStore.user?.username ?? ''),
								'myimage'
							)}:latest
						</code>
					</div>
				{:else}
					<p class="text-sm text-muted-foreground">
						<a href={resolve('/login')} class="link">Sign in</a> to push images
					</p>
				{/if}
			{/if}
		{/snippet}
	</RepoList>
</div>
