<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { BookOpen, Pencil, History, Plus } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import Markdown from '$lib/components/git/markdown.svelte';
	import WikiSidebar from '$lib/components/git/wiki-sidebar.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import type { WikiPage } from '$lib/proto/distroface/v1/wiki_pb';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);
	const base = $derived(resolve('/[namespace]/[name]/wiki', { namespace, name }));
	const newHref = $derived(resolve('/[namespace]/[name]/wiki/new', { namespace, name }));
	const rawBase = $derived(`/${namespace}/${name}.wiki/-/raw/HEAD/`);
	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.WIKI) && !repo?.isArchived);

	let pages = $state<WikiPage[]>([]);
	let exists = $state(false);
	let home = $state<WikiPage | undefined>();
	let loading = $state(true);
	let loadedFor = $state('');
	let httpUrl = $state('');
	let sshUrl = $state('');
	let cloneTab = $state<'http' | 'ssh'>('http');

	const cloneUrl = $derived(cloneTab === 'ssh' && sshUrl ? sshUrl : httpUrl);
	const homeDate = $derived.by(() => {
		const ts = home?.lastCommit?.committer?.at ?? home?.lastCommit?.author?.at;
		return ts ? timestampDate(ts) : null;
	});
	const homeAuthor = $derived(home?.lastCommit?.author?.username || home?.lastCommit?.author?.name || '');

	$effect(() => {
		void gitRepoStore.load(namespace, name);
	});

	$effect(() => {
		if (repo && repo.fullName !== loadedFor) {
			loadedFor = repo.fullName;
			void load();
		}
	});

	// SSH tab falls back when the url goes away
	$effect(() => {
		if (!sshUrl && cloneTab === 'ssh') cloneTab = 'http';
	});

	function wikiUrl(url: string | undefined): string {
		return url ? url.replace(/\.git$/, '.wiki.git') : '';
	}

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.wiki.listWikiPages(
				{ namespace, name, page: { pageSize: 500, pageToken: '', query: { text: '', filters: [] }, orderBy: '' } },
				silentCallOptions
			);
			pages = resp.pages;
			exists = resp.exists;
			httpUrl = resp.httpCloneUrl || wikiUrl(repo?.httpCloneUrl) || portalStore.httpCloneURL(namespace, name, true);
			sshUrl = resp.sshCloneUrl || wikiUrl(repo?.sshCloneUrl);
			home = undefined;
			if (exists && pages.some((p) => p.title === 'Home')) {
				try {
					const h = await rpcClient.wiki.getWikiPage({ namespace, name, title: 'Home', sha: '' }, silentCallOptions);
					home = h.page;
				} catch {
					home = undefined;
				}
			}
		} catch {
			pages = [];
			exists = false;
		} finally {
			loading = false;
		}
	}
</script>

<svelte:head>
	<title>Wiki - {namespace}/{name}</title>
</svelte:head>

