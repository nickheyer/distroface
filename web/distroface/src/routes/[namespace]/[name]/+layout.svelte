<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { GitBranch, Loader2 } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import RepoHeader from '$lib/components/git/repo-header.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { authStore } from '$lib/stores/auth.svelte';

	let { children } = $props();

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');

	let redirecting = $state(false);
	let redirectChecked = '';

	$effect(() => {
		if (namespace && name) gitRepoStore.load(namespace, name);
	});

	// Old image and artifact bookmarks keep working
	async function tryRedirect(ns: string, n: string) {
		redirecting = true;
		try {
			await rpcClient.repository.getRepository({ namespace: ns, name: n }, silentCallOptions);
			await goto(resolve('/images/[namespace]/[...name]', { namespace: ns, name: n }), {
				replaceState: true
			});
			return;
		} catch {
			// Not an image repository
		}
		try {
			await rpcClient.artifact.getArtifactRepository({ namespace: ns, name: n }, silentCallOptions);
			await goto(resolve('/artifacts/[namespace]/[repo]', { namespace: ns, repo: n }), {
				replaceState: true
			});
			return;
		} catch {
			// Not an artifact repository either
		}
		redirecting = false;
	}

	$effect(() => {
		if (!gitRepoStore.notFound) return;
		const key = `${namespace}/${name}`;
		if (redirectChecked === key) return;
		redirectChecked = key;
		tryRedirect(namespace, name);
	});

	// A renamed or moved repository answers under its new address, follow it
	$effect(() => {
		const r = gitRepoStore.repo;
		if (!r || gitRepoStore.loading || gitRepoStore.loadedFor !== `${namespace}/${name}`) return;
		if (r.namespace === namespace && r.name === name) return;
		const prefix = `/${namespace}/${name}`;
		const rest = page.url.pathname.startsWith(prefix) ? page.url.pathname.slice(prefix.length) : '';
		goto(resolve(`/${r.namespace}/${r.name}${rest}${page.url.search}`), { replaceState: true });
	});

	const repo = $derived(gitRepoStore.repo);
	const current = $derived(!!repo && repo.namespace === namespace && repo.name === name);

	onMount(() => mirrorSyncStore.ensure());
</script>

<div class="space-y-6">
	{#if current && repo}
		<RepoHeader {repo} />
		<div class="page-enter">
			{@render children()}
		</div>
	{:else if gitRepoStore.loading || redirecting}
		<div class="space-y-4">
			<div class="flex flex-wrap items-start gap-4">
				<div class="min-w-48 flex-1 space-y-2">
					<Skeleton class="h-6 w-full max-w-72" />
					<Skeleton class="h-4 w-full max-w-96" />
				</div>
				<Skeleton class="h-8 w-24" />
				<Skeleton class="h-8 w-24" />
			</div>
			<div class="flex gap-4 border-b pb-2">
				{#each { length: 5 }, i (i)}
					<Skeleton class="h-5 w-20" />
				{/each}
			</div>
			<Skeleton class="h-64 w-full rounded-lg" />
			{#if redirecting}
				<p class="flex items-center gap-2 text-sm text-muted-foreground">
					<Loader2 class="size-3.5 animate-spin" />Looking up {namespace}/{name}
				</p>
			{/if}
		</div>
	{:else}
		<EmptyState
			icon={GitBranch}
			message="Repository not found"
			description="{namespace}/{name} does not exist or you don't have access."
		>
			{#snippet actions()}
				<Button variant="outline" href={resolve('/git')}>Back to Git</Button>
				{#if !authStore.isAuthenticated}
					<Button href={resolve('/login')}>Sign in</Button>
				{/if}
			{/snippet}
		</EmptyState>
	{/if}
</div>
