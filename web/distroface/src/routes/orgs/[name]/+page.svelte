<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { onMount, getContext } from 'svelte';
	import { Plus } from '$lib/icons';
	import RepoList from '$lib/components/repo-list.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import { Button } from '$lib/components/ui/button';
	import { rpcClient } from '$lib/api/rpc-client';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';
	import type { Repository } from '$lib/proto/distroface/v1/types_pb';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');
	const newHref = $derived(`${resolve('/images/new')}?namespace=${encodeURIComponent(orgName)}`);

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
		<div class="min-w-0">
			<h2 class="section-title">Image repositories</h2>
			<p class="section-subtitle">Container images pushed under {orgName}.</p>
		</div>
		{#if ctx.canAdmin}
			<Button class="shrink-0" href={newHref}>
				<Plus />
				New repository
			</Button>
		{/if}
	</div>

	<div class="max-w-md">
		<QueryFilterBar {filter} placeholder="Search repositories" onchange={filterChanged} />
	</div>

	<RepoList
		{repos}
		{pager}
		onChange={loadRepos}
		{loading}
		{loaded}
		emptyMessage={filter.active ? 'No image repositories found' : 'No image repositories yet'}
		emptyDescription={filter.active ? 'No results match the current filter' : undefined}
	>
		{#snippet emptyActions()}
			{#if !filter.active}
				<div class="space-y-3 text-center">
					{#if ctx.canAdmin}
						<Button variant="outline" href={newHref}>
							<Plus />
							New image repository
						</Button>
					{/if}
					<p class="text-sm text-muted-foreground">Or push your first image</p>
					<code class="code-inline block">
						docker push {portalStore.host(configStore.publicHostname)}/{portalStore.imageRef(
							orgName,
							'myimage'
						)}:latest
					</code>
				</div>
			{/if}
		{/snippet}
	</RepoList>
</div>
