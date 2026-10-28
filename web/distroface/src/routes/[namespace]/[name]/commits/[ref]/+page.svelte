<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { ChevronLeft, ChevronRight, X, Search } from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import RefPicker from '$lib/components/git/ref-picker.svelte';
	import CommitList from '$lib/components/git/commit-list.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import type { GitCommit } from '$lib/proto/distroface/v1/git_pb';

	const PAGE_SIZE = 30;

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const ref = $derived(page.params.ref ?? '');
	const path = $derived(page.url.searchParams.get('path') ?? '');
	const authorParam = $derived(page.url.searchParams.get('author') ?? '');

	let commits = $state<GitCommit[]>([]);
	let loading = $state(true);
	let author = $state('');
	let tokens = $state<string[]>([]);
	let nextToken = $state('');
	let debounce: ReturnType<typeof setTimeout> | undefined;
	let seq = 0;

	async function load(token = '') {
		const mine = ++seq;
		loading = true;
		try {
			const resp = await rpcClient.gitFiles.listCommits(
				{
					namespace,
					name,
					ref,
					path,
					author: author.trim(),
					page: { pageSize: PAGE_SIZE, pageToken: token, orderBy: '' }
				},
				silentCallOptions
			);
			if (mine !== seq) return;
			commits = resp.commits;
			nextToken = resp.page?.nextPageToken ?? '';
		} catch {
			if (mine !== seq) return;
			commits = [];
			nextToken = '';
		} finally {
			if (mine === seq) loading = false;
		}
	}

	function restart() {
		tokens = [];
		load();
	}

	function older() {
		if (!nextToken) return;
		tokens = [...tokens, nextToken];
		load(nextToken);
	}

	function newer() {
		if (tokens.length === 0) return;
		tokens = tokens.slice(0, -1);
		load(tokens[tokens.length - 1] ?? '');
	}

	$effect(() => {
		void ref;
		void path;
		void repo.id;
		author = authorParam;
		restart();
	});

	function onAuthorInput() {
		clearTimeout(debounce);
		debounce = setTimeout(restart, 300);
	}

	function onRef(next: string) {
		const href = resolve('/[namespace]/[name]/commits/[ref]', {
			namespace,
			name,
			ref: encodeURIComponent(next)
		});
		goto(resolve(`${href}${path ? `?path=${encodeURIComponent(path)}` : ''}` as `/${string}`));
	}

	function clearPath() {
		goto(
			resolve('/[namespace]/[name]/commits/[ref]', {
				namespace,
				name,
				ref: encodeURIComponent(ref)
			})
		);
	}

	const pageNumber = $derived(tokens.length + 1);
</script>

<div class="space-y-4">
	<div class="flex flex-wrap items-center gap-2">
		<RefPicker
			{namespace}
			{name}
			current={ref}
			defaultBranch={repo.defaultBranch}
			onSelect={onRef}
		/>
		{#if path}
			<span
				class="inline-flex h-8 max-w-full min-w-0 items-center gap-1 rounded-md border bg-muted px-2 font-mono text-xs"
			>
				<span class="truncate">{path}</span>
				<button
					type="button"
					class="ml-1 text-muted-foreground hover:text-foreground"
					onclick={clearPath}
					aria-label="Clear path filter"
				>
					<X class="size-3" />
				</button>
			</span>
		{/if}
		<div class="relative w-full sm:ml-auto sm:w-56">
			<Search class="absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
			<Input
				bind:value={author}
				oninput={onAuthorInput}
				placeholder="Filter by author"
				class="pl-8"
			/>
		</div>
	</div>

	<CommitList
		{commits}
		{namespace}
		{name}
		{loading}
		emptyMessage={author || path ? 'No matching commits' : 'No commits yet'}
	/>

	{#if tokens.length > 0 || nextToken}
		<div class="flex items-center justify-between gap-3 pt-1">
			<span class="text-xs text-muted-foreground tabular-nums">Page {pageNumber}</span>
			<div class="flex items-center gap-1">
				<Button variant="outline" disabled={tokens.length === 0 || loading} onclick={newer}>
					<ChevronLeft />Newer
				</Button>
				<Button variant="outline" disabled={!nextToken || loading} onclick={older}>
					Older<ChevronRight />
				</Button>
			</div>
		</div>
	{/if}
</div>
