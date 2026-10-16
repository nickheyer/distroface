<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import {
		Code, GitBranch, Tag, BookOpen, Plus, Package, Archive, Users, Link as LinkIcon, Star, GitFork, Clock, ChevronDown, Upload, FilePlus
	} from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import {
		DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger
	} from '$lib/components/ui/dropdown-menu';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import CloneBox from '$lib/components/git/clone-box.svelte';
	import RefPicker from '$lib/components/git/ref-picker.svelte';
	import TreeTable from '$lib/components/git/tree-table.svelte';
	import Markdown from '$lib/components/git/markdown.svelte';
	import LatestCommitBar from '$lib/components/git/latest-commit-bar.svelte';
	import ForkContributeBar from '$lib/components/git/fork-contribute-bar.svelte';
	import { rpcClient, silentCallOptions, backgroundCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { GitUnit, LinkedRepositoryKind } from '$lib/proto/distroface/v1/git_pb';
	import type { GitTreeEntry, GitCommit, GitBlob, LinkedRepository, GitLanguage, GitContributor } from '$lib/proto/distroface/v1/git_pb';
	import type { Release } from '$lib/proto/distroface/v1/release_pb';
	import { relativeTime, formatBytes } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const ref = $derived(repo.defaultBranch);
	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.CODE) && !repo.isArchived);
	const params = $derived({ namespace, name });
	const refParams = $derived({ namespace, name, ref: ref || 'main', path: '' });

	let entries = $state<GitTreeEntry[]>([]);
	let lastCommit = $state<GitCommit | undefined>();
	let treeLoading = $state(true);
	let readme = $state<GitBlob | undefined>();
	let latestRelease = $state<Release | undefined>();
	let linked = $state<LinkedRepository[]>([]);
	let languages = $state<GitLanguage[]>([]);
	let contributors = $state<GitContributor[]>([]);
	let contributorTotal = $state(0);

	let loadedFor = '';

	async function loadAll() {
		treeLoading = true;
		const ns = namespace;
		const n = name;
		const opts = silentCallOptions;
		try {
			const [tree, rd] = await Promise.all([
				rpcClient.gitFiles.getTree({ namespace: ns, name: n, ref: '', path: '', withLastCommit: true }, opts).catch(() => undefined),
				rpcClient.gitFiles.getReadme({ namespace: ns, name: n, ref: '', path: '' }, opts).catch(() => undefined)
			]);
			entries = tree?.entries ?? [];
			lastCommit = tree?.lastCommit;
			readme = rd?.blob;
		} finally {
			treeLoading = false;
		}
		void Promise.all([
			gitRepoStore.unitEnabled(GitUnit.RELEASES)
				? rpcClient.release.getLatestRelease({ namespace: ns, name: n }, opts).then((r) => (latestRelease = r.release)).catch(() => (latestRelease = undefined))
				: Promise.resolve(),
			gitRepoStore.unitEnabled(GitUnit.PACKAGES)
				? rpcClient.git.listLinkedRepositories({ namespace: ns, name: n }, opts).then((r) => (linked = r.repositories)).catch(() => (linked = []))
				: Promise.resolve(),
			rpcClient.git.getRepositoryLanguages({ namespace: ns, name: n }, backgroundCallOptions).then((r) => (languages = r.languages)).catch(() => (languages = [])),
			rpcClient.git
				.listContributors({ namespace: ns, name: n, page: { pageSize: 12, pageToken: '', orderBy: '' } }, backgroundCallOptions)
				.then((r) => {
					contributors = r.contributors;
					contributorTotal = Number(r.page?.totalCount ?? 0n) || r.contributors.length;
				})
				.catch(() => (contributors = []))
		]);
	}

	$effect(() => {
		const key = `${repo.id}:${repo.defaultBranch}:${repo.isEmpty}`;
		if (key === loadedFor) return;
		loadedFor = key;
		if (repo.isEmpty) {
			entries = [];
			lastCommit = undefined;
			readme = undefined;
			treeLoading = false;
			return;
		}
		loadAll();
	});

	function onRef(next: string) {
		goto(resolve('/[namespace]/[name]/tree/[ref]/[...path]', { namespace, name, ref: next, path: '' }));
	}

	const encodedRef = $derived(encodeURIComponent(ref));
	const readmeText = $derived(readme ? new TextDecoder().decode(readme.content) : '');
	const readmeDir = $derived(readme?.path.includes('/') ? readme.path.slice(0, readme.path.lastIndexOf('/')) : '');
	const readmeBase = $derived(`/${namespace}/${name}/blob/${encodedRef}/${readmeDir ? `${readmeDir}/` : ''}`);
	const readmeRaw = $derived(`/${namespace}/${name}/-/raw/${encodedRef}/${readmeDir ? `${readmeDir}/` : ''}`);

	const httpUrl = $derived(repo.httpCloneUrl);
	const stars = $derived(Number(repo.starCount));

	function linkedHref(l: LinkedRepository): string {
		return l.kind === LinkedRepositoryKind.IMAGE
			? resolve('/images/[namespace]/[...name]', { namespace: l.namespace, name: l.name })
			: resolve('/artifacts/[namespace]/[repo]', { namespace: l.namespace, repo: l.name });
	}

	function topicHref(topic: string): string {
		return `${resolve('/git')}?${new URLSearchParams({ topic })}`;
	}

	function contributorInitials(c: GitContributor): string {
		const n = c.username || c.displayName || c.email || '?';
		return n.split(/[\s-]+/).map((w) => w[0]).join('').toUpperCase().slice(0, 2);
	}
