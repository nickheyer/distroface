<script lang="ts">
	import { onMount } from 'svelte';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { resolve } from '$app/paths';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import MirrorBadge from '$lib/components/mirror-badge.svelte';
	import { Archive, Plus, Trash, Lock, Globe, HardDrive, Clock } from '$lib/icons';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime, formatBytes } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { ArtifactRepoType } from '$lib/proto/distroface/v1/types_pb';
	import type { ArtifactRepository } from '$lib/proto/distroface/v1/types_pb';
	import { artifactMirrorLabel } from '$lib/mirror';

	let { namespace, canCreate = false }: { namespace: string; canCreate?: boolean } = $props();

	let repos = $state<ArtifactRepository[]>([]);
	let loading = $state(true);
	let loaded = $state(false);
	const pager = new Pager(20);
	const filter = new QueryFilter([
		{ key: 'name', label: 'Name' },
		{ key: 'namespace', label: 'Namespace' },
		{ key: 'description', label: 'Description' }
	]);
	const newHref = $derived(
		`${resolve('/artifacts/new')}?namespace=${encodeURIComponent(namespace)}`
	);

	let deleteDialogOpen = $state(false);
	let deleteTarget = $state<ArtifactRepository | null>(null);
	let deleting = $state(false);

	async function loadRepos() {
		loading = true;
		try {
			const resp = await rpcClient.artifact.listArtifactRepositories({
				page: pager.request(filter.request()),
				namespace
			});
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

	function openDelete(repo: ArtifactRepository, e: MouseEvent) {
		e.stopPropagation();
		deleteTarget = repo;
		deleteDialogOpen = true;
	}

	async function confirmDelete() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await rpcClient.artifact.deleteArtifactRepository({
				name: deleteTarget.name,
				namespace: deleteTarget.namespace
			});
			toast.success('Repository deleted');
			deleteDialogOpen = false;
			await loadRepos();
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}

	function canDelete(repo: ArtifactRepository): boolean {
		return (
			canCreate ||
			repo.owner === authStore.user?.username ||
			authStore.hasPermission('artifacts', 'manage')
		);
	}

	onMount(() => {
		mirrorSyncStore.ensure();
		loadRepos();
	});

	// Finished syncs refresh the list live
	let syncSeqSeen = 0;
	$effect(() => {
		const seq = mirrorSyncStore.finishedSeq;
		if (seq === syncSeqSeen) return;
		syncSeqSeen = seq;
		if (mirrorSyncStore.lastFinished?.kind === 'artifact') loadRepos();
	});
</script>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Artifact repositories</h2>
			<p class="section-subtitle">
				Build outputs, packages, and mirrored release assets under {namespace}.
			</p>
		</div>
		{#if canCreate}
			<Button class="shrink-0" href={newHref}>
				<Plus />
				New repository
			</Button>
		{/if}
	</div>

	<div class="max-w-md">
		<QueryFilterBar {filter} placeholder="Search repositories" onchange={filterChanged} />
	</div>

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 3 }, i (i)}
				<div class="space-y-2.5 px-4 py-3.5">
					<Skeleton class="h-4 w-48" />
					<Skeleton class="h-3.5 w-72" />
					<div class="flex gap-4">
						<Skeleton class="h-3 w-14" />
						<Skeleton class="h-3 w-14" />
						<Skeleton class="h-3 w-20" />
					</div>
				</div>
			{/each}
		</div>
	{:else if repos.length === 0}
		<EmptyState
			icon={Archive}
			message={filter.active ? 'No artifact repositories found' : 'No artifact repositories yet'}
			description={filter.active
				? 'No results match the current filter'
				: `Repositories under ${namespace} hold build artifacts, packages, and other files.`}
		>
			{#snippet actions()}
				{#if canCreate && !filter.active}
					<Button variant="outline" href={newHref}>
						<Plus />
						New repository
					</Button>
				{/if}
			{/snippet}
		</EmptyState>
	{:else}
		<div class="box divide-y transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			{#each repos as repo (repo.id)}
				<div class="group flex items-center gap-3 px-4 py-3 transition-colors hover:bg-muted/30">
					<a
						href={resolve('/artifacts/[namespace]/[repo]', {
							namespace: repo.namespace,
							repo: repo.name
						})}
						class="min-w-0 flex-1"
					>
						<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
							<span
								class="truncate text-sm font-semibold underline-offset-2 group-hover:text-primary group-hover:underline"
							>
								{repo.name}
							</span>
							<Badge variant="outline">
								{#if repo.isPrivate}
									<Lock />Private
								{:else}
									<Globe />Public
								{/if}
							</Badge>
							{#if repo.type !== ArtifactRepoType.FILE && repo.type !== ArtifactRepoType.UNSPECIFIED}
								<MirrorBadge
									label={artifactMirrorLabel(repo.type)}
									error={repo.mirrorLastError}
									title={repo.mirror?.upstream ?? ''}
									syncing={repo.mirrorSyncing || mirrorSyncStore.syncing('artifact', repo.id)}
								/>
							{/if}
						</div>
						{#if repo.description}
							<p class="mt-0.5 line-clamp-1 text-sm text-muted-foreground">{repo.description}</p>
						{/if}
						<div
							class="mt-1.5 flex flex-wrap items-center gap-x-3.5 gap-y-1 text-xs text-muted-foreground"
						>
							<span class="flex items-center gap-1 tabular-nums">
								<Archive class="size-3.5" />{repo.artifactCount} artifact{Number(
									repo.artifactCount
								) === 1
									? ''
									: 's'}
							</span>
							<span class="flex items-center gap-1">
								<HardDrive class="size-3.5" />{formatBytes(Number(repo.totalSize))}
							</span>
							{#if repo.createdAt}
								<span class="flex items-center gap-1">
									<Clock class="size-3.5" />Created {relativeTime(timestampDate(repo.createdAt))}
								</span>
							{/if}
						</div>
					</a>
					{#if canDelete(repo)}
						<Button
							variant="ghost-muted"
							size="icon-sm"
							class="shrink-0 hover:text-destructive"
							onclick={(e) => openDelete(repo, e)}
							aria-label="Delete {repo.name}"
						>
							<Trash />
						</Button>
					{/if}
				</div>
			{/each}
		</div>
		<DataPagination {pager} onChange={loadRepos} />
	{/if}
</div>

<ConfirmDialog
	bind:open={deleteDialogOpen}
	title="Delete repository"
	confirmLabel="Delete"
	onConfirm={confirmDelete}
	loading={deleting}
>
	{#snippet description()}
		This permanently deletes <strong>{deleteTarget?.namespace}/{deleteTarget?.name}</strong> and all its
		artifacts.
	{/snippet}
</ConfirmDialog>
