<script lang="ts">
	import { page } from '$app/state';
	import { onMount } from 'svelte';
	import { UserRound, Calendar, Building2, Settings } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { pageToToken, relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import { Separator } from '$lib/components/ui/separator';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar';
	import RepoList from '$lib/components/repo-list.svelte';
	import type { User, Repository } from '$lib/proto/distroface/v1/types_pb';

	const username = $derived(page.params.username);

	let user = $state<User | undefined>(undefined);
	let loading = $state(true);
	let repos = $state<Repository[]>([]);
	let repoLoading = $state(true);
	let repoTotalCount = $state(0);
	let repoPage = $state(1);
	const repoPageSize = 20;

	const isOwnProfile = $derived(authStore.user?.username === username);

	function getInitials(u: User): string {
		const name = u.displayName || u.username;
		return name.split(/[\s-]+/).map((w) => w[0]).join('').toUpperCase().slice(0, 2);
	}

	async function loadUser() {
		loading = true;
		try {
			const resp = await rpcClient.user.getUser({ username });
			user = resp.user;
		} catch {
			user = undefined;
		} finally {
			loading = false;
		}
	}

	async function loadRepos() {
		repoLoading = true;
		try {
			const resp = await rpcClient.repository.listRepositories({
				namespace: username,
				pageSize: repoPageSize,
				pageToken: pageToToken(repoPage, repoPageSize)
			});
			repos = resp.repositories;
			repoTotalCount = resp.totalCount;
		} catch {
			repos = [];
		} finally {
			repoLoading = false;
		}
	}

	function handlePageChange(newPage: number) {
		repoPage = newPage;
		loadRepos();
	}

	onMount(() => { loadUser(); loadRepos(); });
</script>

<div class="space-y-6">
	{#if loading}
		<div class="flex items-center gap-4">
			<Skeleton class="h-16 w-16 rounded-full" />
			<div class="space-y-2 flex-1">
				<Skeleton class="h-7 w-48" />
				<Skeleton class="h-4 w-32" />
			</div>
		</div>
	{:else if user}
		<div class="flex items-center gap-4">
			<Avatar class="h-16 w-16">
				<AvatarFallback class="text-xl bg-primary/10 text-primary font-bold">
					{getInitials(user)}
				</AvatarFallback>
			</Avatar>
			<div class="space-y-1 flex-1 min-w-0">
				<div class="flex items-center gap-2.5">
					<h1 class="text-2xl font-bold tracking-tight">{user.username}</h1>
					{#each user.roles as role}
						<Badge variant="outline" class="text-xs">{role}</Badge>
					{/each}
				</div>
				<div class="flex items-center gap-3 text-sm text-muted-foreground">
					{#if user.displayName}
						<span>{user.displayName}</span>
					{/if}
					{#if user.createdAt}
						<span class="flex items-center gap-1">
							<Calendar class="h-3.5 w-3.5" />
							Joined {relativeTime(timestampDate(user.createdAt))}
						</span>
					{/if}
				</div>
			</div>
			{#if isOwnProfile}
				<div class="flex gap-2 shrink-0">
					<a href="/orgs">
						<Button variant="outline" size="sm">
							<Building2 class="h-4 w-4 mr-1.5" />Organizations
						</Button>
					</a>
					<a href="/settings/profile">
						<Button variant="outline" size="sm">
							<Settings class="h-4 w-4 mr-1.5" />Edit Profile
						</Button>
					</a>
				</div>
			{/if}
		</div>
	{:else}
		<div class="flex items-center gap-4">
			<div class="h-16 w-16 rounded-full bg-muted flex items-center justify-center">
				<UserRound class="h-8 w-8 text-muted-foreground" />
			</div>
			<div>
				<h1 class="text-2xl font-bold tracking-tight">{username}</h1>
				<p class="text-[13px] text-muted-foreground">User not found</p>
			</div>
		</div>
	{/if}

	<Separator />

	<div class="space-y-4">
		<div class="section-header">
			<h2 class="section-title">Repositories</h2>
			{#if repoTotalCount > 0}
				<span class="text-[12px] text-muted-foreground/60 tabular-nums">{repoTotalCount} total</span>
			{/if}
		</div>

		<RepoList
			{repos}
			totalCount={repoTotalCount}
			loading={repoLoading}
			page={repoPage}
			pageSize={repoPageSize}
			onPageChange={handlePageChange}
			emptyMessage="No repositories yet"
		/>
	</div>
</div>
