<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import {
		Lock, Eye, Star, GitFork, Archive, LayoutTemplate, Code, CircleDot,
		GitPullRequest, Tag, BookOpen, Kanban, Play, Package, Activity, Settings, BellRing, Bell, BellOff, ChevronDown, Loader2
	} from '@lucide/svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import {
		DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger
	} from '$lib/components/ui/dropdown-menu';
	import MirrorBadge from '$lib/components/mirror-badge.svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { GitRepoType, Visibility } from '$lib/proto/distroface/v1/types_pb';
	import { GitUnit, WatchMode } from '$lib/proto/distroface/v1/git_pb';
	import type { GitRepository } from '$lib/proto/distroface/v1/git_pb';
	import { toast } from 'svelte-sonner';
	import type { Component } from 'svelte';

	let { repo }: { repo: GitRepository } = $props();

	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const base = $derived(`/${namespace}/${name}`);
	const params = $derived({ namespace, name });
	const namespaceHref = $derived(
		repo.isOrgNamespace ? resolve('/orgs/[name]', { name: namespace }) : resolve('/[namespace]', { namespace })
	);

	// Href of a repository named as ns/name
	function repoHref(full: string): string {
		const [ns, n] = full.split('/');
		return resolve('/[namespace]/[name]', { namespace: ns ?? '', name: n ?? '' });
	}
	const isPrivate = $derived(repo.visibility === Visibility.PRIVATE);
	const isMirror = $derived(repo.type === GitRepoType.MIRROR);
	const syncing = $derived(repo.mirrorSyncing || mirrorSyncStore.syncing('git', repo.id));
	const actionsEnabled = $derived(configStore.actionsEnabled && repo.units?.actions === true);

	type Tab = { key: string; label: string; href: string; icon: Component<{ class?: string }>; count?: number; show: boolean };

	const tabs = $derived.by<Tab[]>(() => [
		{ key: 'code', label: 'Code', href: resolve('/[namespace]/[name]', params), icon: Code, show: true },
		{ key: 'issues', label: 'Issues', href: resolve('/[namespace]/[name]/issues', params), icon: CircleDot, count: Number(repo.openIssueCount), show: gitRepoStore.unitEnabled(GitUnit.ISSUES) },
		{ key: 'pulls', label: 'Pull requests', href: resolve('/[namespace]/[name]/pulls', params), icon: GitPullRequest, count: Number(repo.openPullCount), show: gitRepoStore.unitEnabled(GitUnit.PULLS) },
		{ key: 'releases', label: 'Releases', href: resolve('/[namespace]/[name]/releases', params), icon: Tag, count: repo.releaseCount, show: gitRepoStore.unitEnabled(GitUnit.RELEASES) },
		{ key: 'wiki', label: 'Wiki', href: resolve('/[namespace]/[name]/wiki', params), icon: BookOpen, show: gitRepoStore.unitEnabled(GitUnit.WIKI) },
		{ key: 'projects', label: 'Projects', href: resolve('/[namespace]/[name]/projects', params), icon: Kanban, show: gitRepoStore.unitEnabled(GitUnit.PROJECTS) },
		{ key: 'actions', label: 'Actions', href: resolve('/[namespace]/[name]/actions', params), icon: Play, show: actionsEnabled },
		{ key: 'packages', label: 'Packages', href: resolve('/[namespace]/[name]/packages', params), icon: Package, show: gitRepoStore.unitEnabled(GitUnit.PACKAGES) },
		{ key: 'activity', label: 'Activity', href: resolve('/[namespace]/[name]/activity', params), icon: Activity, show: true },
		{ key: 'settings', label: 'Settings', href: resolve('/[namespace]/[name]/settings', params), icon: Settings, show: gitRepoStore.canAdmin }
	]);

	const CODE_SEGMENTS = new Set(['', 'tree', 'blob', 'blame', 'commits', 'commit', 'branches', 'tags', 'compare', 'edit', 'new', 'upload', 'forks', 'search']);

	const activeKey = $derived.by(() => {
		const rest = page.url.pathname.slice(base.length).replace(/^\//, '');
		const seg = rest.split('/')[0] ?? '';
		if (CODE_SEGMENTS.has(seg)) return 'code';
		if (seg === 'labels' || seg === 'milestones') return 'issues';
		return seg;
	});

	let starPending = $state(false);
	async function toggleStar() {
		if (starPending) return;
		starPending = true;
		try {
			if (repo.isStarred) {
				const resp = await rpcClient.git.unstarGitRepository({ namespace, name });
				repo.isStarred = false;
				repo.starCount = resp.starCount;
			} else {
				const resp = await rpcClient.git.starGitRepository({ namespace, name });
				repo.isStarred = true;
				repo.starCount = resp.starCount;
			}
		} catch {
			// Error interceptor already toasted
		} finally {
			starPending = false;
		}
	}

	let watchPending = $state(false);
	async function setWatch(mode: WatchMode) {
		if (watchPending) return;
		watchPending = true;
		try {
			const resp = await rpcClient.git.watchGitRepository({ namespace, name, mode });
			repo.watchMode = resp.mode;
		} catch {
			// Error interceptor already toasted
		} finally {
			watchPending = false;
		}
	}

	const watchLabel = $derived.by(() => {
		switch (repo.watchMode) {
			case WatchMode.ALL:
				return 'Watching';
			case WatchMode.IGNORE:
				return 'Ignoring';
			default:
				return 'Watch';
		}
	});

	let forking = $state(false);
	async function fork() {
		if (forking) return;
		forking = true;
		try {
			const resp = await rpcClient.git.forkGitRepository({ namespace, name });
			const f = resp.repository;
			toast.success('Fork created');
			if (f) await goto(resolve('/[namespace]/[name]', { namespace: f.namespace, name: f.name }));
		} catch {
			// Error interceptor already toasted
		} finally {
			forking = false;
		}
	}

	const canFork = $derived(authStore.isAuthenticated && repo.merge?.allowForks !== false && !repo.isEmpty);
	const stars = $derived(Number(repo.starCount));
</script>

<div class="space-y-4">
	<div class="flex flex-wrap items-start gap-x-4 gap-y-3">
		<div class="flex-1 min-w-0 space-y-1.5">
			<div class="flex items-center gap-2.5 flex-wrap min-w-0">
				<h1 class="text-xl font-bold tracking-tight flex items-baseline min-w-0 max-w-full">
					<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
					<a href={namespaceHref} class="text-muted-foreground font-normal hover:text-primary transition-colors truncate">{namespace}</a>
					<span class="text-muted-foreground/40 mx-1 font-normal">/</span>
					<a href={resolve('/[namespace]/[name]', params)} class="hover:text-primary transition-colors truncate">{name}</a>
				</h1>
				<Badge
					variant="outline"
					class="text-[10px] shrink-0 gap-0.5 py-0 h-4.5 {isPrivate
						? 'border-amber-500/30 text-amber-600 dark:text-amber-400'
						: 'border-primary/20 text-primary/60 dark:text-primary/70'}"
				>
					{#if isPrivate}
						<Lock class="h-2.5 w-2.5" />Private
					{:else}
						<Eye class="h-2.5 w-2.5" />Public
					{/if}
				</Badge>
				{#if repo.isArchived}
					<Badge variant="outline" class="text-[10px] shrink-0 gap-0.5 py-0 h-4.5 text-muted-foreground">
						<Archive class="h-2.5 w-2.5" />Archived
					</Badge>
				{/if}
				{#if isMirror}
					<MirrorBadge label="Mirror" error={repo.mirrorLastError} title={repo.mirror?.upstream ?? ''} {syncing} />
				{/if}
				{#if repo.type === GitRepoType.TEMPLATE}
					<Badge variant="outline" class="text-[10px] shrink-0 gap-0.5 py-0 h-4.5 border-violet-500/30 text-violet-600 dark:text-violet-400">
						<LayoutTemplate class="h-2.5 w-2.5" />Template
					</Badge>
				{/if}
			</div>

			{#if repo.forkOf || repo.templateOf}
				<p class="text-[12.5px] text-muted-foreground flex items-center gap-1.5 min-w-0">
					{#if repo.forkOf}
						<GitFork class="h-3 w-3 shrink-0" />forked from
						<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
						<a href={repoHref(repo.forkOf)} class="font-medium text-foreground/80 hover:text-primary truncate">{repo.forkOf}</a>
					{:else}
						<LayoutTemplate class="h-3 w-3 shrink-0" />generated from
						<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
						<a href={repoHref(repo.templateOf)} class="font-medium text-foreground/80 hover:text-primary truncate">{repo.templateOf}</a>
					{/if}
				</p>
			{/if}
		</div>

		<div class="flex items-center gap-2 shrink-0 w-full sm:w-auto">
			{#if authStore.isAuthenticated}
				<DropdownMenu>
					<DropdownMenuTrigger>
						{#snippet child({ props })}
							<Button {...props} variant="outline" size="sm" class="h-8 gap-1.5 flex-1 sm:flex-none" disabled={watchPending}>
								{#if repo.watchMode === WatchMode.ALL}
									<BellRing class="h-3.5 w-3.5" />
								{:else if repo.watchMode === WatchMode.IGNORE}
									<BellOff class="h-3.5 w-3.5" />
								{:else}
									<Bell class="h-3.5 w-3.5" />
								{/if}
								{watchLabel}
								<ChevronDown class="h-3 w-3 text-muted-foreground" />
							</Button>
						{/snippet}
					</DropdownMenuTrigger>
					<DropdownMenuContent align="end" class="w-[min(16rem,calc(100vw-2rem))]">
						<DropdownMenuItem onclick={() => setWatch(WatchMode.PARTICIPATING)}>
							<div>
								<div class="font-medium">Participating</div>
								<div class="text-xs text-muted-foreground">Only threads you take part in or are mentioned in</div>
							</div>
						</DropdownMenuItem>
						<DropdownMenuItem onclick={() => setWatch(WatchMode.ALL)}>
							<div>
								<div class="font-medium">All activity</div>
								<div class="text-xs text-muted-foreground">Every issue, pull request, and release</div>
							</div>
						</DropdownMenuItem>
						<DropdownMenuItem onclick={() => setWatch(WatchMode.IGNORE)}>
							<div>
								<div class="font-medium">Ignore</div>
								<div class="text-xs text-muted-foreground">Never notify</div>
							</div>
						</DropdownMenuItem>
					</DropdownMenuContent>
				</DropdownMenu>
				{#if canFork}
					<Button variant="outline" size="sm" class="h-8 gap-1.5 flex-1 sm:flex-none" onclick={fork} disabled={forking}>
						{#if forking}
							<Loader2 class="h-3.5 w-3.5 animate-spin" />
						{:else}
							<GitFork class="h-3.5 w-3.5" />
						{/if}
						Fork
						{#if repo.forkCount > 0}
							<span class="tabular-nums text-muted-foreground">{repo.forkCount}</span>
						{/if}
					</Button>
				{:else if repo.forkCount > 0}
					<Button variant="outline" size="sm" class="h-8 gap-1.5 flex-1 sm:flex-none" href={resolve('/[namespace]/[name]/forks', params)}>
						<GitFork class="h-3.5 w-3.5" />Forks
						<span class="tabular-nums text-muted-foreground">{repo.forkCount}</span>
					</Button>
				{/if}
				<Button variant="outline" size="sm" class="h-8 gap-1.5 flex-1 sm:flex-none" onclick={toggleStar} disabled={starPending}>
					<Star class="h-3.5 w-3.5 {repo.isStarred ? 'fill-amber-400 text-amber-400' : ''}" />
					{repo.isStarred ? 'Starred' : 'Star'}
					{#if stars > 0}
						<span class="tabular-nums text-muted-foreground">{stars.toLocaleString()}</span>
					{/if}
				</Button>
			{:else}
				<div class="flex items-center gap-3 text-[12.5px] text-muted-foreground tabular-nums">
					{#if stars > 0}
						<span class="flex items-center gap-1"><Star class="h-3.5 w-3.5" />{stars.toLocaleString()}</span>
					{/if}
					{#if repo.forkCount > 0}
						<a href={resolve('/[namespace]/[name]/forks', params)} class="flex items-center gap-1 hover:text-primary"><GitFork class="h-3.5 w-3.5" />{repo.forkCount}</a>
					{/if}
				</div>
			{/if}
		</div>
	</div>

	<nav class="flex items-center gap-0.5 border-b border-border/60 overflow-x-auto -mx-4 px-4 sm:-mx-1 sm:px-1 scrollbar-none" aria-label="Repository sections">
		{#each tabs.filter((t) => t.show) as tab (tab.key)}
			{@const active = activeKey === tab.key}
			<!-- eslint-disable svelte/no-navigation-without-resolve -->
			<a
				href={tab.href}
				class="flex items-center gap-1.5 px-3 py-2 text-[13px] font-medium whitespace-nowrap border-b-2 -mb-px transition-colors shrink-0 {active
					? 'border-primary text-foreground'
					: 'border-transparent text-muted-foreground hover:text-foreground hover:border-border'}"
				aria-current={active ? 'page' : undefined}
			>
				<tab.icon class="h-3.5 w-3.5" />
				{tab.label}
				{#if tab.count !== undefined && tab.count > 0}
					<span class="rounded-full bg-muted text-muted-foreground px-1.5 py-px text-[11px] tabular-nums">{tab.count.toLocaleString()}</span>
				{/if}
			</a>
			<!-- eslint-enable svelte/no-navigation-without-resolve -->
		{/each}
	</nav>
</div>
