<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import {
		GitCompare,
		GitPullRequest,
		ArrowLeftRight,
		GitCommit as GitCommitIcon,
		FileDiff
	} from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import RefPicker from '$lib/components/git/ref-picker.svelte';
	import BaseRepoPicker from '$lib/components/git/base-repo-picker.svelte';
	import PagedCommitList from '$lib/components/git/paged-commit-list.svelte';
	import DiffViewer from '$lib/components/git/diff-viewer.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import type { CompareRefsResponse } from '$lib/proto/distroface/v1/git_pb';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const repoFullName = $derived(`${namespace}/${name}`);

	// Accepts base...head, base..head, or a lone head
	const parsed = $derived.by(() => {
		const raw = page.params.range ?? '';
		const sep = raw.includes('...') ? '...' : raw.includes('..') ? '..' : '';
		if (!sep) return { base: repo.defaultBranch, head: raw };
		const idx = raw.indexOf(sep);
		return { base: raw.slice(0, idx) || repo.defaultBranch, head: raw.slice(idx + sep.length) };
	});
	const base = $derived(parsed.base);
	const head = $derived(parsed.head);
	const headIsCrossRepo = $derived(head.includes(':'));

	let result = $state<CompareRefsResponse | undefined>();
	let loading = $state(true);
	let failed = $state(false);
	let seq = 0;

	async function load() {
		const mine = ++seq;
		loading = true;
		failed = false;
		if (!head) {
			loading = false;
			return;
		}
		try {
			const resp = await rpcClient.gitFiles.compareRefs(
				{ namespace, name, base, head },
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
		void base;
		void head;
		void repo.id;
		load();
	});

	function navigate(b: string, h: string) {
		goto(
			resolve('/[namespace]/[name]/compare/[range]', {
				namespace,
				name,
				range: `${encodeURIComponent(b)}...${encodeURIComponent(h)}`
			})
		);
	}

	// Picking the upstream as base moves the comparison there with this repository as the head
	function pickBaseRepo(full: string) {
		const [ns, ...rest] = full.split('/');
		const crossHead = headIsCrossRepo ? head : `${repoFullName}:${head}`;
		goto(
			resolve('/[namespace]/[name]/compare/[range]', {
				namespace: ns,
				name: rest.join('/'),
				range: `...${encodeURIComponent(crossHead)}`
			})
		);
	}

	const canOpenPull = $derived(
		authStore.isAuthenticated && gitRepoStore.unitEnabled(GitUnit.PULLS) && !repo.isArchived
	);
	const pullHref = $derived(
		resolve(
			`${resolve('/[namespace]/[name]/pulls/new', { namespace, name })}?base=${encodeURIComponent(base)}&head=${encodeURIComponent(head)}` as `/${string}`
		)
	);
	const identical = $derived(!!result && result.ahead === 0 && result.changedFiles === 0);
	const diffBase = $derived(result ? result.mergeBaseSha || result.baseSha : '');
	const resetKey = $derived(result ? `${diffBase}:${result.headSha}` : '');
</script>

<svelte:head>
	<title>Comparing {base}...{head} - {repoFullName}</title>
</svelte:head>

<div class="space-y-5">
	<div class="flex flex-wrap items-center gap-2 rounded-lg border bg-card px-4 py-3">
		<GitCompare class="size-4 text-muted-foreground" />
		<BaseRepoPicker {repo} onSelect={pickBaseRepo} />
		<RefPicker
			{namespace}
			{name}
			current={base}
			defaultBranch={repo.defaultBranch}
			label="base"
			onSelect={(r) => navigate(r, head)}
		/>
		<Button
			variant="ghost-muted"
			size="icon"
			onclick={() => navigate(head, base)}
			title="Swap"
			aria-label="Swap"
			disabled={headIsCrossRepo}
		>
			<ArrowLeftRight />
		</Button>
		{#if headIsCrossRepo}
			<span
				class="inline-flex h-8 max-w-full min-w-0 items-center truncate rounded-md border px-2 font-mono text-xs"
				>{head}</span
			>
		{:else}
			<RefPicker
				{namespace}
				{name}
				current={head}
				defaultBranch={repo.defaultBranch}
				label="compare"
				onSelect={(r) => navigate(base, r)}
			/>
		{/if}
		{#if result}
			<span class="ml-1 text-xs text-muted-foreground tabular-nums">
				<span class="font-medium text-foreground">{result.ahead}</span> ahead,
				<span class="font-medium text-foreground">{result.behind}</span> behind
			</span>
		{/if}
		<div class="ml-auto">
			{#if canOpenPull && result && !identical}
				<Button href={pullHref}>
					<GitPullRequest />Create pull request
				</Button>
			{/if}
		</div>
	</div>

	{#if loading}
		<Skeleton class="h-40 w-full rounded-lg" />
		<Skeleton class="h-64 w-full rounded-lg" />
	{:else if !head}
		<EmptyState
			icon={GitCompare}
			message="Pick a branch to compare"
			description="Choose a compare ref to see commits and changes against {base}."
		/>
	{:else if failed || !result}
		<EmptyState
			icon={GitCompare}
			message="Nothing to compare"
			description="{base} and {head} could not be compared. Check that both refs exist."
		/>
	{:else if identical}
		<EmptyState
			icon={GitCompare}
			message="These refs are identical"
			description="There is nothing to compare between {base} and {head}."
		/>
	{:else}
		<section class="space-y-3">
			<h2 class="inline-flex items-center gap-1.5 text-sm font-semibold">
				<GitCommitIcon class="size-4 text-muted-foreground" />
				{result.ahead} commit{result.ahead !== 1 ? 's' : ''}
			</h2>
			<PagedCommitList
				{namespace}
				{name}
				{resetKey}
				fetch={(p) =>
					rpcClient.gitFiles.listCompareCommits(
						{ namespace, name, base, head, page: p },
						silentCallOptions
					)}
				emptyMessage="No commits between these refs"
			/>
		</section>
		<section class="space-y-3">
			<h2 class="inline-flex items-center gap-1.5 text-sm font-semibold">
				<FileDiff class="size-4 text-muted-foreground" />
				{result.changedFiles} file{result.changedFiles !== 1 ? 's' : ''} changed
			</h2>
			<DiffViewer
				{namespace}
				{name}
				head={result.headSha}
				base={diffBase}
				{resetKey}
				listFiles={async (p) => {
					const resp = await rpcClient.gitFiles.getCompareDiff(
						{ namespace, name, base, head, page: p },
						silentCallOptions
					);
					return {
						files: resp.diff?.files ?? [],
						page: resp.page,
						totalFiles: resp.diff?.totalFiles ?? 0,
						additions: resp.diff?.additions ?? 0,
						deletions: resp.diff?.deletions ?? 0,
						truncated: resp.diff?.truncated ?? false
					};
				}}
				loadFile={async (path) =>
					(
						await rpcClient.gitFiles.getDiffFile(
							{ namespace, name, base: diffBase, head: result?.headSha ?? '', path },
							silentCallOptions
						)
					).file}
			/>
		</section>
	{/if}
</div>
