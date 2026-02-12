<script lang="ts">
	import { page } from '$app/state';
	import { onMount } from 'svelte';
	import { UserRound, Package, Calendar } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { pageToToken } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { Card, CardContent } from '$lib/components/ui/card';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import type { User, Repository } from '$lib/proto/distroface/v1/types_pb';

	const username = $derived(page.params.username);

	let user = $state<User | undefined>(undefined);
	let loading = $state(true);
	let repos = $state<Repository[]>([]);
	let repoLoading = $state(true);
	let repoTotalCount = $state(0);
	let repoPage = $state(1);
	const repoPageSize = 20;

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

	function prevPage() {
		if (repoPage > 1) {
			repoPage--;
			loadRepos();
		}
	}

	function nextPage() {
		if (repoPage * repoPageSize < repoTotalCount) {
			repoPage++;
			loadRepos();
		}
	}

	onMount(() => {
		loadUser();
		loadRepos();
	});
</script>

<div class="flex-1 space-y-6 h-full p-6">
	<!-- User Header -->
	<div class="flex items-center gap-4 pb-4 border-b border-border/40">
		<div class="h-14 w-14 rounded-full bg-linear-to-br from-primary/20 to-primary/10 flex items-center justify-center shadow-lg">
			<UserRound class="h-7 w-7 text-primary" />
		</div>
		<div class="space-y-1">
			{#if loading}
				<Skeleton class="h-8 w-48" />
				<Skeleton class="h-4 w-32" />
			{:else if user}
				<h2 class="text-3xl font-bold tracking-tight">{user.username}</h2>
				<div class="flex items-center gap-3 text-sm text-muted-foreground">
					{#if user.displayName}
						<span>{user.displayName}</span>
					{/if}
					{#if user.createdAt}
						<span class="flex items-center gap-1">
							<Calendar class="h-3.5 w-3.5" />
							Joined {timestampDate(user.createdAt).toLocaleDateString()}
						</span>
					{/if}
				</div>
			{:else}
				<h2 class="text-3xl font-bold tracking-tight">{username}</h2>
				<p class="text-sm text-muted-foreground">User not found</p>
			{/if}
		</div>
	</div>

	<!-- Repositories -->
	<div class="space-y-4">
		<div class="flex items-center justify-between">
			<h3 class="text-lg font-semibold">Repositories</h3>
			{#if repoTotalCount > 0}
				<span class="text-sm text-muted-foreground">{repoTotalCount} total</span>
			{/if}
		</div>

		{#if repoLoading}
			<div class="space-y-3">
				{#each Array(3) as _}
					<Skeleton class="h-16 w-full" />
				{/each}
			</div>
		{:else if repos.length === 0}
			<Card class="border-dashed">
				<CardContent class="flex flex-col items-center justify-center py-12 text-center">
					<Package class="h-12 w-12 text-muted-foreground/50 mb-4" />
					<p class="text-muted-foreground">No repositories yet</p>
				</CardContent>
			</Card>
		{:else}
			<div class="grid gap-3">
				{#each repos as repo}
					<a href="/{repo.namespace}/{repo.name}" class="block">
						<Card class="border-border/50 hover:border-primary/30 transition-all hover:shadow-md">
							<CardContent class="flex items-center justify-between py-4">
								<div class="flex items-center gap-3">
									<Package class="h-5 w-5 text-muted-foreground" />
									<div>
										<p class="font-medium">{repo.fullName}</p>
										<div class="flex items-center gap-2 mt-1">
											<Badge variant="outline" class="text-xs">
												{repo.visibility === 2 ? 'private' : 'public'}
											</Badge>
											{#if repo.description}
												<span class="text-xs text-muted-foreground truncate max-w-xs">{repo.description}</span>
											{/if}
											{#if repo.pushCount > 0}
												<span class="text-xs text-muted-foreground">{repo.pushCount} pushes</span>
											{/if}
											{#if repo.pullCount > 0}
												<span class="text-xs text-muted-foreground">{repo.pullCount} pulls</span>
											{/if}
										</div>
									</div>
								</div>
							</CardContent>
						</Card>
					</a>
				{/each}
			</div>

			<!-- Pagination -->
			{#if repoTotalCount > repoPageSize}
				<div class="flex items-center justify-between">
					<span class="text-sm text-muted-foreground">
						Page {repoPage} of {Math.ceil(repoTotalCount / repoPageSize)}
					</span>
					<div class="flex gap-2">
						<Button variant="outline" size="sm" disabled={repoPage <= 1} onclick={prevPage}>
							Previous
						</Button>
						<Button
							variant="outline"
							size="sm"
							disabled={repoPage * repoPageSize >= repoTotalCount}
							onclick={nextPage}
						>
							Next
						</Button>
					</div>
				</div>
			{/if}
		{/if}
	</div>
</div>
