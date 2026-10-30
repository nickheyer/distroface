<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { goto } from '$app/navigation';
	import { rpcClient, backgroundCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		DropdownMenu,
		DropdownMenuContent,
		DropdownMenuItem,
		DropdownMenuTrigger
	} from '$lib/components/ui/dropdown-menu';
	import UserAvatar from '$lib/components/user-avatar.svelte';
	import ActivityFeedView from '$lib/components/activity-feed.svelte';
	import { activityGroups } from '$lib/activity';
	import {
		Activity,
		Archive,
		Building,
		ChevronDown,
		Container,
		GitBranch,
		LogIn,
		Package,
		Plus,
		RefreshCw,
		Rocket,
		Star,
		Users
	} from '$lib/icons';
	import { ActivityFeed } from '$lib/proto/distroface/v1/activity_pb';
	import type { GitRepository } from '$lib/proto/distroface/v1/git_pb';
	import type { Organization, Repository } from '$lib/proto/distroface/v1/types_pb';
	import type { SyncEvent } from '$lib/proto/distroface/v1/mirror_pb';
	import { cn } from '$lib/utils';

	const signedIn = $derived(authStore.isAuthenticated);
	const me = $derived(authStore.user);
	const gitOn = $derived(configStore.gitEnabled);

	let everyone = $state(false);
	const feed = $derived(signedIn && !everyone ? ActivityFeed.HOME : ActivityFeed.PUBLIC);
	const home = $derived(feed === ActivityFeed.HOME);
	const scopes = [
		{ everyone: false, label: 'For you' },
		{ everyone: true, label: 'Everyone' }
	];

	let group = $state('');
	const kinds = $derived(activityGroups.find((g) => g.key === group)?.kinds ?? []);

	let feedView = $state<ReturnType<typeof ActivityFeedView> | undefined>();

	// Left rail
	let myGit = $state<GitRepository[]>([]);
	let myImages = $state<Repository[]>([]);
	let myOrgs = $state<Organization[]>([]);
	let railLoaded = $state(false);

	// Right rail
	let popular = $state<GitRepository[]>([]);
	let popularLoaded = $state(false);

	async function loadRails() {
		const username = me?.username ?? '';
		const results = await Promise.allSettled([
			gitOn && username
				? rpcClient.git.listGitRepositories(
						{
							namespace: username,
							page: { pageSize: 6, pageToken: '', orderBy: 'updated_at desc' }
						},
						backgroundCallOptions
					)
				: Promise.resolve(undefined),
			username
				? rpcClient.repository.listRepositories(
						{ namespace: username, page: { pageSize: 4, pageToken: '', orderBy: '' } },
						backgroundCallOptions
					)
				: Promise.resolve(undefined),
			username && !portalStore.isPortal
				? rpcClient.organization.listOrganizations(
						{ page: { pageSize: 8, pageToken: '', orderBy: '' } },
						backgroundCallOptions
					)
				: Promise.resolve(undefined),
			gitOn
				? rpcClient.git.listGitRepositories(
						{ page: { pageSize: 5, pageToken: '', orderBy: 'stars desc' } },
						backgroundCallOptions
					)
				: Promise.resolve(undefined)
		]);
		myGit = results[0].status === 'fulfilled' ? (results[0].value?.repositories ?? []) : [];
		myImages = results[1].status === 'fulfilled' ? (results[1].value?.repositories ?? []) : [];
		myOrgs = results[2].status === 'fulfilled' ? (results[2].value?.organizations ?? []) : [];
		popular =
			results[3].status === 'fulfilled'
				? (results[3].value?.repositories ?? []).filter((r) => Number(r.starCount) > 0)
				: [];
		railLoaded = true;
		popularLoaded = true;
	}

	onMount(() => {
		mirrorSyncStore.ensure();
		void loadRails();
	});

	const liveSyncs = $derived(Object.values(mirrorSyncStore.activeEvents) as SyncEvent[]);

	function syncHref(ev: SyncEvent): string {
		switch (ev.kind) {
			case 'image':
				return resolve('/images/[namespace]/[...name]', { namespace: ev.namespace, name: ev.name });
			case 'artifact':
				return resolve('/artifacts/[namespace]/[repo]', { namespace: ev.namespace, repo: ev.name });
			default:
				return resolve('/[namespace]/[name]', { namespace: ev.namespace, name: ev.name });
		}
	}

	function syncLabel(kind: string): string {
		switch (kind) {
			case 'image':
				return 'Image mirror';
			case 'artifact':
				return 'Release mirror';
			case 'import':
				return 'Import';
			case 'git_push':
				return 'Push mirror';
			default:
				return 'Git mirror';
		}
	}

	const greeting = $derived.by(() => {
		const h = new Date().getHours();
		if (h < 5) return 'Still up';
		if (h < 12) return 'Good morning';
		if (h < 18) return 'Good afternoon';
		return 'Good evening';
	});
