<script lang="ts">
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { resolve } from '$app/paths';
	import { configStore } from '$lib/stores/config.svelte';
	import { Pager } from '$lib/pager.svelte';
	import RepoList from '$lib/components/repo-list.svelte';
	import GitRepoList from '$lib/components/git/git-repo-list.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Archive, Globe, Lock, Star } from '$lib/icons';
	import { cn, formatBytes } from '$lib/utils';
	import type { GitRepository } from '$lib/proto/distroface/v1/git_pb';
	import type { ArtifactRepository, Repository } from '$lib/proto/distroface/v1/types_pb';

	// Everything one user starred, one list per repository kind
	let { username, isOwn = false }: { username: string; isOwn?: boolean } = $props();

	type Kind = 'git' | 'images' | 'artifacts';
	let kind = $state<Kind>(configStore.gitEnabled ? 'git' : 'images');

	let git = $state<GitRepository[]>([]);
	let images = $state<Repository[]>([]);
	let artifacts = $state<ArtifactRepository[]>([]);
	const gitPager = new Pager(20);
	const imagePager = new Pager(20);
	const artifactPager = new Pager(20);
	let gitLoaded = $state(false);
	let imagesLoaded = $state(false);
	let artifactsLoaded = $state(false);
	let gitLoading = $state(false);
	let imagesLoading = $state(false);
	let artifactsLoading = $state(false);

	async function loadGit() {
		gitLoading = true;
		try {
			const r = await rpcClient.git.listStarredGitRepositories(
				{ username, page: gitPager.request() },
				silentCallOptions
			);
			git = r.repositories;
			gitPager.apply(r.page);
		} catch {
			git = [];
			gitPager.apply();
		} finally {
			gitLoading = false;
			gitLoaded = true;
		}
	}

	async function loadImages() {
		imagesLoading = true;
		try {
			const r = await rpcClient.repository.listStarredRepositories(
				{ username, page: imagePager.request() },
				silentCallOptions
			);
			images = r.repositories;
			imagePager.apply(r.page);
		} catch {
			images = [];
			imagePager.apply();
		} finally {
			imagesLoading = false;
			imagesLoaded = true;
		}
	}

	async function loadArtifacts() {
		artifactsLoading = true;
		try {
			const r = await rpcClient.artifact.listStarredArtifactRepositories(
				{ username, page: artifactPager.request() },
				silentCallOptions
			);
			artifacts = r.repositories;
			artifactPager.apply(r.page);
		} catch {
			artifacts = [];
			artifactPager.apply();
		} finally {
			artifactsLoading = false;
			artifactsLoaded = true;
		}
	}

	let loadedFor = '';
	$effect(() => {
		if (!username || username === loadedFor) return;
		loadedFor = username;
		gitPager.reset();
		imagePager.reset();
		artifactPager.reset();
		if (configStore.gitEnabled) void loadGit();
		void loadImages();
		void loadArtifacts();
	});

	const tabs = $derived<{ key: Kind; label: string; count: number; show: boolean }[]>([
		{ key: 'git', label: 'Git', count: gitPager.totalCount, show: configStore.gitEnabled },
		{ key: 'images', label: 'Images', count: imagePager.totalCount, show: true },
		{ key: 'artifacts', label: 'Artifacts', count: artifactPager.totalCount, show: true }
	]);
	const who = $derived(isOwn ? 'You have' : `${username} has`);
</script>

<div class="space-y-4">
	<nav
		class="scrollbar-none flex items-center gap-0.5 overflow-x-auto border-b"
		aria-label="Starred kinds"
	>
		{#each tabs.filter((t) => t.show) as t (t.key)}
			<button
				type="button"
				onclick={() => (kind = t.key)}
				aria-pressed={kind === t.key}
				class={cn(
					'-mb-px flex shrink-0 items-center gap-1.5 border-b-2 px-3 py-2 text-sm font-medium whitespace-nowrap transition-colors',
					kind === t.key
						? 'border-primary text-foreground'
						: 'border-transparent text-muted-foreground hover:text-foreground'
				)}
			>
				{t.label}
				{#if t.count > 0}
					<span class="rounded-full bg-muted px-1.5 text-xs text-muted-foreground tabular-nums"
						>{t.count}</span
					>
				{/if}
			</button>
		{/each}
	</nav>

	{#if kind === 'git'}
		<GitRepoList
			repos={git}
			pager={gitPager}
			onChange={loadGit}
			loading={gitLoading}
			loaded={gitLoaded}
			emptyMessage="{who} not starred any git repositories"
		/>
	{:else if kind === 'images'}
		<RepoList
			repos={images}
			pager={imagePager}
			onChange={loadImages}
			loading={imagesLoading}
			loaded={imagesLoaded}
			emptyMessage="{who} not starred any image repositories"
		/>
	{:else if !artifactsLoaded}
		<div class="box divide-y">
			{#each { length: 3 }, i (i)}
				<div class="space-y-2.5 px-4 py-3.5">
					<Skeleton class="h-4 w-48" />
					<Skeleton class="h-3.5 w-72" />
				</div>
			{/each}
		</div>
	{:else if artifacts.length === 0}
		<EmptyState icon={Star} message="{who} not starred any artifact repositories" />
	{:else}
		<div class="box divide-y {artifactsLoading ? 'opacity-60' : ''}">
			{#each artifacts as repo (repo.id)}
				<a
					href={resolve('/artifacts/[namespace]/[repo]', {
						namespace: repo.namespace,
						repo: repo.name
					})}
					class="group block px-4 py-3 transition-colors hover:bg-muted/30"
				>
					<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
						<span class="flex min-w-0 items-baseline text-sm font-semibold">
							<span class="truncate font-normal text-muted-foreground">{repo.namespace}</span>
							<span class="mx-0.5 text-muted-foreground/50">/</span>
							<span
								class="truncate underline-offset-2 group-hover:text-primary group-hover:underline"
								>{repo.name}</span
							>
						</span>
						<Badge variant="outline">
							{#if repo.isPrivate}<Lock />Private{:else}<Globe />Public{/if}
						</Badge>
					</div>
					{#if repo.description}
						<p class="mt-0.5 line-clamp-1 text-sm text-muted-foreground">{repo.description}</p>
					{/if}
					<div
						class="mt-1.5 flex flex-wrap items-center gap-x-3.5 gap-y-1 text-xs text-muted-foreground"
					>
						<span class="flex items-center gap-1 tabular-nums"
							><Archive class="size-3.5" />{repo.artifactCount}</span
						>
						<span class="flex items-center gap-1">{formatBytes(Number(repo.totalSize))}</span>
						<span class="flex items-center gap-1 tabular-nums"
							><Star class="size-3.5 {repo.isStarred ? 'fill-warning text-warning' : ''}" />{Number(
								repo.starCount
							)}</span
						>
					</div>
				</a>
			{/each}
		</div>
		<DataPagination pager={artifactPager} onChange={loadArtifacts} />
	{/if}
</div>
