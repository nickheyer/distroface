<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import GitRepoCard from '$lib/components/git/git-repo-card.svelte';
	import { GitFork } from '@lucide/svelte';
	import type { GitRepository } from '$lib/proto/distroface/v1/git_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const forkHref = $derived(resolve('/[namespace]/[name]/fork', { namespace, name }));

	let forks = $state<GitRepository[]>([]);
	let loading = $state(false);
	let loaded = $state(false);
	let loadedFor = $state('');
	const pager = new Pager(20);

	const canFork = $derived(authStore.isAuthenticated && (repo?.merge?.allowForks ?? true));
	const ownFork = $derived(forks.find((f) => f.namespace === authStore.user?.username));

	$effect(() => {
		void gitRepoStore.load(namespace, name);
	});

	$effect(() => {
		if (repo && repo.fullName !== loadedFor) {
			loadedFor = repo.fullName;
			pager.reset();
			void load();
		}
	});

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.git.listForks(
				{ namespace, name, page: pager.request() },
				silentCallOptions
			);
			forks = resp.repositories;
			pager.apply(resp.page);
		} catch {
			forks = [];
		} finally {
			loading = false;
			loaded = true;
		}
	}
</script>

<svelte:head>
	<title>Forks - {namespace}/{name}</title>
</svelte:head>

<div class="space-y-4">
	<div class="section-header">
		<div>
			<h2 class="section-title">Forks</h2>
			<p class="section-subtitle">
				{#if repo?.forkOf}
					{@const upstream = repo.forkOf.split('/')}
					Forked from
					<a
						href={resolve('/[namespace]/[name]', {
							namespace: upstream[0] ?? '',
							name: upstream.slice(1).join('/')
						})}
						class="link">{repo.forkOf}</a
					>.
				{/if}
				{repo?.forkCount ?? 0} fork{(repo?.forkCount ?? 0) === 1 ? '' : 's'} of this repository.
			</p>
		</div>
		<PermissionGate allowed={canFork}>
			{#if ownFork}
				<Button
					variant="outline"
					href={resolve('/[namespace]/[name]', {
						namespace: ownFork.namespace,
						name: ownFork.name
					})}
				>
					<GitFork />Your fork
				</Button>
			{:else}
				<Button href={forkHref} disabled={!repo}>
					<GitFork />Fork
				</Button>
			{/if}
		</PermissionGate>
	</div>

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 3 }, i (i)}
				<div class="space-y-2.5 px-4 py-3.5">
					<Skeleton class="h-4 w-48" />
					<Skeleton class="h-3.5 w-72" />
					<div class="flex gap-4">
						<Skeleton class="h-3 w-14" />
						<Skeleton class="h-3 w-20" />
					</div>
				</div>
			{/each}
		</div>
	{:else if forks.length === 0}
		<EmptyState
			icon={GitFork}
			message="No forks yet"
			description="A fork is a copy you own, send changes back with a pull request."
		>
			{#snippet actions()}
				<PermissionGate allowed={canFork}>
					<Button variant="outline" href={forkHref}>
						<GitFork />Fork repository
					</Button>
				</PermissionGate>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="box divide-y transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			{#each forks as f (f.id)}
				<GitRepoCard repo={f} />
			{/each}
		</div>
		<DataPagination {pager} onChange={load} />
	{/if}
</div>