</script>

<!-- eslint-disable svelte/no-navigation-without-resolve -->
<svelte:head>
	<title>Home - {portalStore.isPortal ? portalStore.displayName : 'Distroface'}</title>
</svelte:head>

<div class="grid gap-6 lg:grid-cols-[15rem_minmax(0,1fr)] xl:grid-cols-[15rem_minmax(0,1fr)_17rem]">
	<!-- Left rail: your work -->
	<aside class="space-y-5 lg:sticky lg:top-20 lg:self-start">
		{#if signedIn && me}
			<div class="flex items-center gap-3">
				<a href={resolve('/[namespace]', { namespace: me.username })}>
					<UserAvatar
						username={me.username}
						displayName={me.displayName}
						avatarUrl={me.avatarUrl}
						class="size-10"
					/>
				</a>
				<div class="min-w-0">
					<p class="text-xs text-muted-foreground">{greeting},</p>
					<a
						href={resolve('/[namespace]', { namespace: me.username })}
						class="block truncate text-sm font-semibold hover:text-primary"
						>{me.displayName || me.username}</a
					>
				</div>
			</div>

			<DropdownMenu>
				<DropdownMenuTrigger>
					{#snippet child({ props })}
						<Button {...props} class="w-full"
							><Plus />New<ChevronDown class="ml-auto opacity-70" /></Button
						>
					{/snippet}
				</DropdownMenuTrigger>
				<DropdownMenuContent align="start" class="w-56">
					{#if gitOn}
						<DropdownMenuItem onclick={() => goto(resolve('/git/new'))}>
							<GitBranch />Git repository
						</DropdownMenuItem>
					{/if}
					<DropdownMenuItem onclick={() => goto(resolve('/images/new'))}>
						<Package />Image repository
					</DropdownMenuItem>
					<DropdownMenuItem onclick={() => goto(resolve('/artifacts/new'))}>
						<Archive />Artifact repository
					</DropdownMenuItem>
					{#if authStore.canCreateOrgs && !portalStore.isPortal}
						<DropdownMenuItem onclick={() => goto(resolve('/orgs/new'))}>
							<Building />Organization
						</DropdownMenuItem>
					{/if}
				</DropdownMenuContent>
			</DropdownMenu>

			{#if gitOn}
				<section class="space-y-1.5">
					<div class="flex items-center justify-between">
						<h2 class="eyebrow">Repositories</h2>
						<a href={resolve('/git')} class="text-xs text-muted-foreground hover:text-primary"
							>All</a
						>
					</div>
					{#if !railLoaded}
						<Skeleton class="h-20 w-full" />
					{:else if myGit.length === 0}
						<p class="text-xs text-muted-foreground">
							Nothing under {me.username} yet.
							<a href={resolve('/git/new')} class="link">Create one</a>
						</p>
					{:else}
						<ul class="space-y-0.5">
							{#each myGit as r (r.id)}
								<li>
									<a
										href={resolve('/[namespace]/[name]', { namespace: r.namespace, name: r.name })}
										class="flex items-center gap-2 rounded-md px-2 py-1 text-sm hover:bg-muted"
									>
										<GitBranch class="size-3.5 shrink-0 text-muted-foreground" />
										<span class="truncate">{r.name}</span>
									</a>
								</li>
							{/each}
						</ul>
					{/if}
				</section>
			{/if}

			<section class="space-y-1.5">
				<div class="flex items-center justify-between">
					<h2 class="eyebrow">Images</h2>
					<a href={resolve('/images')} class="text-xs text-muted-foreground hover:text-primary"
						>All</a
					>
				</div>
				{#if !railLoaded}
					<Skeleton class="h-12 w-full" />
				{:else if myImages.length === 0}
					<p class="text-xs text-muted-foreground">No images pushed under {me.username} yet.</p>
				{:else}
					<ul class="space-y-0.5">
						{#each myImages as r (r.id)}
							<li>
								<a
									href={resolve('/images/[namespace]/[...name]', {
										namespace: r.namespace,
										name: r.name
									})}
									class="flex items-center gap-2 rounded-md px-2 py-1 text-sm hover:bg-muted"
								>
									<Container class="size-3.5 shrink-0 text-muted-foreground" />
									<span class="truncate">{r.name}</span>
								</a>
							</li>
						{/each}
					</ul>
				{/if}
			</section>

			{#if !portalStore.isPortal}
				<section class="space-y-1.5">
					<div class="flex items-center justify-between">
						<h2 class="eyebrow">Organizations</h2>
						<a href={resolve('/orgs')} class="text-xs text-muted-foreground hover:text-primary"
							>All</a
						>
					</div>
					{#if !railLoaded}
						<Skeleton class="h-8 w-full" />
					{:else if myOrgs.length === 0}
						<p class="text-xs text-muted-foreground">You belong to no organizations.</p>
					{:else}
						<ul class="space-y-0.5">
							{#each myOrgs as o (o.id)}
								<li>
									<a
										href={resolve('/orgs/[name]', { name: o.name })}
										class="flex items-center gap-2 rounded-md px-2 py-1 text-sm hover:bg-muted"
									>
										<span
											class="flex size-5 shrink-0 items-center justify-center rounded border bg-muted text-2xs font-semibold text-muted-foreground uppercase"
											>{o.name.slice(0, 2)}</span
										>
										<span class="truncate">{o.displayName || o.name}</span>
									</a>
								</li>
							{/each}
						</ul>
					{/if}
				</section>
			{/if}
		{:else}
			<div class="rounded-lg border bg-card p-4">
				<p class="text-sm font-semibold">
					Welcome to {portalStore.isPortal ? portalStore.displayName : 'Distroface'}
				</p>
				<p class="mt-1 text-sm text-muted-foreground">
					Sign in to follow your repositories, star what matters, and see your own feed here.
				</p>
				<Button class="mt-3 w-full" href={resolve('/login')}><LogIn />Sign in</Button>
			</div>
			<nav class="space-y-0.5">
				{#if gitOn}
					<a
						href={resolve('/git')}
						class="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted"
					>
						<GitBranch class="size-4 text-muted-foreground" />Browse git repositories
					</a>
				{/if}
				<a
					href={resolve('/images')}
					class="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted"
				>
					<Package class="size-4 text-muted-foreground" />Browse images
				</a>
				<a
					href={resolve('/artifacts')}
					class="flex items-center gap-2 rounded-md px-2 py-1.5 text-sm hover:bg-muted"
				>
					<Archive class="size-4 text-muted-foreground" />Browse artifacts
				</a>
			</nav>
		{/if}
	</aside>

	<!-- Center: the feed -->
	<main class="min-w-0 space-y-4">
		<div class="border-b pb-3">
			<div class="flex items-center justify-between gap-3">
				<h1 class="flex items-center gap-2 text-xl leading-7 font-semibold tracking-tight">
					<Activity class="size-5 text-muted-foreground" />
					Home
				</h1>
				<Button
					variant="ghost-muted"
					size="icon-sm"
					class="shrink-0"
					aria-label="Refresh"
					onclick={() => feedView?.refresh()}
				>
					<RefreshCw />
				</Button>
			</div>
			<div class="mt-1 flex items-center justify-between gap-3">
				<p class="text-sm text-muted-foreground">
					{home ? 'Your personalized activity feed' : 'The public activity feed'}
				</p>
				{#if signedIn}
					<nav
						class="flex shrink-0 items-center gap-0.5 rounded-md border bg-muted/40 p-0.5"
						aria-label="Feed"
					>
						{#each scopes as s (s.label)}
							<button
								type="button"
								onclick={() => (everyone = s.everyone)}
								aria-pressed={everyone === s.everyone}
								class={cn(
									'rounded px-2.5 py-1 text-xs font-medium transition-colors',
									everyone === s.everyone
										? 'bg-background text-foreground shadow-xs'
										: 'text-muted-foreground hover:text-foreground'
								)}>{s.label}</button
							>
						{/each}
					</nav>
				{/if}
			</div>
		</div>

		<div class="scrollbar-none flex items-center gap-1.5 overflow-x-auto">
			<button
				type="button"
				onclick={() => (group = '')}
				class={cn(
					'shrink-0 rounded-full border px-2.5 py-0.5 text-xs font-medium transition-colors',
					group === ''
						? 'border-primary bg-primary/10 text-primary'
						: 'text-muted-foreground hover:text-foreground'
				)}>All</button
			>
			{#each activityGroups as g (g.key)}
				<button
					type="button"
					onclick={() => (group = group === g.key ? '' : g.key)}
					class={cn(
						'shrink-0 rounded-full border px-2.5 py-0.5 text-xs font-medium transition-colors',
						group === g.key
							? 'border-primary bg-primary/10 text-primary'
							: 'text-muted-foreground hover:text-foreground'
					)}>{g.label}</button
				>
			{/each}
		</div>

		{#key `${feed}:${group}`}
			<ActivityFeedView
				bind:this={feedView}
				{feed}
				{kinds}
				poll={60}
				emptyMessage={home ? 'Your feed is quiet' : 'Nothing has happened yet'}
				emptyDescription={home
					? 'Star repositories, join organizations, or push something and it shows up here.'
					: 'Pushes, releases, issues, stars, and uploads show up here as they happen.'}
			/>
		{/key}
	</main>

	<!-- Right rail: what is happening -->
	<aside class="hidden space-y-6 xl:sticky xl:top-20 xl:block xl:self-start">
		{#if signedIn}
			<section class="space-y-2">
				<h2 class="eyebrow flex items-center gap-1.5">
					<span class="relative flex size-2">
						{#if liveSyncs.length > 0}
							<span
								class="absolute inline-flex size-full animate-ping rounded-full bg-success opacity-75"
							></span>
						{/if}
						<span
							class="relative inline-flex size-2 rounded-full {liveSyncs.length > 0
								? 'bg-success'
								: 'bg-border'}"
						></span>
					</span>
					Live
				</h2>
				{#if liveSyncs.length === 0}
					<p class="text-xs text-muted-foreground">No mirror syncs or imports running right now.</p>
				{:else}
					<ul class="space-y-1">
						{#each liveSyncs as ev (`${ev.kind}:${ev.repoId}`)}
							<li>
								<a
									href={syncHref(ev)}
									class="block rounded-md border bg-card px-2.5 py-2 hover:bg-muted/30"
								>
									<span class="flex items-center justify-between gap-2">
										<span class="truncate text-sm font-medium">{ev.namespace}/{ev.name}</span>
										<Badge variant="outline" class="shrink-0">{syncLabel(ev.kind)}</Badge>
									</span>
									{#if mirrorSyncStore.messages[`${ev.kind}:${ev.repoId}`]}
										<span class="mt-0.5 block truncate text-xs text-muted-foreground"
											>{mirrorSyncStore.messages[`${ev.kind}:${ev.repoId}`]}</span
										>
									{/if}
								</a>
							</li>
						{/each}
					</ul>
				{/if}
			</section>
		{/if}

		{#if gitOn}
			<section class="space-y-2">
				<h2 class="eyebrow flex items-center gap-1.5"><Star class="size-3.5" />Most starred</h2>
				{#if !popularLoaded}
					<Skeleton class="h-24 w-full" />
				{:else if popular.length === 0}
					<p class="text-xs text-muted-foreground">No stars given yet. Be the first.</p>
				{:else}
					<ul class="divide-y rounded-lg border bg-card">
						{#each popular as r (r.id)}
							<li>
								<a
									href={resolve('/[namespace]/[name]', { namespace: r.namespace, name: r.name })}
									class="block px-3 py-2 hover:bg-muted/30"
								>
									<span class="flex items-center justify-between gap-2 text-sm">
										<span class="truncate"
											><span class="text-muted-foreground">{r.namespace}/</span><span
												class="font-medium">{r.name}</span
											></span
										>
										<span
											class="flex shrink-0 items-center gap-1 text-xs text-muted-foreground tabular-nums"
											><Star class="size-3" />{Number(r.starCount)}</span
										>
									</span>
									{#if r.description}
										<span class="mt-0.5 line-clamp-1 block text-xs text-muted-foreground"
											>{r.description}</span
										>
									{/if}
								</a>
							</li>
						{/each}
					</ul>
				{/if}
			</section>
		{/if}

		<section class="space-y-2">
			<h2 class="eyebrow flex items-center gap-1.5"><Rocket class="size-3.5" />Latest releases</h2>
			<ActivityFeedView
				feed={ActivityFeed.PUBLIC}
				kinds={['release']}
				pageSize={5}
				compact
				emptyMessage="No releases published yet"
			/>
		</section>

		{#if !signedIn}
			<section class="space-y-2">
				<h2 class="eyebrow flex items-center gap-1.5"><Users class="size-3.5" />Community</h2>
				<p class="text-xs text-muted-foreground">
					Everything public on this instance flows through the feed. Sign in to star and follow.
				</p>
			</section>
		{/if}
	</aside>
</div>
<!-- eslint-enable svelte/no-navigation-without-resolve -->
