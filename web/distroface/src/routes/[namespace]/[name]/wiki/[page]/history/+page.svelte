<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import {
		History,
		ChevronLeft,
		ChevronRight,
		Eye,
		X,
		GitCommit as GitCommitIcon,
		Loader2
	} from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import Markdown from '$lib/components/git/markdown.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import type { GitCommit } from '$lib/proto/distroface/v1/git_pb';
	import type { WikiPage } from '$lib/proto/distroface/v1/wiki_pb';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const title = $derived(decodeURIComponent(page.params.page ?? ''));
	const base = $derived(resolve('/[namespace]/[name]/wiki', { namespace, name }));

	let commits = $state<GitCommit[]>([]);
	let loading = $state(true);
	let loadedKey = $state('');
	// Revisions walk a commit cursor, so pages stack instead of counting
	const PAGE_SIZE = 25;
	let tokens = $state<string[]>([]);
	let nextToken = $state('');
	const subtitle = $derived(
		tokens.length === 0 && !nextToken
			? `${commits.length} revision${commits.length === 1 ? '' : 's'}`
			: `Page ${tokens.length + 1}`
	);

	let viewing = $state<WikiPage | undefined>();
	let viewingSha = $state('');
	let viewLoading = $state('');

	$effect(() => {
		void gitRepoStore.load(namespace, name);
	});

	$effect(() => {
		const key = `${namespace}/${name}:${title}`;
		if (key !== loadedKey) {
			loadedKey = key;
			tokens = [];
			nextToken = '';
			viewing = undefined;
			viewingSha = '';
			void load();
		}
	});

	async function load(token = '') {
		loading = true;
		try {
			const resp = await rpcClient.wiki.listWikiRevisions(
				{ namespace, name, title, page: { pageSize: PAGE_SIZE, pageToken: token, orderBy: '' } },
				silentCallOptions
			);
			commits = resp.commits;
			nextToken = resp.page?.nextPageToken ?? '';
		} catch {
			commits = [];
			nextToken = '';
		} finally {
			loading = false;
		}
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

	// Page content as it stood at one commit
	async function view(c: GitCommit) {
		if (viewingSha === c.sha) {
			viewing = undefined;
			viewingSha = '';
			return;
		}
		viewLoading = c.sha;
		try {
			const resp = await rpcClient.wiki.getWikiPage(
				{ namespace, name, title, sha: c.sha },
				silentCallOptions
			);
			viewing = resp.page;
			viewingSha = c.sha;
		} catch {
			viewing = undefined;
			viewingSha = '';
		} finally {
			viewLoading = '';
		}
	}

	function when(c: GitCommit): Date | null {
		const ts = c.committer?.at ?? c.author?.at;
		return ts ? timestampDate(ts) : null;
	}

	function who(c: GitCommit): string {
		return c.author?.username || c.author?.name || '';
	}

	function subject(c: GitCommit): string {
		return c.message.split('\n')[0] ?? '';
	}
</script>

<svelte:head>
	<title>History of {title} - Wiki - {namespace}/{name}</title>
</svelte:head>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title truncate">History of {title}</h2>
			<p class="section-subtitle">{subtitle}</p>
		</div>
		<Button
			variant="outline"
			class="shrink-0"
			href={resolve('/[namespace]/[name]/wiki/[page]', {
				namespace,
				name,
				page: encodeURIComponent(title)
			})}
		>
			<ChevronLeft />Page
		</Button>
	</div>

	{#if viewing}
		<article class="box">
			<header class="flex flex-wrap items-center gap-3 border-b px-5 py-3">
				<Eye class="size-4 shrink-0 text-muted-foreground" />
				<div class="min-w-0 flex-1 text-sm">
					Viewing <strong>{viewing.title}</strong> at
					<code class="font-mono text-xs">{viewingSha.slice(0, 7)}</code>
				</div>
				<Button
					variant="ghost-muted"
					size="sm"
					onclick={() => {
						viewing = undefined;
						viewingSha = '';
					}}
				>
					<X />Close
				</Button>
			</header>
			<div class="min-w-0 overflow-x-auto px-5 py-4">
				{#if viewing.content.trim()}
					<Markdown source={viewing.content} baseHref="{base}/" />
				{:else}
					<p class="text-sm text-muted-foreground">This revision is empty</p>
				{/if}
			</div>
		</article>
	{/if}

	{#if loading && commits.length === 0}
		<Skeleton class="h-64 w-full rounded-lg" />
	{:else if commits.length === 0}
		<EmptyState icon={History} message="No revisions" description="This page has no history yet." />
	{:else}
		<div class="box transition-opacity {loading ? 'opacity-60' : ''}">
			<ul class="divide-y">
				{#each commits as c, i (c.sha)}
					{@const date = when(c)}
					<li
						class="flex min-w-0 flex-wrap items-center gap-3 px-4 py-2.5 sm:flex-nowrap {viewingSha ===
						c.sha
							? 'bg-muted/30'
							: ''}"
					>
						<GitCommitIcon class="size-3.5 shrink-0 text-muted-foreground" />
						<div class="min-w-0 flex-1">
							<div class="flex min-w-0 items-center gap-2">
								<span class="truncate text-sm font-medium" title={c.message}
									>{subject(c) || 'No message'}</span
								>
								{#if i === 0 && tokens.length === 0}
									<Badge variant="outline">Latest</Badge>
								{/if}
							</div>
							<p class="truncate text-xs text-muted-foreground">
								{#if who(c)}
									<a
										href={resolve('/[namespace]', { namespace: who(c) })}
										class="font-medium text-foreground hover:text-primary">{who(c)}</a
									>
								{/if}
								{#if date}
									<span title={date.toLocaleString()}>committed {relativeTime(date)}</span>
								{/if}
							</p>
						</div>
						<code
							class="hidden shrink-0 font-mono text-2xs text-muted-foreground sm:inline"
							title={c.sha}>{c.sha.slice(0, 7)}</code
						>
						<Button
							variant={viewingSha === c.sha ? 'default' : 'outline'}
							size="sm"
							class="shrink-0"
							onclick={() => view(c)}
							disabled={viewLoading === c.sha}
						>
							{#if viewLoading === c.sha}<Loader2 class="animate-spin" />{:else}<Eye />{/if}
							{viewLoading === c.sha ? 'Loading' : viewingSha === c.sha ? 'Viewing' : 'View'}
						</Button>
					</li>
				{/each}
			</ul>
			{#if tokens.length > 0 || nextToken}
				<div class="flex items-center justify-end gap-1 border-t px-4 py-2">
					<Button
						variant="outline"
						size="sm"
						disabled={tokens.length === 0 || loading}
						onclick={newer}
					>
						<ChevronLeft />Newer
					</Button>
					<Button variant="outline" size="sm" disabled={!nextToken || loading} onclick={older}>
						Older<ChevronRight />
					</Button>
				</div>
			{/if}
		</div>
	{/if}
</div>
