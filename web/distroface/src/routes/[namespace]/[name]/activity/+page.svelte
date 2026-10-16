<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from '$lib/components/ui/table';
	import EmptyState from '$lib/components/empty-state.svelte';
	import { Activity, Users, Code2, GitCommit } from '@lucide/svelte';
	import type { GitActivityWeek, GitContributor, GitLanguage, GitCommit as GitCommitType } from '$lib/proto/distroface/v1/git_pb';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);

	let weeks = $state<GitActivityWeek[]>([]);
	let contributors = $state<GitContributor[]>([]);
	let languages = $state<GitLanguage[]>([]);
	let commits = $state<GitCommitType[]>([]);
	let loaded = $state(false);
	let loadedFor = $state('');

	const maxCommits = $derived(Math.max(1, ...weeks.map((w) => w.commits)));
	const totalCommits = $derived(weeks.reduce((n, w) => n + w.commits, 0));
	const totalAdditions = $derived(weeks.reduce((n, w) => n + w.additions, 0));
	const totalDeletions = $derived(weeks.reduce((n, w) => n + w.deletions, 0));
	const recentWeeks = $derived(weeks.slice(-26));

	$effect(() => {
		void gitRepoStore.load(namespace, name);
	});

	$effect(() => {
		if (repo && repo.fullName !== loadedFor) {
			loadedFor = repo.fullName;
			void load();
		}
	});

	async function load() {
		const results = await Promise.allSettled([
			rpcClient.git.getRepositoryActivity({ namespace, name }, silentCallOptions),
			rpcClient.git.listContributors({ namespace, name, page: { pageSize: 50 } }, silentCallOptions),
			rpcClient.git.getRepositoryLanguages({ namespace, name }, silentCallOptions),
			rpcClient.gitFiles.listCommits({ namespace, name, ref: repo?.defaultBranch ?? '', page: { pageSize: 10 } }, silentCallOptions)
		]);
		weeks = results[0].status === 'fulfilled' ? results[0].value.weeks : [];
		contributors = results[1].status === 'fulfilled' ? results[1].value.contributors : [];
		languages = results[2].status === 'fulfilled' ? results[2].value.languages : [];
		commits = results[3].status === 'fulfilled' ? results[3].value.commits : [];
		loaded = true;
	}

	const weekLabel = (w: GitActivityWeek) => (w.week ? timestampDate(w.week).toLocaleDateString(undefined, { month: 'short', day: 'numeric' }) : '');
	const firstLine = (m: string) => m.split('\n')[0] ?? '';
	const shortSha = (s: string) => s.slice(0, 7);
	const num = (n: number) => n.toLocaleString();
</script>

<svelte:head>
	<title>Activity - {namespace}/{name}</title>
</svelte:head>