{#snippet clone()}
	<div class="rounded-xl border border-border/60 overflow-hidden">
		<div class="flex items-center gap-1 px-3 border-b border-border/40 bg-muted/20">
			<span class="text-[12.5px] font-semibold py-2 mr-auto">Clone this wiki</span>
			<button
				type="button"
				class="px-2 py-1.5 text-[12px] font-medium border-b-2 -mb-px transition-colors {cloneTab === 'http' ? 'border-primary text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}"
				onclick={() => (cloneTab = 'http')}
			>
				HTTPS
			</button>
			{#if sshUrl}
				<button
					type="button"
					class="px-2 py-1.5 text-[12px] font-medium border-b-2 -mb-px transition-colors {cloneTab === 'ssh' ? 'border-primary text-foreground' : 'border-transparent text-muted-foreground hover:text-foreground'}"
					onclick={() => (cloneTab = 'ssh')}
				>
					SSH
				</button>
			{/if}
		</div>
		<div class="p-3 space-y-2">
			<div class="rounded-lg bg-muted/30 border border-border/40 pl-3 pr-1 py-1.5 flex items-center gap-2">
				<code class="text-[12px] font-mono text-foreground/90 flex-1 min-w-0 truncate select-all">{cloneUrl}</code>
				<CopyButton text={cloneUrl} label="Clone URL copied!" />
			</div>
			<p class="text-[11.5px] text-muted-foreground">Pages are markdown files, push to publish.</p>
		</div>
	</div>
{/snippet}

{#if loading}
	<div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_18rem]">
		<Skeleton class="h-96 w-full rounded-xl" />
		<Skeleton class="h-64 w-full rounded-xl" />
	</div>
{:else if !exists || pages.length === 0}
	<EmptyState icon={BookOpen} message="This wiki is empty" description="Wiki pages are markdown files committed to a separate repository you can also clone and push.">
		{#snippet actions()}
			<PermissionGate allowed={canWrite}>
				<Button href={resolve(`${newHref}?title=Home` as `/${string}`)}>
					<Plus class="h-3.5 w-3.5 mr-1.5" />Create First Page
				</Button>
			</PermissionGate>
		{/snippet}
	</EmptyState>
{:else}
	<div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_18rem]">
		<div class="min-w-0 space-y-4">
			{#if home}
				<article class="rounded-xl border border-border/60 bg-card overflow-hidden">
					<header class="flex items-center gap-3 px-5 py-3.5 border-b border-border/40 bg-muted/20 flex-wrap">
						<BookOpen class="h-4 w-4 text-primary shrink-0" />
						<div class="min-w-0 flex-1">
							<h1 class="text-[15px] font-semibold truncate">{home.title}</h1>
							{#if home.lastCommit}
								<p class="text-[12px] text-muted-foreground truncate">
									Last edited
									{#if homeAuthor}
										by <a href={resolve('/[namespace]', { namespace: homeAuthor })} class="font-medium text-foreground/80 hover:text-primary">{homeAuthor}</a>
									{/if}
									{#if homeDate}
										<span title={homeDate.toLocaleString()}>{relativeTime(homeDate)}</span>
									{/if}
								</p>
							{/if}
						</div>
						<div class="flex items-center gap-2 flex-wrap">
							<Button variant="outline" size="sm" class="h-8" href={resolve('/[namespace]/[name]/wiki/[page]/history', { namespace, name, page: 'Home' })}>
								<History class="h-3.5 w-3.5 mr-1.5" />History
							</Button>
							<PermissionGate allowed={canWrite}>
								<Button size="sm" class="h-8" href={resolve('/[namespace]/[name]/wiki/[page]/edit', { namespace, name, page: 'Home' })}>
									<Pencil class="h-3.5 w-3.5 mr-1.5" />Edit
								</Button>
							</PermissionGate>
						</div>
					</header>
					<div class="px-5 py-4 min-w-0 overflow-x-auto">
						<Markdown source={home.content} baseHref="{base}/" wikiBase="{base}/" {rawBase} />
					</div>
				</article>
			{:else}
				<EmptyState icon={BookOpen} message="No Home page" description="Pick a page from the list or create Home to land here.">
					{#snippet actions()}
						<PermissionGate allowed={canWrite}>
							<Button href={resolve(`${newHref}?title=Home` as `/${string}`)}>
								<Plus class="h-3.5 w-3.5 mr-1.5" />Create Home Page
							</Button>
						</PermissionGate>
					{/snippet}
				</EmptyState>
			{/if}
			<div class="lg:hidden">
				{@render clone()}
			</div>
		</div>
		<WikiSidebar
			{namespace}
			{name}
			{pages}
			current="Home"
			sidebar={home?.sidebar ?? ''}
			footer={home?.footer ?? ''}
			{rawBase}
			{canWrite}
			class="order-first lg:order-none"
			extra={clone}
		/>
	</div>
{/if}
