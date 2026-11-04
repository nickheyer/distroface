<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import {
		Building,
		Calendar,
		GitBranch,
		Package,
		Archive,
		Star,
		Settings,
		UserAvatar as UserIcon,
		Activity,
		Users
	} from '$lib/icons';
	import { rpcClient, silentCallOptions, backgroundCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import { relativeTime, cn } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import UserAvatar from '$lib/components/user-avatar.svelte';
	import BotBadge from '$lib/components/bot-badge.svelte';
	import RepoList from '$lib/components/repo-list.svelte';
	import GitRepoList from '$lib/components/git/git-repo-list.svelte';
	import ArtifactRepoList from '$lib/components/artifact-repo-list.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import ActivityFeedView from '$lib/components/activity-feed.svelte';
	import ProfileReadme from '$lib/components/profile-readme.svelte';
	import StarredList from '$lib/components/starred-list.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import { ActivityFeed } from '$lib/proto/distroface/v1/activity_pb';
	import type { User, Repository, Organization } from '$lib/proto/distroface/v1/types_pb';
	import type { GitRepository } from '$lib/proto/distroface/v1/git_pb';

	const username = $derived(page.params.namespace ?? '');
	const isOwnProfile = $derived(authStore.user?.username === username);

	type Tab = 'overview' | 'repositories' | 'images' | 'artifacts' | 'stars' | 'organizations';
	const tab = $derived<Tab>((page.url.searchParams.get('tab') as Tab) || 'overview');
	function tabHref(t: Tab): string {
		const base = resolve('/[namespace]', { namespace: username });
		return t === 'overview' ? base : `${base}?tab=${t}`;
	}

	let user = $state<User | undefined>(undefined);
	let loading = $state(true);
	let notFound = $state(false);

	// Counts shown in the sidebar come from the first page of each list
	let gitRepos = $state<GitRepository[]>([]);
	let gitLoading = $state(true);
	let gitLoaded = $state(false);
	const gitPager = new Pager(20);
	const gitFilter = new QueryFilter([
		{ key: 'name', label: 'Name' },
		{ key: 'description', label: 'Description' }
	]);

	let repos = $state<Repository[]>([]);
	let repoLoading = $state(true);
	let repoLoaded = $state(false);
	const repoPager = new Pager(20);
	const repoFilter = new QueryFilter([
		{ key: 'name', label: 'Name' },
		{ key: 'description', label: 'Description' }
	]);

	let artifactCount = $state(0);
	let orgs = $state<Organization[]>([]);
	let orgsLoaded = $state(false);
	let topRepos = $state<GitRepository[]>([]);

	async function loadUser() {
		loading = true;
		notFound = false;
		// On a portal that maps bare names, a single segment is a repository of its org
		if (
			portalStore.isPortal &&
			portalStore.mapUnqualified &&
			username &&
			username !== portalStore.orgName
		) {
			await goto(
				resolve('/[namespace]/[name]', { namespace: portalStore.orgName, name: username }),
				{
					replaceState: true
				}
			);
			return;
		}
		try {
			const resp = await rpcClient.user.getUser({ username }, silentCallOptions);
			user = resp.user;
		} catch {
			user = undefined;
			try {
				const org = await rpcClient.organization.getOrganization(
					{ name: username },
					silentCallOptions
				);
				if (org.organization) {
					await goto(resolve('/orgs/[name]', { name: username }), { replaceState: true });
					return;
				}
			} catch {
				// Neither a user nor an org
			}
			notFound = true;
		} finally {
			loading = false;
		}
	}

	async function loadGit() {
		gitLoading = true;
		try {
			const resp = await rpcClient.git.listGitRepositories(
				{ page: gitPager.request(gitFilter.request(), 'updated_at desc'), namespace: username },
				silentCallOptions
			);
			gitRepos = resp.repositories;
			gitPager.apply(resp.page);
		} catch {
			gitRepos = [];
			gitPager.apply();
		} finally {
			gitLoading = false;
			gitLoaded = true;
		}
	}

	async function loadRepos() {
		repoLoading = true;
		try {
			const resp = await rpcClient.repository.listRepositories(
				{ page: repoPager.request(repoFilter.request()), namespace: username },
				silentCallOptions
			);
			repos = resp.repositories;
			repoPager.apply(resp.page);
		} catch {
			repos = [];
			repoPager.apply();
		} finally {
			repoLoading = false;
			repoLoaded = true;
		}
	}

	async function loadSide() {
		const results = await Promise.allSettled([
			rpcClient.artifact.listArtifactRepositories(
				{ namespace: username, page: { pageSize: 1, pageToken: '', orderBy: '' } },
				backgroundCallOptions
			),
			authStore.isAuthenticated && !portalStore.isPortal
				? rpcClient.organization.listOrganizations(
						{ username, page: { pageSize: 12, pageToken: '', orderBy: '' } },
						backgroundCallOptions
					)
				: Promise.resolve(undefined),
			configStore.gitEnabled
				? rpcClient.git.listGitRepositories(
						{ namespace: username, page: { pageSize: 4, pageToken: '', orderBy: 'stars desc' } },
						backgroundCallOptions
					)
				: Promise.resolve(undefined)
		]);
		artifactCount =
			results[0].status === 'fulfilled' ? Number(results[0].value.page?.totalCount ?? 0n) : 0;
		orgs = results[1].status === 'fulfilled' ? (results[1].value?.organizations ?? []) : [];
		orgsLoaded = true;
		topRepos = results[2].status === 'fulfilled' ? (results[2].value?.repositories ?? []) : [];
	}

	let loadedFor = '';
	$effect(() => {
		if (!username || username === loadedFor) return;
		loadedFor = username;
		gitPager.reset();
		repoPager.reset();
		void loadUser();
		void loadRepos();
		if (configStore.gitEnabled) void loadGit();
		void loadSide();
	});

	const joined = $derived(user?.createdAt ? relativeTime(timestampDate(user.createdAt)) : '');

	type TabDef = { key: Tab; label: string; icon: typeof Package; count?: number; show: boolean };
	const tabs = $derived<TabDef[]>([
		{ key: 'overview', label: 'Overview', icon: UserIcon, show: true },
		{
			key: 'repositories',
			label: 'Repositories',
			icon: GitBranch,
			count: gitPager.totalCount,
			show: configStore.gitEnabled
		},
		{ key: 'images', label: 'Images', icon: Package, count: repoPager.totalCount, show: true },
		{ key: 'artifacts', label: 'Artifacts', icon: Archive, count: artifactCount, show: true },
		{ key: 'stars', label: 'Stars', icon: Star, show: true },
		{
			key: 'organizations',
			label: 'Organizations',
			icon: Building,
			count: orgs.length,
			show: !portalStore.isPortal && authStore.isAuthenticated
		}
	]);
</script>

<!-- eslint-disable svelte/no-navigation-without-resolve -->
<svelte:head>
	<title>{user?.displayName || username} - Distroface</title>
</svelte:head>

{#if loading}
	<div class="grid gap-8 lg:grid-cols-[17rem_minmax(0,1fr)]">
		<div class="space-y-4">
			<Skeleton class="size-40 rounded-full" />
			<Skeleton class="h-6 w-48" />
			<Skeleton class="h-4 w-32" />
		</div>
		<Skeleton class="h-64 w-full rounded-lg" />
	</div>
{:else if notFound}
	<EmptyState
		icon={UserIcon}
		message="{username} not found"
		description="There is no user or organization by that name."
	>
		{#snippet actions()}
			<Button variant="outline" onclick={() => goto(resolve('/'))}>Back home</Button>
		{/snippet}
	</EmptyState>
{:else if user}
	<div class="grid gap-8 lg:grid-cols-[17rem_minmax(0,1fr)]">
		<!-- Identity column -->
		<aside class="space-y-5">
			<div class="flex items-center gap-4 lg:block">
				<UserAvatar
					username={user.username}
					displayName={user.displayName}
					avatarUrl={user.avatarUrl}
					class="size-20 ring-1 ring-border lg:size-40"
					fallbackClass="text-2xl lg:text-5xl"
				/>
				<div class="min-w-0 lg:mt-4">
					{#if user.displayName}
						<h1 class="truncate text-xl leading-7 font-semibold tracking-tight">
							{user.displayName}
						</h1>
						<p class="truncate text-base text-muted-foreground">
							{user.username}{#if user.isAgent}<BotBadge class="ml-1.5 align-middle" />{/if}
						</p>
					{:else}
						<h1 class="truncate text-xl leading-7 font-semibold tracking-tight">
							{user.username}{#if user.isAgent}<BotBadge class="ml-1.5 align-middle" />{/if}
						</h1>
					{/if}
				</div>
			</div>

			{#if isOwnProfile}
				<Button variant="outline" class="w-full" href={resolve('/settings/profile')}>
					<Settings />
					Edit profile
				</Button>
			{/if}

			{#if user.roles.length > 0}
				<div class="flex flex-wrap gap-1">
					{#each user.roles as role (role.id)}
						<Badge variant="outline">{role.name}</Badge>
					{/each}
				</div>
			{/if}

			<dl class="space-y-1.5 text-sm text-muted-foreground">
				{#if joined}
					<div class="flex items-center gap-2">
						<Calendar class="size-3.5 shrink-0" />
						<dd>Joined {joined}</dd>
					</div>
				{/if}
				{#if user.authProvider && user.authProvider !== 'local'}
					<div class="flex items-center gap-2">
						<Users class="size-3.5 shrink-0" />
						<dd>Signs in with {user.authProvider}</dd>
					</div>
				{/if}
			</dl>

			<div class="grid grid-cols-3 gap-2 lg:grid-cols-1 lg:gap-1">
				{#if configStore.gitEnabled}
					<a
						href={tabHref('repositories')}
						class="flex items-center gap-2 rounded-md px-2 py-1 text-sm hover:bg-muted"
					>
						<GitBranch class="size-3.5 text-muted-foreground" />
						<span class="tabular-nums">{gitPager.totalCount}</span>
						<span class="text-muted-foreground">repositories</span>
					</a>
				{/if}
				<a
					href={tabHref('images')}
					class="flex items-center gap-2 rounded-md px-2 py-1 text-sm hover:bg-muted"
				>
					<Package class="size-3.5 text-muted-foreground" />
					<span class="tabular-nums">{repoPager.totalCount}</span>
					<span class="text-muted-foreground">images</span>
				</a>
				<a
					href={tabHref('artifacts')}
					class="flex items-center gap-2 rounded-md px-2 py-1 text-sm hover:bg-muted"
				>
					<Archive class="size-3.5 text-muted-foreground" />
					<span class="tabular-nums">{artifactCount}</span>
					<span class="text-muted-foreground">artifacts</span>
				</a>
			</div>

			{#if orgsLoaded && orgs.length > 0}
				<section class="space-y-2">
					<h2 class="eyebrow">Organizations</h2>
					<div class="flex flex-wrap gap-1.5">
						{#each orgs as o (o.id)}
							<a
								href={resolve('/orgs/[name]', { name: o.name })}
								title={o.displayName || o.name}
								class="flex size-8 items-center justify-center rounded-md border bg-muted text-xs font-semibold text-muted-foreground uppercase transition-colors hover:border-border-strong hover:text-foreground"
								>{o.name.slice(0, 2)}</a
							>
						{/each}
					</div>
				</section>
			{/if}
		</aside>

		<!-- Content column -->
		<div class="min-w-0 space-y-5">
			<nav
				class="scrollbar-none flex items-center gap-0.5 overflow-x-auto border-b"
				aria-label="Profile sections"
			>
				{#each tabs.filter((t) => t.show) as t (t.key)}
					{@const Icon = t.icon}
					<a
						href={tabHref(t.key)}
						aria-current={tab === t.key ? 'page' : undefined}
						class={cn(
							'-mb-px flex shrink-0 items-center gap-1.5 border-b-2 px-3 py-2 text-sm font-medium whitespace-nowrap transition-colors',
							tab === t.key
								? 'border-primary text-foreground'
								: 'border-transparent text-muted-foreground hover:text-foreground'
						)}
					>
						<Icon class="size-4 opacity-80" />
						{t.label}
						{#if t.count}
							<span class="rounded-full bg-muted px-1.5 text-xs text-muted-foreground tabular-nums"
								>{t.count}</span
							>
						{/if}
					</a>
				{/each}
			</nav>

			{#if tab === 'overview'}
				<div class="space-y-6">
					{#if configStore.gitEnabled}
						<ProfileReadme namespace={username} isOwn={isOwnProfile} />
					{/if}

					{#if topRepos.length > 0}
						<section class="space-y-2">
							<div class="flex items-center justify-between">
								<h2 class="text-sm font-semibold">Popular repositories</h2>
								<a
									href={tabHref('repositories')}
									class="text-xs text-muted-foreground hover:text-primary">All</a
								>
							</div>
							<div class="grid gap-3 sm:grid-cols-2">
								{#each topRepos as r (r.id)}
									<a
										href={resolve('/[namespace]/[name]', { namespace: r.namespace, name: r.name })}
										class="card-interactive block p-3"
									>
										<div class="flex items-center justify-between gap-2">
											<span class="flex min-w-0 items-center gap-1.5 text-sm font-semibold">
												<GitBranch class="size-3.5 shrink-0 text-muted-foreground" />
												<span class="truncate">{r.name}</span>
											</span>
											<Badge variant="outline" class="shrink-0"
												>{r.visibility === 2 ? 'Private' : 'Public'}</Badge
											>
										</div>
										<p class="mt-1 line-clamp-2 min-h-[2.5rem] text-xs text-muted-foreground">
											{r.description || 'No description'}
										</p>
										<div class="mt-2 flex items-center gap-3 text-xs text-muted-foreground">
											{#if Number(r.starCount) > 0}
												<span class="flex items-center gap-1 tabular-nums"
													><Star class="size-3" />{Number(r.starCount)}</span
												>
											{/if}
											{#if r.updatedAt}
												<span>Updated {relativeTime(timestampDate(r.updatedAt))}</span>
											{/if}
										</div>
									</a>
								{/each}
							</div>
						</section>
					{/if}

					<section class="space-y-2">
						<h2 class="flex items-center gap-1.5 text-sm font-semibold">
							<Activity class="size-4 text-muted-foreground" />Recent activity
						</h2>
						<ActivityFeedView
							feed={ActivityFeed.ACTOR}
							actor={username}
							pageSize={15}
							emptyMessage="{isOwnProfile ? 'You have' : `${username} has`} no public activity yet"
							emptyDescription="Pushes, releases, issues, stars, and uploads land here."
						/>
					</section>
				</div>
			{:else if tab === 'repositories'}
				<div class="space-y-4">
					<div class="flex flex-wrap items-center justify-between gap-3">
						<div class="max-w-md flex-1">
							<QueryFilterBar
								filter={gitFilter}
								placeholder="Find a repository"
								onchange={() => {
									gitPager.reset();
									loadGit();
								}}
							/>
						</div>
						{#if isOwnProfile}
							<Button href={`${resolve('/git/new')}?namespace=${encodeURIComponent(username)}`}
								><GitBranch />New</Button
							>
						{/if}
					</div>
					<GitRepoList
						repos={gitRepos}
						pager={gitPager}
						onChange={loadGit}
						loading={gitLoading}
						loaded={gitLoaded}
						showNamespace={false}
						emptyMessage={gitFilter.active ? 'No repositories match' : 'No git repositories yet'}
					/>
				</div>
			{:else if tab === 'images'}
				<div class="space-y-4">
					<div class="flex flex-wrap items-center justify-between gap-3">
						<div class="max-w-md flex-1">
							<QueryFilterBar
								filter={repoFilter}
								placeholder="Find an image"
								onchange={() => {
									repoPager.reset();
									loadRepos();
								}}
							/>
						</div>
						{#if isOwnProfile}
							<Button href={`${resolve('/images/new')}?namespace=${encodeURIComponent(username)}`}
								><Package />New</Button
							>
						{/if}
					</div>
					<RepoList
						{repos}
						pager={repoPager}
						onChange={loadRepos}
						loading={repoLoading}
						loaded={repoLoaded}
						emptyMessage={repoFilter.active ? 'No images match' : 'No image repositories yet'}
					/>
				</div>
			{:else if tab === 'artifacts'}
				<ArtifactRepoList namespace={username} canCreate={isOwnProfile} />
			{:else if tab === 'stars'}
				<StarredList {username} isOwn={isOwnProfile} />
			{:else if tab === 'organizations'}
				{#if !orgsLoaded}
					<Skeleton class="h-32 w-full rounded-lg" />
				{:else if orgs.length === 0}
					<EmptyState
						icon={Building}
						message="No shared organizations"
						description={isOwnProfile
							? 'You do not belong to any organization yet.'
							: `You and ${username} share no organizations.`}
					/>
				{:else}
					<div class="box divide-y">
						{#each orgs as o (o.id)}
							<a
								href={resolve('/orgs/[name]', { name: o.name })}
								class="flex items-center gap-3 px-4 py-3 transition-colors hover:bg-muted/30"
							>
								<span
									class="flex size-9 shrink-0 items-center justify-center rounded-md border bg-muted text-sm font-semibold text-muted-foreground uppercase"
									>{o.name.slice(0, 2)}</span
								>
								<span class="min-w-0 flex-1">
									<span class="block truncate text-sm font-semibold">{o.displayName || o.name}</span
									>
									{#if o.description}
										<span class="block truncate text-xs text-muted-foreground">{o.description}</span
										>
									{/if}
								</span>
								<span
									class="flex shrink-0 items-center gap-1 text-xs text-muted-foreground tabular-nums"
									><Users class="size-3.5" />{o.memberCount}</span
								>
							</a>
						{/each}
					</div>
				{/if}
			{/if}
		</div>
	</div>
{/if}
<!-- eslint-enable svelte/no-navigation-without-resolve -->
