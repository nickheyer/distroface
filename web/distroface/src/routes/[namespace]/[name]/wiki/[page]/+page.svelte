<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { BookOpen, Pencil, History, Trash2, ChevronLeft } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import Markdown from '$lib/components/git/markdown.svelte';
	import WikiSidebar from '$lib/components/git/wiki-sidebar.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import type { WikiPage } from '$lib/proto/distroface/v1/wiki_pb';
	import { ConnectError } from '@connectrpc/connect';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const title = $derived(decodeURIComponent(page.params.page ?? ''));
	const repo = $derived(gitRepoStore.repo);
	const base = $derived(resolve('/[namespace]/[name]/wiki', { namespace, name }));
	const rawBase = $derived(`/${namespace}/${name}.wiki/-/raw/HEAD/`);
	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.WIKI) && !repo?.isArchived);

	let current = $state<WikiPage | undefined>();
	let pages = $state<WikiPage[]>([]);
	let loading = $state(true);
	let error = $state('');
	let loadedKey = $state('');
	let deleteOpen = $state(false);
	let deleting = $state(false);

	const date = $derived.by(() => {
		const ts = current?.lastCommit?.committer?.at ?? current?.lastCommit?.author?.at;
		return ts ? timestampDate(ts) : null;
	});
	const author = $derived(
		current?.lastCommit?.author?.username || current?.lastCommit?.author?.name || ''
	);

	$effect(() => {
		void gitRepoStore.load(namespace, name);
	});

	$effect(() => {
		const key = `${namespace}/${name}:${title}`;
		if (key !== loadedKey) {
			loadedKey = key;
			void load();
		}
	});

	async function load() {
		loading = true;
		error = '';
		try {
			const [pg, list] = await Promise.all([
				rpcClient.wiki.getWikiPage({ namespace, name, title, sha: '' }, silentCallOptions),
				rpcClient.wiki
					.listWikiPages(
						{
							namespace,
							name,
							page: { pageSize: 500, pageToken: '', query: { text: '', filters: [] }, orderBy: '' }
						},
						silentCallOptions
					)
					.catch(() => undefined)
			]);
			current = pg.page;
			pages = list?.pages ?? [];
		} catch (err) {
			current = undefined;
			error = err instanceof ConnectError ? err.rawMessage || err.message : 'Failed to load page';
		} finally {
			loading = false;
		}
	}

	async function remove() {
		if (!current) return;
		deleting = true;
		try {
			await rpcClient.wiki.deleteWikiPage({ namespace, name, title: current.title });
			toast.success('Page deleted');
			deleteOpen = false;
			await goto(resolve('/[namespace]/[name]/wiki', { namespace, name }));
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}
</script>

<svelte:head>
	<title>{title} - Wiki - {namespace}/{name}</title>
</svelte:head>

{#if loading && !current}
	<div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_18rem]">
		<Skeleton class="h-96 w-full rounded-lg" />
		<Skeleton class="h-64 w-full rounded-lg" />
	</div>
{:else if !current}
	<EmptyState
		icon={BookOpen}
		message="Page not found"
		description={error || `There is no wiki page titled ${title}.`}
	>
		{#snippet actions()}
			<Button variant="outline" href={base}>Back to wiki</Button>
			<PermissionGate allowed={canWrite}>
				<Button
					href={resolve(
						`${resolve('/[namespace]/[name]/wiki/new', { namespace, name })}?title=${encodeURIComponent(title)}` as `/${string}`
					)}>Create page</Button
				>
			</PermissionGate>
		{/snippet}
	</EmptyState>
{:else}
	<div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_18rem]">
		<div class="min-w-0">
			<article class="box">
				<header class="flex flex-wrap items-center gap-3 border-b px-5 py-3.5">
					<BookOpen class="size-4 shrink-0 text-muted-foreground" />
					<div class="min-w-0 flex-1">
						<h1 class="truncate text-base font-semibold">{current.title}</h1>
						{#if current.lastCommit}
							<p class="truncate text-xs text-muted-foreground">
								Last edited
								{#if author}
									by <a
										href={resolve('/[namespace]', { namespace: author })}
										class="font-medium text-foreground hover:text-primary">{author}</a
									>
								{/if}
								{#if date}
									<span title={date.toLocaleString()}>{relativeTime(date)}</span>
								{/if}
							</p>
						{/if}
					</div>
					<div class="flex flex-wrap items-center gap-2">
						<Button
							variant="ghost-muted"
							size="icon"
							href={base}
							title="Wiki home"
							aria-label="Wiki home"
						>
							<ChevronLeft />
						</Button>
						<Button
							variant="outline"
							href={resolve('/[namespace]/[name]/wiki/[page]/history', {
								namespace,
								name,
								page: encodeURIComponent(current.title)
							})}
						>
							<History />History
						</Button>
						<PermissionGate allowed={canWrite}>
							<Button
								href={resolve('/[namespace]/[name]/wiki/[page]/edit', {
									namespace,
									name,
									page: encodeURIComponent(current.title)
								})}
							>
								<Pencil />Edit
							</Button>
							<Button
								variant="outline-destructive"
								size="icon"
								onclick={() => (deleteOpen = true)}
								title="Delete page"
								aria-label="Delete page"
							>
								<Trash2 />
							</Button>
						</PermissionGate>
					</div>
				</header>
				<div
					class="min-w-0 overflow-x-auto px-5 py-4 transition-opacity {loading ? 'opacity-60' : ''}"
				>
					{#if current.content.trim()}
						<Markdown source={current.content} baseHref="{base}/" wikiBase="{base}/" {rawBase} />
					{:else}
						<p class="text-sm text-muted-foreground">This page is empty</p>
					{/if}
				</div>
			</article>
		</div>
		<WikiSidebar
			{namespace}
			{name}
			{pages}
			current={current.title}
			sidebar={current.sidebar}
			footer={current.footer}
			{canWrite}
			{rawBase}
			class="order-first lg:order-none"
		/>
	</div>
{/if}

<ConfirmDialog
	bind:open={deleteOpen}
	title="Delete page"
	confirmLabel="Delete"
	onConfirm={remove}
	loading={deleting}
	icon={Trash2}
>
	{#snippet description()}
		This removes <strong>{current?.title}</strong> from the wiki in a new commit. Earlier revisions stay
		in the wiki history.
	{/snippet}
</ConfirmDialog>
