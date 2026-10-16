<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Input } from '$lib/components/ui/input';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import { GitFork, Star, Lock } from '@lucide/svelte';
	import { Visibility } from '$lib/proto/distroface/v1/types_pb';
	import type { GitRepository } from '$lib/proto/distroface/v1/git_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);

	let forks = $state<GitRepository[]>([]);
	let loading = $state(false);
	let loaded = $state(false);
	let loadedFor = $state('');
	const pager = new Pager(20);

	let forkOpen = $state(false);
	let targetNamespace = $state('');
	let targetName = $state('');
	let forking = $state(false);

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
			const resp = await rpcClient.git.listForks({ namespace, name, page: pager.request() }, silentCallOptions);
			forks = resp.repositories;
			pager.apply(resp.page);
		} catch {
			forks = [];
		} finally {
			loading = false;
			loaded = true;
		}
	}

	function openFork() {
		targetNamespace = authStore.user?.username ?? '';
		targetName = name;
		forkOpen = true;
	}

	async function fork() {
		forking = true;
		try {
			const resp = await rpcClient.git.forkGitRepository({
				namespace, name,
				targetNamespace: targetNamespace.trim(),
				targetName: targetName.trim()
			});
			toast.success('Fork created');
			forkOpen = false;
			await goto(resolve('/[namespace]/[name]', { namespace: resp.repository?.namespace ?? targetNamespace.trim(), name: resp.repository?.name ?? targetName.trim() }));
		} catch {
			// Error interceptor already toasted
		} finally {
			forking = false;
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
					Forked from <a href={resolve('/[namespace]/[name]', { namespace: upstream[0] ?? '', name: upstream.slice(1).join('/') })} class="hover:text-primary transition-colors">{repo.forkOf}</a>.
				{/if}
				{repo?.forkCount ?? 0} fork{(repo?.forkCount ?? 0) === 1 ? '' : 's'} of this repository.
			</p>
		</div>
		<PermissionGate allowed={canFork}>
			{#if ownFork}
				<Button size="sm" variant="outline" href={resolve('/[namespace]/[name]', { namespace: ownFork.namespace, name: ownFork.name })}>
					<GitFork class="h-4 w-4 mr-1.5" />Your fork
				</Button>
			{:else}
				<Button size="sm" onclick={openFork} disabled={!repo}>
					<GitFork class="h-4 w-4 mr-1.5" />Fork
				</Button>
			{/if}
		</PermissionGate>
	</div>

	{#if !loaded}
		<Skeleton class="h-40 w-full rounded-xl" />
	{:else if forks.length === 0}
		<EmptyState icon={GitFork} message="No forks yet" description="A fork is a copy you own, send changes back with a pull request.">
			{#snippet actions()}
				<PermissionGate allowed={canFork}>
					<Button variant="outline" size="sm" onclick={openFork}>
						<GitFork class="h-4 w-4 mr-1.5" />Fork Repository
					</Button>
				</PermissionGate>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="rounded-xl border border-border/60 divide-y divide-border/40 overflow-hidden transition-opacity {loading ? 'opacity-60' : ''}">
			{#each forks as f (f.id)}
				<div class="flex items-center gap-4 px-5 py-3.5">
					<GitFork class="h-4 w-4 text-muted-foreground shrink-0" />
					<div class="flex-1 min-w-0">
						<div class="flex items-center gap-2 min-w-0">
							<a href={resolve('/[namespace]/[name]', { namespace: f.namespace, name: f.name })} class="text-sm font-medium truncate hover:text-primary transition-colors">{f.fullName}</a>
							{#if f.visibility === Visibility.PRIVATE}
								<Lock class="h-3 w-3 text-muted-foreground shrink-0" />
							{/if}
						</div>
						{#if f.description}
							<p class="text-xs text-muted-foreground truncate mt-0.5">{f.description}</p>
						{/if}
					</div>
					<div class="flex items-center gap-3 text-xs text-muted-foreground shrink-0">
						<span class="flex items-center gap-1 tabular-nums"><Star class="h-3 w-3" />{Number(f.starCount)}</span>
						{#if f.lastPushedAt}
							<span class="hidden sm:inline">pushed {relativeTime(timestampDate(f.lastPushedAt))}</span>
						{/if}
					</div>
				</div>
			{/each}
		</div>
		<DataPagination {pager} onChange={load} />
	{/if}
</div>

<FormPanel bind:open={forkOpen} title="Fork repository" description="Copies every branch and tag into a repository you control." icon={GitFork}>
	<div class="space-y-3">
		<FormField label="Owner" id="fork-ns" required help="Your username or an organization you can create repositories in">
			<Input id="fork-ns" bind:value={targetNamespace} class="font-mono text-sm" />
		</FormField>
		<FormField label="Name" id="fork-name" required>
			<Input id="fork-name" bind:value={targetName} class="font-mono text-sm" />
		</FormField>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (forkOpen = false)}>Cancel</Button>
		<Button onclick={fork} disabled={forking || !targetNamespace.trim() || !targetName.trim()}>{forking ? 'Forking...' : 'Create Fork'}</Button>
	{/snippet}
</FormPanel>