</script>

{#if repo.isEmpty}
	<div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_18rem]">
		<div class="min-w-0 space-y-5">
			<div class="rounded-xl border border-border/60 bg-card overflow-hidden">
				<div class="flex flex-wrap items-start justify-between gap-3 px-5 py-4 border-b border-border/40 bg-muted/20">
					<div class="min-w-0">
						<h2 class="text-sm font-semibold">Quick setup</h2>
						<p class="text-[13px] text-muted-foreground mt-0.5">This repository is empty. Push something to get started.</p>
					</div>
					<Popover>
						<PopoverTrigger>
							{#snippet child({ props })}
								<Button {...props} size="sm" class="h-8 gap-1.5"><Code class="h-3.5 w-3.5" />Clone<ChevronDown class="h-3 w-3 opacity-70" /></Button>
							{/snippet}
						</PopoverTrigger>
						<PopoverContent class="w-[min(24rem,calc(100vw-2rem))]" align="end">
							<CloneBox {repo} />
						</PopoverContent>
					</Popover>
				</div>

				<div class="p-5 space-y-5">
					<div class="space-y-2">
						<h3 class="text-[13px] font-semibold">Push an existing repository</h3>
						<div class="relative">
							<pre class="code-inline text-xs whitespace-pre overflow-x-auto pr-10">git remote add origin {httpUrl}
git push -u origin {ref || 'main'}</pre>
							<div class="absolute top-2 right-2">
								<CopyButton text={`git remote add origin ${httpUrl}\ngit push -u origin ${ref || 'main'}`} label="Commands copied!" />
							</div>
						</div>
					</div>

					<div class="space-y-2">
						<h3 class="text-[13px] font-semibold">Start a new one on the command line</h3>
						<div class="relative">
							<pre class="code-inline text-xs whitespace-pre overflow-x-auto pr-10">git init -b {ref || 'main'}
git add README.md
git commit -m "first commit"
git remote add origin {httpUrl}
git push -u origin {ref || 'main'}</pre>
							<div class="absolute top-2 right-2">
								<CopyButton text={`git init -b ${ref || 'main'}\ngit add README.md\ngit commit -m "first commit"\ngit remote add origin ${httpUrl}\ngit push -u origin ${ref || 'main'}`} label="Commands copied!" />
							</div>
						</div>
					</div>

					<PermissionGate allowed={canWrite}>
						<div class="flex flex-wrap items-center gap-2 pt-4 border-t border-border/50">
							<span class="text-[13px] text-muted-foreground">Or start in the browser</span>
							<Button variant="outline" size="sm" class="h-8" href={resolve('/[namespace]/[name]/new/[ref]/[...path]', refParams)}>
								<FilePlus class="h-3.5 w-3.5 mr-1.5" />Create File
							</Button>
							<Button variant="outline" size="sm" class="h-8" href={resolve('/[namespace]/[name]/upload/[ref]/[...path]', refParams)}>
								<Upload class="h-3.5 w-3.5 mr-1.5" />Upload Files
							</Button>
						</div>
					</PermissionGate>
				</div>
			</div>
		</div>
		<aside class="min-w-0">
			{@render about()}
		</aside>
	</div>
{:else}
	<div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_18rem]">
		<div class="lg:hidden min-w-0">
			{@render about()}
		</div>

		<div class="space-y-4 min-w-0">
			<div class="flex flex-wrap items-center gap-2">
				<RefPicker {namespace} {name} current={ref} defaultBranch={repo.defaultBranch} onSelect={onRef} />
				<a href={resolve('/[namespace]/[name]/branches', params)} class="inline-flex items-center gap-1 text-[13px] text-muted-foreground hover:text-primary px-1">
					<GitBranch class="h-3.5 w-3.5" />
					<span class="font-medium text-foreground/80">{repo.branchCount}</span> branch{repo.branchCount !== 1 ? 'es' : ''}
				</a>
				<a href={resolve('/[namespace]/[name]/tags', params)} class="inline-flex items-center gap-1 text-[13px] text-muted-foreground hover:text-primary px-1">
					<Tag class="h-3.5 w-3.5" />
					<span class="font-medium text-foreground/80">{repo.tagCount}</span> tag{repo.tagCount !== 1 ? 's' : ''}
				</a>
				<div class="ml-auto flex items-center gap-2">
					<PermissionGate allowed={canWrite}>
						<DropdownMenu>
							<DropdownMenuTrigger>
								{#snippet child({ props })}
									<Button {...props} variant="outline" size="sm" class="h-8 gap-1.5"><Plus class="h-3.5 w-3.5" />Add file<ChevronDown class="h-3 w-3 opacity-70" /></Button>
								{/snippet}
							</DropdownMenuTrigger>
							<DropdownMenuContent align="end">
								<DropdownMenuItem onclick={() => goto(resolve('/[namespace]/[name]/new/[ref]/[...path]', refParams))}>
									<FilePlus class="h-4 w-4 mr-2" />Create new file
								</DropdownMenuItem>
								<DropdownMenuItem onclick={() => goto(resolve('/[namespace]/[name]/upload/[ref]/[...path]', refParams))}>
									<Upload class="h-4 w-4 mr-2" />Upload files
								</DropdownMenuItem>
							</DropdownMenuContent>
						</DropdownMenu>
					</PermissionGate>
					<Popover>
						<PopoverTrigger>
							{#snippet child({ props })}
								<Button {...props} size="sm" class="h-8 gap-1.5"><Code class="h-3.5 w-3.5" />Code<ChevronDown class="h-3 w-3 opacity-70" /></Button>
							{/snippet}
						</PopoverTrigger>
						<PopoverContent class="w-[min(24rem,calc(100vw-2rem))]" align="end">
							<CloneBox {repo} {ref} />
						</PopoverContent>
					</Popover>
				</div>
			</div>

			{#if repo.forkOf}
				<ForkContributeBar {repo} branch={ref} />
			{/if}
			<LatestCommitBar commit={lastCommit} {namespace} {name} {ref} loading={treeLoading} />

			<TreeTable {entries} {namespace} {name} {ref} loading={treeLoading} />

			{#if readme}
				<div class="rounded-xl border border-border/60 bg-card overflow-hidden" id="readme">
					<div class="flex items-center gap-2 px-4 py-2.5 border-b border-border/50 bg-muted/20">
						<BookOpen class="h-4 w-4 text-muted-foreground/70" />
						<a href={resolve('/[namespace]/[name]/blob/[ref]/[...path]', { namespace, name, ref, path: readme.path })} class="text-[13px] font-medium hover:text-primary truncate">{readme.name}</a>
					</div>
					<div class="px-4 py-4 sm:px-6 sm:py-5 overflow-x-auto">
						<Markdown source={readmeText} baseHref={readmeBase} rawBase={readmeRaw} />
					</div>
				</div>
			{/if}
		</div>

		<aside class="min-w-0 grid gap-6 sm:grid-cols-2 lg:grid-cols-1 lg:content-start">
			<div class="hidden lg:block">
				{@render about()}
			</div>

			{#if gitRepoStore.unitEnabled(GitUnit.RELEASES)}
				<section class="space-y-2 min-w-0">
					<div class="flex items-center justify-between">
						<h3 class="text-[13px] font-semibold">Releases</h3>
						{#if repo.releaseCount > 0}
							<a href={resolve('/[namespace]/[name]/releases', params)} class="text-[12px] text-muted-foreground hover:text-primary">{repo.releaseCount} total</a>
						{/if}
					</div>
					{#if latestRelease}
						<a href={resolve('/[namespace]/[name]/releases/[tag]', { namespace, name, tag: latestRelease.tag })} class="block rounded-lg border border-border/60 px-3 py-2.5 hover:border-primary/30 transition-colors">
							<div class="flex items-center gap-2 min-w-0">
								<Tag class="h-3.5 w-3.5 text-primary/70 shrink-0" />
								<span class="text-[13px] font-medium truncate">{latestRelease.name || latestRelease.tag}</span>
								<Badge variant="outline" class="text-[10px] h-4.5 py-0 ml-auto shrink-0 border-emerald-500/30 text-emerald-600 dark:text-emerald-400">Latest</Badge>
							</div>
							{#if latestRelease.publishedAt}
								<p class="text-[12px] text-muted-foreground mt-1">{relativeTime(timestampDate(latestRelease.publishedAt))}</p>
							{/if}
						</a>
					{:else}
						<p class="text-[12.5px] text-muted-foreground">No releases published.
							<PermissionGate allowed={gitRepoStore.canWrite(GitUnit.RELEASES)}>
								<a href={resolve('/[namespace]/[name]/releases/new', params)} class="text-primary hover:underline">Create one</a>
							</PermissionGate>
						</p>
					{/if}
				</section>
			{/if}

			{#if gitRepoStore.unitEnabled(GitUnit.PACKAGES)}
				<section class="space-y-2 min-w-0">
					<div class="flex items-center justify-between">
						<h3 class="text-[13px] font-semibold">Packages</h3>
						<a href={resolve('/[namespace]/[name]/packages', params)} class="text-[12px] text-muted-foreground hover:text-primary">Manage</a>
					</div>
					{#if linked.length > 0}
						<ul class="space-y-1">
							{#each linked as l (l.kind + l.fullName)}
								<li>
									<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
									<a href={linkedHref(l)} class="flex items-center gap-2 rounded-lg border border-border/60 px-3 py-2 hover:border-primary/30 transition-colors text-[13px] min-w-0">
										{#if l.kind === LinkedRepositoryKind.IMAGE}
											<Package class="h-3.5 w-3.5 text-muted-foreground shrink-0" />
										{:else}
											<Archive class="h-3.5 w-3.5 text-muted-foreground shrink-0" />
										{/if}
										<span class="truncate">{l.fullName}</span>
										{#if l.latest}
											<span class="ml-auto font-mono text-[11px] text-muted-foreground truncate max-w-[6rem] shrink-0">{l.latest}</span>
										{/if}
									</a>
								</li>
							{/each}
						</ul>
					{:else}
						<p class="text-[12.5px] text-muted-foreground">No linked packages.</p>
					{/if}
				</section>
			{/if}

			{#if languages.length > 0}
				<section class="space-y-2 min-w-0">
					<h3 class="text-[13px] font-semibold">Languages</h3>
					<div class="flex h-2 rounded-full overflow-hidden bg-muted">
						{#each languages as lang (lang.name)}
							<div style="width: {lang.percent}%; background-color: {lang.color || 'var(--primary)'}" title="{lang.name} {lang.percent.toFixed(1)}%"></div>
						{/each}
					</div>
					<ul class="flex flex-wrap gap-x-4 gap-y-1">
						{#each languages.slice(0, 8) as lang (lang.name)}
							<li class="flex items-center gap-1.5 text-[12px]">
								<span class="h-2 w-2 rounded-full" style="background-color: {lang.color || 'var(--primary)'}"></span>
								<span class="font-medium">{lang.name}</span>
								<span class="text-muted-foreground tabular-nums">{lang.percent.toFixed(1)}%</span>
							</li>
						{/each}
					</ul>
				</section>
			{/if}

			{#if contributors.length > 0}
				<section class="space-y-2 min-w-0">
					<div class="flex items-center justify-between">
						<h3 class="text-[13px] font-semibold flex items-center gap-1.5"><Users class="h-3.5 w-3.5 text-muted-foreground" />Contributors</h3>
						<a href={resolve('/[namespace]/[name]/activity', params)} class="text-[12px] text-muted-foreground hover:text-primary">{contributorTotal || contributors.length}</a>
					</div>
					<div class="flex flex-wrap gap-1.5">
						{#each contributors as c (c.email + c.username)}
							{#if c.username}
								<a href={resolve('/[namespace]', { namespace: c.username })} class="h-8 w-8 rounded-full bg-primary/10 text-primary/80 flex items-center justify-center text-[11px] font-bold hover:bg-primary/20 transition-colors" title="{c.username}, {c.commits} commit{c.commits !== 1 ? 's' : ''}">
									{contributorInitials(c)}
								</a>
							{:else}
								<span class="h-8 w-8 rounded-full bg-muted text-muted-foreground flex items-center justify-center text-[11px] font-bold" title="{c.displayName || c.email}, {c.commits} commit{c.commits !== 1 ? 's' : ''}">
									{contributorInitials(c)}
								</span>
							{/if}
						{/each}
					</div>
				</section>
			{/if}
		</aside>
	</div>
{/if}

{#snippet about()}
	<section class="space-y-3 min-w-0">
		<h3 class="text-[13px] font-semibold">About</h3>
		{#if repo.description}
			<p class="text-[13px] text-muted-foreground break-words">{repo.description}</p>
		{:else}
			<p class="text-[13px] text-muted-foreground/60 italic">No description</p>
		{/if}
		{#if repo.website}
			<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
			<a href={repo.website} target="_blank" rel="noopener noreferrer" class="flex items-center gap-1.5 text-[12.5px] text-primary hover:underline min-w-0">
				<LinkIcon class="h-3.5 w-3.5 shrink-0" /><span class="truncate">{repo.website.replace(/^https?:\/\//, '')}</span>
			</a>
		{/if}
		{#if repo.topics.length > 0}
			<div class="flex flex-wrap gap-1.5">
				{#each repo.topics as topic (topic)}
					<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
					<a href={topicHref(topic)} class="rounded-full bg-primary/8 text-primary/80 hover:bg-primary/15 px-2.5 py-0.5 text-[12px] transition-colors">{topic}</a>
				{/each}
			</div>
		{/if}
		<ul class="flex flex-wrap gap-x-4 gap-y-1.5 lg:flex-col lg:gap-y-1.5 text-[12.5px] text-muted-foreground">
			<li class="flex items-center gap-2"><Star class="h-3.5 w-3.5" /><span class="tabular-nums">{stars.toLocaleString()}</span> star{stars !== 1 ? 's' : ''}</li>
			<li class="flex items-center gap-2"><GitFork class="h-3.5 w-3.5" /><span class="tabular-nums">{repo.forkCount}</span> fork{repo.forkCount !== 1 ? 's' : ''}</li>
			{#if Number(repo.sizeBytes) > 0}
				<li class="flex items-center gap-2"><Archive class="h-3.5 w-3.5" />{formatBytes(Number(repo.sizeBytes))}{#if Number(repo.lfsSizeBytes) > 0}<span class="text-muted-foreground/60">+ {formatBytes(Number(repo.lfsSizeBytes))} LFS</span>{/if}</li>
			{/if}
			{#if repo.lastPushedAt}
				<li class="flex items-center gap-2"><Clock class="h-3.5 w-3.5" />Pushed {relativeTime(timestampDate(repo.lastPushedAt))}</li>
			{/if}
		</ul>
	</section>
{/snippet}
