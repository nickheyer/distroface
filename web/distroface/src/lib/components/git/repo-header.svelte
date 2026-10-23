<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import {
		Lock,
		Eye,
		Star,
		GitFork,
		Archive,
		LayoutTemplate,
		Code,
		CircleDot,
		GitPullRequest,
		Tag,
		BookOpen,
		Kanban,
		Play,
		Bot,
		Package,
		Activity,
		Settings,
		BellRing,
		Bell,
		BellOff,
		ChevronDown,
		RefreshCw
	} from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { importLabel, importTitle } from '$lib/git-import';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import {
		DropdownMenu,
		DropdownMenuContent,
		DropdownMenuItem,
		DropdownMenuTrigger
	} from '$lib/components/ui/dropdown-menu';
	import MirrorBadge from '$lib/components/mirror-badge.svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { GitRepoType, Visibility } from '$lib/proto/distroface/v1/types_pb';
	import { GitUnit, WatchMode } from '$lib/proto/distroface/v1/git_pb';
	import type { GitRepository } from '$lib/proto/distroface/v1/git_pb';
	import type { Component } from 'svelte';
	import { Cancel } from '../ui/alert-dialog';

	let { repo }: { repo: GitRepository } = $props();

	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const base = $derived(`/${namespace}/${name}`);
	const params = $derived({ namespace, name });
	const namespaceHref = $derived(
		repo.isOrgNamespace
			? resolve('/orgs/[name]', { name: namespace })
			: resolve('/[namespace]', { namespace })
	);

	// Href of a repository named as ns/name
	function repoHref(full: string): string {
		const [ns, n] = full.split('/');
		return resolve('/[namespace]/[name]', { namespace: ns ?? '', name: n ?? '' });
	}
	const isPrivate = $derived(repo.visibility === Visibility.PRIVATE);
	const isMirror = $derived(repo.type === GitRepoType.MIRROR);
	const syncing = $derived(repo.mirrorSyncing || mirrorSyncStore.syncing('git', repo.id));
	const importRunning = $derived(
		!!repo.importStatus && (repo.importStatus.running || mirrorSyncStore.syncing('import', repo.id))
	);
	const actionsEnabled = $derived(configStore.actionsEnabled && repo.units?.actions === true);
	const agentsEnabled = $derived(configStore.agentsEnabled && repo.units?.agents === true);

	// A finished import changes the repository, reload it once
	let syncSeqSeen = 0;
	$effect(() => {
		const seq = mirrorSyncStore.finishedSeq;
		const ev = mirrorSyncStore.lastFinished;
		if (seq === syncSeqSeen) return;
		syncSeqSeen = seq;
		if (ev?.kind === 'import' && ev.repoId === repo.id) {
			void gitRepoStore.load(namespace, name, true);
		}
	});

	let importPending = $state(false);
	async function retryImport() {
		if (importPending) return;
		importPending = true;
		try {
			await rpcClient.git.syncGitRepository({ namespace, name });
			toast.success('Import resumed');
			await gitRepoStore.load(namespace, name, true);
		} catch {
			// Error interceptor already toasted
		} finally {
			importPending = false;
		}
	}

	async function dismissImport() {
		if (importPending) return;
		importPending = true;
		try {
			await rpcClient.git.dismissGitImport({ namespace, name });
			toast.success('Import dismissed');
			await gitRepoStore.load(namespace, name, true);
		} catch {
			// Error interceptor already toasted
		} finally {
			importPending = false;
		}
	}

	type Tab = {
		key: string;
		label: string;
		href: string;
		icon: Component<{ class?: string }>;
		count?: number;
		show: boolean;
	};

	const tabs = $derived.by<Tab[]>(() => [
		{
			key: 'code',
			label: 'Code',
			href: resolve('/[namespace]/[name]', params),
			icon: Code,
			show: true
		},
		{
			key: 'issues',
			label: 'Issues',
			href: resolve('/[namespace]/[name]/issues', params),
			icon: CircleDot,
			count: Number(repo.openIssueCount),
			show: gitRepoStore.unitEnabled(GitUnit.ISSUES)
		},
		{
			key: 'pulls',
			label: 'Pull requests',
			href: resolve('/[namespace]/[name]/pulls', params),
			icon: GitPullRequest,
			count: Number(repo.openPullCount),
			show: gitRepoStore.unitEnabled(GitUnit.PULLS)
		},
		{
			key: 'releases',
			label: 'Releases',
			href: resolve('/[namespace]/[name]/releases', params),
			icon: Tag,
			count: repo.releaseCount,
			show: gitRepoStore.unitEnabled(GitUnit.RELEASES)
		},
		{
			key: 'wiki',
			label: 'Wiki',
			href: resolve('/[namespace]/[name]/wiki', params),
			icon: BookOpen,
			show: gitRepoStore.unitEnabled(GitUnit.WIKI)
		},
		{
			key: 'projects',
			label: 'Projects',
			href: resolve('/[namespace]/[name]/projects', params),
			icon: Kanban,
			show: gitRepoStore.unitEnabled(GitUnit.PROJECTS)
		},
		{
			key: 'actions',
			label: 'Actions',
			href: resolve('/[namespace]/[name]/actions', params),
			icon: Play,
			show: actionsEnabled
		},
		{
			key: 'agents',
			label: 'Agents',
			href: resolve('/[namespace]/[name]/agents', params),
			icon: Bot,
			show: agentsEnabled
		},
		{
			key: 'packages',
			label: 'Packages',
			href: resolve('/[namespace]/[name]/packages', params),
			icon: Package,
			show: gitRepoStore.unitEnabled(GitUnit.PACKAGES)
		},
		{
			key: 'activity',
			label: 'Activity',
			href: resolve('/[namespace]/[name]/activity', params),
			icon: Activity,
			show: true
		},
		{
			key: 'settings',
			label: 'Settings',
			href: resolve('/[namespace]/[name]/settings', params),
			icon: Settings,
			show: gitRepoStore.canAdmin
		}
	]);

	const CODE_SEGMENTS = new Set([
		'',
		'tree',
		'blob',
		'blame',
		'commits',
		'commit',
		'branches',
		'tags',
		'compare',
		'edit',
		'new',
		'upload',
		'fork',
		'forks',
		'search'
	]);

	const activeKey = $derived.by(() => {
		const rest = page.url.pathname.slice(base.length).replace(/^\//, '');
		const seg = rest.split('/')[0] ?? '';
		if (CODE_SEGMENTS.has(seg)) return 'code';
		if (seg === 'labels' || seg === 'milestones') return 'issues';
		return seg;
	});

	// Read only portals refuse stars and watches, show the counts instead
	const readOnlyPortal = $derived(portalStore.isPortal && !portalStore.allowPush);

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

	const canFork = $derived(
		authStore.isAuthenticated && repo.merge?.allowForks !== false && !repo.isEmpty
	);
	const stars = $derived(Number(repo.starCount));
</script>

<div class="space-y-4">
	<div class="flex flex-wrap items-start gap-x-4 gap-y-3">
		<div class="min-w-0 flex-1 space-y-1.5">
			<div class="flex min-w-0 flex-wrap items-center gap-2.5">
				<h1 class="flex max-w-full min-w-0 items-baseline text-xl font-semibold tracking-tight">
					<!-- eslint-disable svelte/no-navigation-without-resolve -->
					<a
						href={namespaceHref}
						class="truncate font-normal text-muted-foreground transition-colors hover:text-primary"
						>{namespace}</a
					>
					<!-- eslint-enable svelte/no-navigation-without-resolve -->
					<span class="mx-1 font-normal text-muted-foreground/50">/</span>
					<a
						href={resolve('/[namespace]/[name]', params)}
						class="truncate transition-colors hover:text-primary">{name}</a
					>
				</h1>
				<Badge variant="outline" class="shrink-0">
					{#if isPrivate}
						<Lock />Private
					{:else}
						<Eye />Public
					{/if}
				</Badge>
				{#if repo.isArchived}
					<Badge variant="outline" class="shrink-0">
						<Archive />Archived
					</Badge>
				{/if}
				{#if isMirror}
					<MirrorBadge
						label="Mirror"
						error={repo.mirrorLastError}
						title={repo.mirror?.upstream ?? ''}
						{syncing}
					/>
				{/if}
				{#if repo.type === GitRepoType.TEMPLATE}
					<Badge variant="done" class="shrink-0">
						<LayoutTemplate />Template
					</Badge>
				{/if}
				{#if repo.importStatus}
					<MirrorBadge
						label={importLabel(repo.importStatus)}
						syncingLabel="Importing"
						error={repo.importStatus.error}
						title={importTitle(repo.importStatus, mirrorSyncStore.message('import', repo.id))}
						syncing={importRunning}
					/>
					{#if gitRepoStore.canAdmin && !importRunning}
						{#if repo.importStatus.phase !== 'done'}
							<Button
								variant="ghost-muted"
								size="sm"
								onclick={retryImport}
								disabled={importPending}
							>
								<RefreshCw />Retry import
							</Button>
							<Button
								variant="ghost-muted"
								size="sm"
								onclick={dismissImport}
								disabled={importPending}
							>
								<Cancel />Cancel
							</Button>
						{/if}
					{/if}
				{/if}
			</div>

			{#if repo.forkOf || repo.templateOf}
				<p class="flex min-w-0 items-center gap-1.5 text-xs text-muted-foreground">
					{#if repo.forkOf}
						<GitFork class="size-3 shrink-0" />forked from
						<!-- eslint-disable svelte/no-navigation-without-resolve -->
						<a
							href={repoHref(repo.forkOf)}
							class="truncate font-medium text-foreground hover:text-primary">{repo.forkOf}</a
						>
						<!-- eslint-enable svelte/no-navigation-without-resolve -->
					{:else}
						<LayoutTemplate class="size-3 shrink-0" />generated from
						<!-- eslint-disable svelte/no-navigation-without-resolve -->
						<a
							href={repoHref(repo.templateOf)}
							class="truncate font-medium text-foreground hover:text-primary">{repo.templateOf}</a
						>
						<!-- eslint-enable svelte/no-navigation-without-resolve -->
					{/if}
				</p>
			{/if}
		</div>

		<div class="flex w-full shrink-0 items-center gap-2 sm:w-auto">
			{#if authStore.isAuthenticated && !readOnlyPortal}
				<DropdownMenu>
					<DropdownMenuTrigger>
						{#snippet child({ props })}
							<Button
								{...props}
								variant="outline"
								class="flex-1 sm:flex-none"
								disabled={watchPending}
							>
								{#if repo.watchMode === WatchMode.ALL}
									<BellRing />
								{:else if repo.watchMode === WatchMode.IGNORE}
									<BellOff />
								{:else}
									<Bell />
								{/if}
								{watchLabel}
								<ChevronDown class="text-muted-foreground" />
							</Button>
						{/snippet}
					</DropdownMenuTrigger>
					<DropdownMenuContent align="end" class="w-[min(16rem,calc(100vw-2rem))]">
						<DropdownMenuItem onclick={() => setWatch(WatchMode.PARTICIPATING)}>
							<div>
								<div class="font-medium">Participating</div>
								<div class="text-xs text-muted-foreground">
									Only threads you take part in or are mentioned in
								</div>
							</div>
						</DropdownMenuItem>
						<DropdownMenuItem onclick={() => setWatch(WatchMode.ALL)}>
							<div>
								<div class="font-medium">All activity</div>
								<div class="text-xs text-muted-foreground">
									Every issue, pull request, and release
								</div>
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
					<Button
						variant="outline"
						class="flex-1 sm:flex-none"
						href={resolve('/[namespace]/[name]/fork', params)}
					>
						<GitFork />
						Fork
						{#if repo.forkCount > 0}
							<span class="text-muted-foreground tabular-nums">{repo.forkCount}</span>
						{/if}
					</Button>
				{:else if repo.forkCount > 0}
					<Button
						variant="outline"
						class="flex-1 sm:flex-none"
						href={resolve('/[namespace]/[name]/forks', params)}
					>
						<GitFork />Forks
						<span class="text-muted-foreground tabular-nums">{repo.forkCount}</span>
					</Button>
				{/if}
				<Button
					variant="outline"
					class="flex-1 sm:flex-none"
					onclick={toggleStar}
					disabled={starPending}
				>
					<Star class={repo.isStarred ? 'fill-warning text-warning' : ''} />
					{repo.isStarred ? 'Starred' : 'Star'}
					{#if stars > 0}
						<span class="text-muted-foreground tabular-nums">{stars.toLocaleString()}</span>
					{/if}
				</Button>
			{:else}
				<div class="flex items-center gap-3 text-xs text-muted-foreground tabular-nums">
					{#if stars > 0}
						<span class="flex items-center gap-1"
							><Star class="size-3.5" />{stars.toLocaleString()}</span
						>
					{/if}
					{#if repo.forkCount > 0}
						<a
							href={resolve('/[namespace]/[name]/forks', params)}
							class="flex items-center gap-1 hover:text-primary"
							><GitFork class="size-3.5" />{repo.forkCount}</a
						>
					{/if}
				</div>
			{/if}
		</div>
	</div>

	<nav
		class="scrollbar-none -mx-4 flex items-center gap-0.5 overflow-x-auto border-b px-4 sm:-mx-1 sm:px-1"
		aria-label="Repository sections"
	>
		{#each tabs.filter((t) => t.show) as tab (tab.key)}
			{@const active = activeKey === tab.key}
			<!-- eslint-disable svelte/no-navigation-without-resolve -->
			<a
				href={tab.href}
				class="-mb-px flex shrink-0 items-center gap-1.5 border-b-2 px-3 py-2 text-sm font-medium whitespace-nowrap transition-colors {active
					? 'border-primary text-foreground'
					: 'border-transparent text-muted-foreground hover:text-foreground'}"
				aria-current={active ? 'page' : undefined}
			>
				<tab.icon class="size-3.5" />
				{tab.label}
				{#if tab.count !== undefined && tab.count > 0}
					<span class="rounded-full bg-muted px-1.5 text-xs text-muted-foreground tabular-nums"
						>{tab.count.toLocaleString()}</span
					>
				{/if}
			</a>
			<!-- eslint-enable svelte/no-navigation-without-resolve -->
		{/each}
	</nav>
</div>