{#if !loaded}
	<div class="space-y-4">
		<Skeleton class="h-48 w-full rounded-xl" />
		<Skeleton class="h-40 w-full rounded-xl" />
	</div>
{:else}
	<div class="space-y-6">
		<section class="rounded-xl border border-border/60 bg-card overflow-hidden">
			<div class="flex items-center gap-3 px-5 py-3.5 border-b border-border/40 bg-muted/20">
				<Activity class="h-4 w-4 text-primary" />
				<div class="flex-1 min-w-0">
					<h2 class="text-sm font-semibold">Commits per week</h2>
					<p class="text-xs text-muted-foreground">
						{num(totalCommits)} commit{totalCommits === 1 ? '' : 's'} over {weeks.length} week{weeks.length === 1 ? '' : 's'}
						<span class="text-emerald-600 dark:text-emerald-400 ml-2 tabular-nums">+{num(totalAdditions)}</span>
						<span class="text-destructive ml-1 tabular-nums">-{num(totalDeletions)}</span>
					</p>
				</div>
			</div>
			<div class="p-5">
				{#if recentWeeks.length === 0}
					<p class="text-sm text-muted-foreground">No commits yet.</p>
				{:else}
					<div class="flex items-end gap-0.5 h-36" role="img" aria-label="Weekly commit counts">
						{#each recentWeeks as w (w.week ? timestampDate(w.week).getTime() : Math.random())}
							{@const pct = (w.commits / maxCommits) * 100}
							<div
								class="group relative flex-1 min-w-0 h-full flex items-end"
								title="{weekLabel(w)}: {w.commits} commit{w.commits === 1 ? '' : 's'}, +{w.additions} -{w.deletions}"
							>
								<div
									class="w-full rounded-t-[4px] bg-primary/70 group-hover:bg-primary transition-colors"
									style="height: {w.commits === 0 ? 2 : Math.max(4, pct)}%"
								></div>
							</div>
						{/each}
					</div>
					<div class="flex justify-between text-[11px] text-muted-foreground mt-2">
						<span>{weekLabel(recentWeeks[0])}</span>
						<span>{weekLabel(recentWeeks[recentWeeks.length - 1])}</span>
					</div>
				{/if}
			</div>
		</section>

		<div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
			<section class="rounded-xl border border-border/60 bg-card overflow-hidden min-w-0">
				<div class="flex items-center gap-3 px-5 py-3.5 border-b border-border/40 bg-muted/20">
					<GitCommit class="h-4 w-4 text-primary" />
					<h2 class="text-sm font-semibold">Recent commits</h2>
				</div>
				{#if commits.length === 0}
					<p class="text-sm text-muted-foreground p-5">Nothing on {repo?.defaultBranch || 'the default branch'} yet.</p>
				{:else}
					<ul class="divide-y divide-border/40">
						{#each commits as c (c.sha)}
							<li class="flex items-center gap-3 px-5 py-2.5 text-sm">
								<a href={resolve('/[namespace]/[name]/commit/[sha]', { namespace, name, sha: c.sha })} class="font-mono text-xs text-muted-foreground hover:text-primary shrink-0">{shortSha(c.sha)}</a>
								<a href={resolve('/[namespace]/[name]/commit/[sha]', { namespace, name, sha: c.sha })} class="truncate flex-1 hover:text-primary transition-colors">{firstLine(c.message)}</a>
								<span class="text-xs text-muted-foreground shrink-0 hidden sm:inline">
									{c.author?.name}{#if c.author?.at}, {relativeTime(timestampDate(c.author.at))}{/if}
								</span>
							</li>
						{/each}
					</ul>
				{/if}
			</section>

			<section class="rounded-xl border border-border/60 bg-card overflow-hidden self-start">
				<div class="flex items-center gap-3 px-5 py-3.5 border-b border-border/40 bg-muted/20">
					<Code2 class="h-4 w-4 text-primary" />
					<h2 class="text-sm font-semibold">Languages</h2>
				</div>
				<div class="p-5 space-y-3">
					{#if languages.length === 0}
						<p class="text-sm text-muted-foreground">No languages detected.</p>
					{:else}
						<div class="flex h-2.5 w-full gap-0.5 overflow-hidden rounded-full" role="img" aria-label="Language share">
							{#each languages as l (l.name)}
								<div
									class="h-full first:rounded-l-full last:rounded-r-full"
									style="width: {Math.max(l.percent, 0.5)}%; background: {l.color || 'var(--muted-foreground)'}"
									title="{l.name} {l.percent.toFixed(1)}%"
								></div>
							{/each}
						</div>
						<ul class="space-y-1.5">
							{#each languages as l (l.name)}
								<li class="flex items-center gap-2 text-sm">
									<span class="h-2.5 w-2.5 rounded-full shrink-0" style="background: {l.color || 'var(--muted-foreground)'}"></span>
									<span class="flex-1 truncate">{l.name}</span>
									<span class="text-xs text-muted-foreground tabular-nums">{l.percent.toFixed(1)}%</span>
								</li>
							{/each}
						</ul>
					{/if}
				</div>
			</section>
		</div>

		<section class="space-y-3">
			<div class="section-header">
				<div>
					<h2 class="section-title">Contributors</h2>
					<p class="section-subtitle">{contributors.length} author{contributors.length === 1 ? '' : 's'} on {repo?.defaultBranch || 'the default branch'}</p>
				</div>
			</div>
			{#if contributors.length === 0}
				<EmptyState icon={Users} message="No contributors yet" />
			{:else}
				<div class="data-table">
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead class="th">Author</TableHead>
								<TableHead class="th text-right">Commits</TableHead>
								<TableHead class="th text-right hidden sm:table-cell">Additions</TableHead>
								<TableHead class="th text-right hidden sm:table-cell">Deletions</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{#each contributors as c (c.username || c.email)}
								<TableRow>
									<TableCell class="py-2.5 px-3">
										{#if c.username}
											<a href={resolve('/[namespace]', { namespace: c.username })} class="text-sm font-medium hover:text-primary transition-colors">{c.username}</a>
										{:else}
											<span class="text-sm font-medium">{c.displayName || c.email}</span>
										{/if}
										{#if c.displayName && c.username && c.displayName !== c.username}
											<span class="text-xs text-muted-foreground ml-1.5">{c.displayName}</span>
										{/if}
									</TableCell>
									<TableCell class="py-2.5 px-3 text-right text-sm tabular-nums">{num(c.commits)}</TableCell>
									<TableCell class="py-2.5 px-3 text-right text-sm tabular-nums text-emerald-600 dark:text-emerald-400 hidden sm:table-cell">+{num(c.additions)}</TableCell>
									<TableCell class="py-2.5 px-3 text-right text-sm tabular-nums text-destructive hidden sm:table-cell">-{num(c.deletions)}</TableCell>
								</TableRow>
							{/each}
						</TableBody>
					</Table>
				</div>
			{/if}
		</section>
	</div>
{/if}
