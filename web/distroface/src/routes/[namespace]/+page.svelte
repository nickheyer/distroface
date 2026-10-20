<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { UserRound, Calendar, Building2, Settings } from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import { Separator } from '$lib/components/ui/separator';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar';
	import RepoList from '$lib/components/repo-list.svelte';
	import GitRepoList from '$lib/components/git/git-repo-list.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import type { User, Repository } from '$lib/proto/distroface/v1/types_pb';
	import type { GitRepository } from '$lib/proto/distroface/v1/git_pb';

	const username = $derived(page.params.namespace);

	let user = $state<User | undefined>(undefined);
	let loading = $state(true);
	let repos = $state<Repository[]>([]);
	let repoLoading = $state(true);
	const repoPager = new Pager(20);

	let gitRepos = $state<GitRepository[]>([]);
	let gitLoading = $state(true);
	const gitPager = new Pager(20);

	async function loadGitRepos() {
		gitLoading = true;
		try {
			const resp = await rpcClient.git.listGitRepositories(
				{ page: gitPager.request(undefined, 'updated_at desc'), namespace: username },
				silentCallOptions
			);
			gitRepos = resp.repositories;
			gitPager.apply(resp.page);
		} catch {
			gitRepos = [];
		} finally {
			gitLoading = false;
		}
	}

	let starred = $state<Repository[]>([]);
	let starredLoading = $state(true);
	const starredPager = new Pager(20);

	const isOwnProfile = $derived(authStore.user?.username === username);

	function getInitials(u: User): string {
		const name = u.displayName || u.username;
		return name
			.split(/[\s-]+/)
			.map((w) => w[0])
			.join('')
			.toUpperCase()
			.slice(0, 2);
	}

	async function loadUser() {
		loading = true;
		// On a portal that maps bare names, a single segment is a repository of its org
		if (
			portalStore.isPortal &&
			portalStore.mapUnqualified &&
			username &&
			username !== portalStore.orgName
		) {
			await goto(
				resolve('/[namespace]/[name]', { namespace: portalStore.orgName, name: username }),
				{ replaceState: true }
			);
			return;
		}
		try {
			const resp = await rpcClient.user.getUser({ username });
			user = resp.user;
		} catch {
			user = undefined;
			try {
				const org = await rpcClient.organization.getOrganization(
					{ name: username },
					silentCallOptions
				);
				if (org.organization) {
					await goto(resolve('/orgs/[name]', { name: username ?? '' }));
					return;
				}
			} catch {
				// Neither a user nor an org, the not found state renders
			}
		} finally {
			loading = false;
		}
	}

	async function loadRepos() {
		repoLoading = true;
		try {
			const resp = await rpcClient.repository.listRepositories({
				page: repoPager.request(),
				namespace: username
			});
			repos = resp.repositories;
			repoPager.apply(resp.page);
		} catch {
			repos = [];
		} finally {
			repoLoading = false;
		}
	}

	async function loadStarred() {
		starredLoading = true;
		try {
			const resp = await rpcClient.repository.listStarredRepositories({
				page: starredPager.request()
			});
			starred = resp.repositories;
			starredPager.apply(resp.page);
		} catch {
			starred = [];
		} finally {
			starredLoading = false;
		}
	}

	onMount(() => {
		loadUser();
		loadRepos();
	});

	$effect(() => {
		if (configStore.gitEnabled) loadGitRepos();
	});

	$effect(() => {
		if (isOwnProfile) loadStarred();
	});
</script>

<div class="space-y-6">
	{#if loading}
		<div class="flex items-center gap-4">
			<Skeleton class="size-14 rounded-full" />
			<div class="flex-1 space-y-2">
				<Skeleton class="h-6 w-48" />
				<Skeleton class="h-4 w-32" />
			</div>
		</div>
	{:else if user}
		<div class="flex flex-wrap items-center gap-4">
			<Avatar class="size-14">
				<AvatarFallback class="text-lg">{getInitials(user)}</AvatarFallback>
			</Avatar>
			<div class="min-w-0 flex-1 space-y-1">
				<div class="flex flex-wrap items-center gap-2">
					<h1 class="text-2xl font-semibold tracking-tight">{user.username}</h1>
					{#each user.roles as role (role.id)}
						<Badge variant="outline">{role.name}</Badge>
					{/each}
				</div>
				<div class="flex flex-wrap items-center gap-3 text-sm text-muted-foreground">
					{#if user.displayName}
						<span>{user.displayName}</span>
					{/if}
					{#if user.createdAt}
						<span class="flex items-center gap-1">
							<Calendar class="size-3.5" />
							Joined {relativeTime(timestampDate(user.createdAt))}
						</span>
					{/if}
				</div>
			</div>
			{#if isOwnProfile}
				<div class="flex shrink-0 flex-wrap gap-2">
					<Button variant="outline" href={resolve('/orgs')}>
						<Building2 />
						Organizations
					</Button>
					<Button variant="outline" href={resolve('/settings/profile')}>
						<Settings />
						Edit profile
					</Button>
				</div>
			{/if}
		</div>
	{:else}
		<div class="flex items-center gap-4">
			<div
				class="flex size-14 items-center justify-center rounded-full border bg-muted text-muted-foreground"
			>
				<UserRound class="size-6" />
			</div>
			<div>
				<h1 class="text-2xl font-semibold tracking-tight">{username}</h1>
				<p class="text-sm text-muted-foreground">User not found</p>
			</div>
		</div>
	{/if}

	<Separator />

	<div class="space-y-4">
		<div class="section-header">
			<h2 class="section-title">Image repositories</h2>
			{#if repoPager.totalCount > 0}
				<span class="text-xs text-muted-foreground tabular-nums">{repoPager.totalCount} total</span>
			{/if}
		</div>

		<RepoList
			{repos}
			pager={repoPager}
			onChange={loadRepos}
			loading={repoLoading}
			emptyMessage="No image repositories yet"
		/>
	</div>

	{#if configStore.gitEnabled}
		<div class="space-y-4">
			<div class="section-header">
				<h2 class="section-title">Git</h2>
				{#if gitPager.totalCount > 0}
					<span class="text-xs text-muted-foreground tabular-nums">{gitPager.totalCount} total</span
					>
				{/if}
			</div>

			<GitRepoList
				repos={gitRepos}
				pager={gitPager}
				onChange={loadGitRepos}
				loading={gitLoading}
				showNamespace={false}
				emptyMessage="No git repositories yet"
			/>
		</div>
	{/if}

	{#if isOwnProfile}
		<div class="space-y-4">
			<div class="section-header">
				<h2 class="section-title">Starred</h2>
				{#if starredPager.totalCount > 0}
					<span class="text-xs text-muted-foreground tabular-nums"
						>{starredPager.totalCount} total</span
					>
				{/if}
			</div>

			<RepoList
				repos={starred}
				pager={starredPager}
				onChange={loadStarred}
				loading={starredLoading}
				emptyMessage="No starred repositories"
				emptyDescription="Star repositories to keep track of them here."
			/>
		</div>
	{/if}
</div>
