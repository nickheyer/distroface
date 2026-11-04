<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { safeHref } from '$lib/utils';
	import {
		Code,
		GitBranch,
		Tag,
		BookOpen,
		Plus,
		Package,
		Archive,
		Users,
		Link as LinkIcon,
		Star,
		GitFork,
		Clock,
		ChevronDown,
		Upload,
		FilePlus
	} from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import {
		DropdownMenu,
		DropdownMenuContent,
		DropdownMenuItem,
		DropdownMenuTrigger
	} from '$lib/components/ui/dropdown-menu';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import CloneBox from '$lib/components/git/clone-box.svelte';
	import RefPicker from '$lib/components/git/ref-picker.svelte';
	import TreeTable from '$lib/components/git/tree-table.svelte';
	import Markdown from '$lib/components/git/markdown.svelte';
	import LatestCommitBar from '$lib/components/git/latest-commit-bar.svelte';
	import ForkContributeBar from '$lib/components/git/fork-contribute-bar.svelte';
	import BotBadge from '$lib/components/bot-badge.svelte';
	import { rpcClient, silentCallOptions, backgroundCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { GitUnit, LinkedRepositoryKind } from '$lib/proto/distroface/v1/git_pb';
	import type {
		GitTreeEntry,
		GitCommit,
		GitBlob,
		LinkedRepository,
		GitLanguage,
		GitContributor
	} from '$lib/proto/distroface/v1/git_pb';
	import type { Release } from '$lib/proto/distroface/v1/release_pb';
	import { relativeTime, formatBytes } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const ref = $derived(repo.defaultBranch);
	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.CODE) && !gitRepoStore.readOnly);
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
				rpcClient.gitFiles
					.getTree({ namespace: ns, name: n, ref: '', path: '', withLastCommit: true }, opts)
					.catch(() => undefined),
				rpcClient.gitFiles
					.getReadme({ namespace: ns, name: n, ref: '', path: '' }, opts)
					.catch(() => undefined)
			]);
			entries = tree?.entries ?? [];
			lastCommit = tree?.lastCommit;
			readme = rd?.blob;
		} finally {
			treeLoading = false;
		}
		void Promise.all([
			gitRepoStore.unitEnabled(GitUnit.RELEASES)
				? rpcClient.release
						.getLatestRelease({ namespace: ns, name: n }, opts)
						.then((r) => (latestRelease = r.release))
						.catch(() => (latestRelease = undefined))
				: Promise.resolve(),
			gitRepoStore.unitEnabled(GitUnit.PACKAGES)
				? rpcClient.git
						.listLinkedRepositories({ namespace: ns, name: n }, opts)
						.then((r) => (linked = r.repositories))
						.catch(() => (linked = []))
				: Promise.resolve(),
			rpcClient.git
				.getRepositoryLanguages({ namespace: ns, name: n }, backgroundCallOptions)
				.then((r) => (languages = r.languages))
				.catch(() => (languages = [])),
			rpcClient.git
				.listContributors(
					{ namespace: ns, name: n, page: { pageSize: 12, pageToken: '', orderBy: '' } },
					backgroundCallOptions
				)
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
		goto(
			resolve('/[namespace]/[name]/tree/[ref]/[...path]', { namespace, name, ref: next, path: '' })
		);
	}

	const encodedRef = $derived(encodeURIComponent(ref));
	const readmeText = $derived(readme ? new TextDecoder().decode(readme.content) : '');
	const readmeDir = $derived(
		readme?.path.includes('/') ? readme.path.slice(0, readme.path.lastIndexOf('/')) : ''
	);
	const readmeBase = $derived(
		`/${namespace}/${name}/blob/${encodedRef}/${readmeDir ? `${readmeDir}/` : ''}`
	);
	const readmeRaw = $derived(
		`/${namespace}/${name}/-/raw/${encodedRef}/${readmeDir ? `${readmeDir}/` : ''}`
	);

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
		return n
			.split(/[\s-]+/)
			.map((w) => w[0])
			.join('')
			.toUpperCase()
			.slice(0, 2);
	}
</script>

