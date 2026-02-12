<script lang="ts">
	import { Card, CardContent, CardHeader, CardTitle } from '$lib/components/ui/card';
	import { Badge } from '$lib/components/ui/badge';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { onMount } from 'svelte';
	import { CheckCircle, XCircle, Home, Package, Search } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { pageToToken } from '$lib/utils';
	import type { Repository } from '$lib/proto/distroface/v1/types_pb';

	let healthStatus = $state<{ status: string; version: string } | null>(null);
	let healthError = $state<string | null>(null);
	let isLoading = $state(true);
	let repos = $state<Repository[]>([]);
	let repoLoading = $state(false);
	let repoTotalCount = $state(0);
	let repoPage = $state(1);
	const repoPageSize = 20;

	let searchQuery = $state('');
	let searchTimeout: ReturnType<typeof setTimeout> | undefined;

	async function checkHealth() {
		try {
			const response = await rpcClient.health.healthCheck({});
			healthStatus = { status: response.status, version: response.version };
			healthError = null;
		} catch (error: any) {
			healthError = error.message || 'Failed to connect';
			healthStatus = null;
		} finally {
			isLoading = false;
		}
	}

	async function loadRepos() {
		repoLoading = true;
		try {
			const response = await rpcClient.repository.listRepositories({
				pageSize: repoPageSize,
				pageToken: pageToToken(repoPage, repoPageSize),
				query: searchQuery
			});
			repos = response.repositories;
			repoTotalCount = response.totalCount;
		} catch {
			repos = [];
			repoTotalCount = 0;
		} finally {
			repoLoading = false;
		}
	}

	function handleSearchInput() {
		clearTimeout(searchTimeout);
		searchTimeout = setTimeout(() => {
			repoPage = 1;
			loadRepos();
		}, 300);
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
		checkHealth();
		loadRepos();
	});
</script>

<div class="flex-1 space-y-6 h-full p-6">
	<div class="flex items-center gap-4 pb-4 border-b border-border/40">
		<div class="h-14 w-14 rounded-2xl bg-linear-to-br from-primary/20 to-primary/10 flex items-center justify-center shadow-lg">
			<Home class="h-7 w-7 text-primary" />
		</div>
		<div class="space-y-1">
			<h2 class="text-3xl font-bold tracking-tight">Distroface</h2>
			<p class="text-sm text-muted-foreground">
				{#if authStore.isAuthenticated}
					Welcome back, {authStore.user?.displayName || authStore.user?.username}
				{:else}
					Your container registry
				{/if}
			</p>
		</div>
	</div>

	<div class="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
		<Card class="border-border/50 hover:border-primary/30 transition-all hover:shadow-lg">
			<CardHeader class="flex flex-row items-center justify-between space-y-0 pb-2">
				<CardTitle class="text-sm font-medium">API Health</CardTitle>
				{#if isLoading}
					<div class="h-5 w-5 border-2 border-primary/30 border-t-primary rounded-full animate-spin"></div>
				{:else if healthStatus}
					<CheckCircle class="h-5 w-5 text-green-500" />
				{:else}
					<XCircle class="h-5 w-5 text-red-500" />
				{/if}
			</CardHeader>
			<CardContent>
				{#if isLoading}
					<p class="text-sm text-muted-foreground">Checking...</p>
				{:else if healthStatus}
					<div class="flex items-center gap-2">
						<Badge variant="outline" class="bg-green-500/10 text-green-500 border-green-500/20">
							{healthStatus.status}
						</Badge>
						<span class="text-xs text-muted-foreground">{healthStatus.version}</span>
					</div>
				{:else}
					<p class="text-sm text-red-500">{healthError}</p>
				{/if}
			</CardContent>
		</Card>
	</div>

	<div class="space-y-4">
		<div class="flex items-center justify-between">
			<h3 class="text-lg font-semibold">Repositories</h3>
			{#if repoTotalCount > 0}
				<span class="text-sm text-muted-foreground">{repoTotalCount} total</span>
			{/if}
		</div>

		<div class="relative">
			<Search class="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
			<Input
				placeholder="Search repositories..."
				class="pl-9"
				bind:value={searchQuery}
				oninput={handleSearchInput}
			/>
		</div>

		{#if repoLoading}
			<div class="flex items-center justify-center py-12">
				<div class="h-6 w-6 border-2 border-primary/30 border-t-primary rounded-full animate-spin"></div>
			</div>
		{:else if repos.length === 0}
			<Card class="border-dashed">
				<CardContent class="flex flex-col items-center justify-center py-12 text-center">
					<Package class="h-12 w-12 text-muted-foreground/50 mb-4" />
					<p class="text-muted-foreground">{searchQuery ? 'No repositories found' : 'No repositories yet'}</p>
					{#if !searchQuery}
						{#if authStore.isAuthenticated}
							<p class="text-sm text-muted-foreground mt-1">
								Push your first image: 
								<code class="bg-muted px-1.5 py-0.5 rounded text-xs">
									docker push {configStore.get('registryHost', 'localhost:8080')}/{authStore.user?.username}/myimage:latest
								</code>
							</p>
						{:else}
							<p class="text-sm text-muted-foreground mt-1">
								<a href="/login" class="text-primary underline-offset-4 hover:underline">Sign in</a> to push images
							</p>
						{/if}
					{/if}
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
