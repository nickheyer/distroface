<script lang="ts">
	import { resolve } from '$app/paths';
	import { GitPullRequest, GitCompare, Loader } from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import type { GitRepository, CompareRefsResponse } from '$lib/proto/distroface/v1/git_pb';

	// Distance of a fork branch from its upstream with a shortcut to open a pull request there
	let {
		repo,
		branch
	}: {
		repo: GitRepository;
		branch: string;
	} = $props();

	const upstream = $derived(repo.forkOf);
	const upstreamNs = $derived(upstream.split('/')[0] ?? '');
	const upstreamName = $derived(upstream.split('/').slice(1).join('/'));
	const head = $derived(`${repo.namespace}/${repo.name}:${branch}`);

	let result = $state<CompareRefsResponse | undefined>();
	let loading = $state(true);
	let failed = $state(false);
	let seq = 0;

	async function load() {
		const mine = ++seq;
		loading = true;
		failed = false;
		try {
			const resp = await rpcClient.gitFiles.compareRefs(
				{ namespace: upstreamNs, name: upstreamName, base: '', head },
				silentCallOptions
			);
			if (mine !== seq) return;
			result = resp;
		} catch {
			if (mine !== seq) return;
			result = undefined;
			failed = true;
		} finally {
			if (mine === seq) loading = false;
		}
	}

	$effect(() => {
		void head;
		void upstream;
		load();
	});

	const pullHref = $derived(
		resolve(
			`${resolve('/[namespace]/[name]/pulls/new', { namespace: upstreamNs, name: upstreamName })}?head=${encodeURIComponent(head)}` as `/${string}`
		)
	);
	const compareHref = $derived(
		resolve('/[namespace]/[name]/compare/[range]', {
			namespace: upstreamNs,
			name: upstreamName,
			range: `...${encodeURIComponent(head)}`
		})
	);
</script>

{#if upstream && branch}
	<div
		class="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border bg-card px-4 py-2.5 text-sm"
	>
		<GitCompare class="size-4 shrink-0 text-muted-foreground" />
		<span class="min-w-0 flex-1">
			{#if loading}
				<span class="inline-flex items-center gap-1.5 text-muted-foreground"
					><Loader class="size-3.5 animate-spin" />Comparing with upstream</span
				>
			{:else if failed || !result}
				<span class="text-muted-foreground">
					This branch cannot be compared with <a
						href={resolve('/[namespace]/[name]', { namespace: upstreamNs, name: upstreamName })}
						class="font-medium text-foreground hover:text-primary">{upstream}</a
					>.
				</span>
			{:else if result.ahead === 0 && result.behind === 0}
				This branch is up to date with <a
					href={resolve('/[namespace]/[name]', { namespace: upstreamNs, name: upstreamName })}
					class="font-medium text-foreground hover:text-primary">{upstream}</a
				>.
			{:else}
				This branch is
				{#if result.ahead > 0}
					<span class="font-medium tabular-nums"
						>{result.ahead} commit{result.ahead !== 1 ? 's' : ''} ahead of</span
					>{#if result.behind > 0},{/if}
				{/if}
				{#if result.behind > 0}
					<span class="font-medium tabular-nums"
						>{result.behind} commit{result.behind !== 1 ? 's' : ''} behind</span
					>
				{/if}
				<a
					href={resolve('/[namespace]/[name]', { namespace: upstreamNs, name: upstreamName })}
					class="font-medium text-foreground hover:text-primary">{upstream}</a
				>.
			{/if}
		</span>
		{#if result && result.ahead > 0}
			<Button variant="outline" href={compareHref}><GitCompare />Compare</Button>
			{#if authStore.isAuthenticated}
				<Button href={pullHref}><GitPullRequest />Open pull request</Button>
			{/if}
		{/if}
	</div>
{/if}