{#if repo.isEmpty}
	<div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_18rem]">
		<div class="min-w-0 space-y-4">
			<div class="box">
				<div class="box-header">
					<span>Quick setup</span>
					<Popover>
						<PopoverTrigger>
							{#snippet child({ props })}
								<Button {...props} size="sm"><Code />Clone<ChevronDown class="opacity-70" /></Button
								>
							{/snippet}
						</PopoverTrigger>
						<PopoverContent class="w-[min(24rem,calc(100vw-2rem))]" align="end">
							<CloneBox {repo} />
						</PopoverContent>
					</Popover>
				</div>

				<div class="space-y-5 p-4">
					{#if gitRepoStore.isMirror}
						<p class="text-sm text-muted-foreground">
							Nothing has been fetched from the upstream yet. Branches and tags appear here after
							the first sync.
						</p>
					{:else}
						<p class="text-sm text-muted-foreground">
							This repository is empty. Push something to get started.
						</p>

						<div class="space-y-2">
							<h3 class="text-sm font-semibold">Push an existing repository</h3>
							<div class="relative">
								<pre
									class="code-inline overflow-x-auto pr-10 whitespace-pre">git remote add origin {httpUrl}
git push -u origin {ref || 'main'}</pre>
								<div class="absolute top-1.5 right-1.5">
									<CopyButton
										text={`git remote add origin ${httpUrl}\ngit push -u origin ${ref || 'main'}`}
										label="Commands copied"
									/>
								</div>
							</div>
						</div>

						<div class="space-y-2">
							<h3 class="text-sm font-semibold">Start a new one on the command line</h3>
							<div class="relative">
								<pre class="code-inline overflow-x-auto pr-10 whitespace-pre">git init -b {ref ||
										'main'}
git add README.md
git commit -m "first commit"
git remote add origin {httpUrl}
git push -u origin {ref || 'main'}</pre>
								<div class="absolute top-1.5 right-1.5">
									<CopyButton
										text={`git init -b ${ref || 'main'}\ngit add README.md\ngit commit -m "first commit"\ngit remote add origin ${httpUrl}\ngit push -u origin ${ref || 'main'}`}
										label="Commands copied"
									/>
								</div>
							</div>
						</div>
					{/if}
					<PermissionGate allowed={canWrite}>
						<div class="flex flex-wrap items-center gap-2 border-t pt-4">
							<span class="text-sm text-muted-foreground">Or start in the browser</span>
							<Button
								variant="outline"
								size="sm"
								href={resolve('/[namespace]/[name]/new/[ref]/[...path]', refParams)}
							>
								<FilePlus />Create file
							</Button>
							<Button
								variant="outline"
								size="sm"
								href={resolve('/[namespace]/[name]/upload/[ref]/[...path]', refParams)}
							>
								<Upload />Upload files
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
		<div class="min-w-0 lg:hidden">
			{@render about()}
		</div>

		<div class="min-w-0 space-y-4">
			<div class="flex flex-wrap items-center gap-2">
				<RefPicker
					{namespace}
					{name}
					current={ref}
					defaultBranch={repo.defaultBranch}
					onSelect={onRef}
				/>
				<a
					href={resolve('/[namespace]/[name]/branches', params)}
					class="inline-flex items-center gap-1 px-1 text-sm text-muted-foreground hover:text-foreground"
				>
					<GitBranch class="size-3.5" />
					<span class="font-medium text-foreground">{repo.branchCount}</span>
					branch{repo.branchCount !== 1 ? 'es' : ''}
				</a>
				<a
					href={resolve('/[namespace]/[name]/tags', params)}
					class="inline-flex items-center gap-1 px-1 text-sm text-muted-foreground hover:text-foreground"
				>
					<Tag class="size-3.5" />
					<span class="font-medium text-foreground">{repo.tagCount}</span> tag{repo.tagCount !== 1
						? 's'
						: ''}
				</a>
				<div class="ml-auto flex items-center gap-2">
					<PermissionGate allowed={canWrite}>
						<DropdownMenu>
							<DropdownMenuTrigger>
								{#snippet child({ props })}
									<Button {...props} variant="outline"
										><Plus />Add file<ChevronDown class="opacity-70" /></Button
									>
								{/snippet}
							</DropdownMenuTrigger>
							<DropdownMenuContent align="end">
								<DropdownMenuItem
									onclick={() =>
										goto(resolve('/[namespace]/[name]/new/[ref]/[...path]', refParams))}
								>
									<FilePlus />Create new file
								</DropdownMenuItem>
								<DropdownMenuItem
									onclick={() =>
										goto(resolve('/[namespace]/[name]/upload/[ref]/[...path]', refParams))}
								>
									<Upload />Upload files
								</DropdownMenuItem>
							</DropdownMenuContent>
						</DropdownMenu>
					</PermissionGate>
					<Popover>
						<PopoverTrigger>
							{#snippet child({ props })}
								<Button {...props}><Code />Code<ChevronDown class="opacity-70" /></Button>
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
			<div class="box">
				<LatestCommitBar commit={lastCommit} {namespace} {name} {ref} loading={treeLoading} />
				<TreeTable {entries} {namespace} {name} {ref} loading={treeLoading} />
			</div>

			{#if readme}
				<div class="box" id="readme">
					<div class="box-header">
						<a
							href={resolve('/[namespace]/[name]/blob/[ref]/[...path]', {
								namespace,
								name,
								ref,
								path: readme.path
							})}
							class="flex min-w-0 items-center gap-2 hover:text-primary"
						>
							<BookOpen class="size-4 text-muted-foreground" />
							<span class="truncate">{readme.name}</span>
						</a>
					</div>
					<div class="overflow-x-auto px-4 py-4 sm:px-6 sm:py-5">
						<Markdown
							source={readmeText}
							baseHref={readmeBase}
							rawBase={readmeRaw}
							class="markdown-readme"
						/>
					</div>
				</div>
			{/if}
		</div>

		<aside class="grid min-w-0 gap-6 sm:grid-cols-2 lg:grid-cols-1 lg:content-start">
			<div class="hidden lg:block">
				{@render about()}
			</div>

			{#if gitRepoStore.unitEnabled(GitUnit.RELEASES)}
				<section class="min-w-0 space-y-2">
					<div class="flex items-center justify-between">
						<h3 class="text-sm font-semibold">Releases</h3>
						{#if repo.releaseCount > 0}
							<a
								href={resolve('/[namespace]/[name]/releases', params)}
								class="text-xs text-muted-foreground hover:text-primary"
								>{repo.releaseCount} total</a
							>
						{/if}
					</div>
					{#if latestRelease}
						<a
							href={resolve('/[namespace]/[name]/releases/[tag]', {
								namespace,
								name,
								tag: latestRelease.tag
							})}
							class="card-interactive block px-3 py-2.5"
						>
							<div class="flex min-w-0 items-center gap-2">
								<Tag class="size-3.5 shrink-0 text-muted-foreground" />
								<span class="truncate text-sm font-medium"
									>{latestRelease.name || latestRelease.tag}</span
								>
								<Badge variant="success" class="ml-auto shrink-0">Latest</Badge>
							</div>
							{#if latestRelease.publishedAt}
								<p class="mt-1 text-xs text-muted-foreground">
									{relativeTime(timestampDate(latestRelease.publishedAt))}
								</p>
							{/if}
						</a>
					{:else}
						<p class="text-sm text-muted-foreground">
							No releases published.
							<PermissionGate
								allowed={gitRepoStore.canWrite(GitUnit.RELEASES) && !gitRepoStore.isMirror}
							>
								<a href={resolve('/[namespace]/[name]/releases/new', params)} class="link"
									>Create one</a
								>
							</PermissionGate>
						</p>
					{/if}
				</section>
			{/if}

			{#if gitRepoStore.unitEnabled(GitUnit.PACKAGES)}
				<section class="min-w-0 space-y-2">
					<div class="flex items-center justify-between">
						<h3 class="text-sm font-semibold">Packages</h3>
						<a
							href={resolve('/[namespace]/[name]/packages', params)}
							class="text-xs text-muted-foreground hover:text-primary">Manage</a
						>
					</div>
					{#if linked.length > 0}
						<ul class="space-y-1">
							{#each linked as l (l.kind + l.fullName)}
								<li>
									<!-- eslint-disable svelte/no-navigation-without-resolve -->
									<a
										href={linkedHref(l)}
										class="card-interactive flex min-w-0 items-center gap-2 px-3 py-2 text-sm"
									>
										{#if l.kind === LinkedRepositoryKind.IMAGE}
											<Package class="size-3.5 shrink-0 text-muted-foreground" />
										{:else}
											<Archive class="size-3.5 shrink-0 text-muted-foreground" />
										{/if}
										<span class="truncate">{l.fullName}</span>
										{#if l.latest}
											<span
												class="ml-auto max-w-24 shrink-0 truncate font-mono text-2xs text-muted-foreground"
												>{l.latest}</span
											>
										{/if}
									</a>
									<!-- eslint-enable svelte/no-navigation-without-resolve -->
								</li>
							{/each}
						</ul>
					{:else}
						<p class="text-sm text-muted-foreground">No linked packages.</p>
					{/if}
				</section>
			{/if}

			{#if languages.length > 0}
				<section class="min-w-0 space-y-2">
					<h3 class="text-sm font-semibold">Languages</h3>
					<div class="flex h-2 overflow-hidden rounded-full bg-muted">
						{#each languages as lang (lang.name)}
							<div
								style="width: {lang.percent}%; background-color: {lang.color || 'var(--primary)'}"
								title="{lang.name} {lang.percent.toFixed(1)}%"
							></div>
						{/each}
					</div>
					<ul class="flex flex-wrap gap-x-4 gap-y-1">
						{#each languages.slice(0, 8) as lang (lang.name)}
							<li class="flex items-center gap-1.5 text-xs">
								<span
									class="size-2 rounded-full"
									style="background-color: {lang.color || 'var(--primary)'}"
								></span>
								<span class="font-medium">{lang.name}</span>
								<span class="text-muted-foreground tabular-nums">{lang.percent.toFixed(1)}%</span>
							</li>
						{/each}
					</ul>
				</section>
			{/if}

			{#if contributors.length > 0}
				<section class="min-w-0 space-y-2">
					<div class="flex items-center justify-between">
						<h3 class="flex items-center gap-1.5 text-sm font-semibold">
							<Users class="size-3.5 text-muted-foreground" />Contributors
						</h3>
						<a
							href={resolve('/[namespace]/[name]/activity', params)}
							class="text-xs text-muted-foreground tabular-nums hover:text-primary"
							>{contributorTotal || contributors.length}</a
						>
					</div>
					<div class="flex flex-wrap gap-1.5">
						{#each contributors as c (c.email + c.username)}
							{#if c.username}
								<a
									href={resolve('/[namespace]', { namespace: c.username })}
									class="inline-flex items-center gap-1 rounded-full transition-opacity hover:opacity-80"
									title="{c.username}, {c.commits} commit{c.commits !== 1 ? 's' : ''}"
								>
									<Avatar class="size-7">
										<AvatarFallback>{contributorInitials(c)}</AvatarFallback>
									</Avatar>
									{#if c.isAgent}<BotBadge />{/if}
								</a>
							{:else}
								<Avatar
									class="size-7"
									title="{c.displayName || c.email}, {c.commits} commit{c.commits !== 1 ? 's' : ''}"
								>
									<AvatarFallback>{contributorInitials(c)}</AvatarFallback>
								</Avatar>
							{/if}
						{/each}
					</div>
				</section>
			{/if}
		</aside>
	</div>
{/if}

{#snippet about()}
	<section class="min-w-0 space-y-3">
		<h3 class="text-sm font-semibold">About</h3>
		{#if repo.description}
			<p class="text-sm wrap-break-word text-muted-foreground">{repo.description}</p>
		{:else}
			<p class="text-sm text-muted-foreground italic">No description</p>
		{/if}
		{#if safeHref(repo.website)}
			<!-- eslint-disable svelte/no-navigation-without-resolve -->
			<a
				href={safeHref(repo.website)}
				target="_blank"
				rel="noopener noreferrer"
				class="link flex min-w-0 items-center gap-1.5 text-xs"
			>
				<LinkIcon class="size-3.5 shrink-0" /><span class="truncate"
					>{repo.website.replace(/^https?:\/\//, '')}</span
				>
			</a>
			<!-- eslint-enable svelte/no-navigation-without-resolve -->
		{/if}
		{#if repo.topics.length > 0}
			<div class="flex flex-wrap gap-1.5">
				{#each repo.topics as topic (topic)}
					<Badge href={topicHref(topic)}>{topic}</Badge>
				{/each}
			</div>
		{/if}
		<ul
			class="flex flex-wrap gap-x-4 gap-y-1.5 text-xs text-muted-foreground lg:flex-col lg:gap-y-1.5"
		>
			<li class="flex items-center gap-2">
				<Star class="size-3.5" /><span class="tabular-nums">{stars.toLocaleString()}</span>
				star{stars !== 1 ? 's' : ''}
			</li>
			<li class="flex items-center gap-2">
				<GitFork class="size-3.5" /><span class="tabular-nums">{repo.forkCount}</span>
				fork{repo.forkCount !== 1 ? 's' : ''}
			</li>
			{#if Number(repo.sizeBytes) > 0}
				<li class="flex items-center gap-2">
					<Archive class="size-3.5" />{formatBytes(
						Number(repo.sizeBytes)
					)}{#if Number(repo.lfsSizeBytes) > 0}<span
							>+ {formatBytes(Number(repo.lfsSizeBytes))} LFS</span
						>{/if}
				</li>
			{/if}
			{#if repo.lastPushedAt}
				<li class="flex items-center gap-2">
					<Clock class="size-3.5" />Pushed {relativeTime(timestampDate(repo.lastPushedAt))}
				</li>
			{/if}
		</ul>
	</section>
{/snippet}
