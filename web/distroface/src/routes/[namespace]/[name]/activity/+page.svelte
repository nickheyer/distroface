<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Table,
		TableBody,
		TableCell,
		TableHead,
		TableHeader,
		TableRow
	} from '$lib/components/ui/table';
	import EmptyState from '$lib/components/empty-state.svelte';
	import BotBadge from '$lib/components/bot-badge.svelte';
	import { Activity, Users, Code, GitCommit } from '$lib/icons';
	import type {
		GitActivityWeek,
		GitContributor,
		GitLanguage,
		GitCommit as GitCommitType
	} from '$lib/proto/distroface/v1/git_pb';

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
			rpcClient.git.listContributors(
				{ namespace, name, page: { pageSize: 50 } },
				silentCallOptions
			),
			rpcClient.git.getRepositoryLanguages({ namespace, name }, silentCallOptions),
			rpcClient.gitFiles.listCommits(
				{ namespace, name, ref: repo?.defaultBranch ?? '', page: { pageSize: 10 } },
				silentCallOptions
			)
		]);
		weeks = results[0].status === 'fulfilled' ? results[0].value.weeks : [];
		contributors = results[1].status === 'fulfilled' ? results[1].value.contributors : [];
		languages = results[2].status === 'fulfilled' ? results[2].value.languages : [];
		commits = results[3].status === 'fulfilled' ? results[3].value.commits : [];
		loaded = true;
	}

	const weekLabel = (w: GitActivityWeek) =>
		w.week
			? timestampDate(w.week).toLocaleDateString(undefined, { month: 'short', day: 'numeric' })
			: '';
	const firstLine = (m: string) => m.split('\n')[0] ?? '';
	const shortSha = (s: string) => s.slice(0, 7);
	const num = (n: number) => n.toLocaleString();
</script>

<svelte:head>
	<title>Activity - {namespace}/{name}</title>
</svelte:head>

{#if !loaded}
	<div class="space-y-4">
		<Skeleton class="h-48 w-full rounded-lg" />
		<Skeleton class="h-40 w-full rounded-lg" />
	</div>
{:else}
	<div class="space-y-6">
		<section class="box">
			<div class="box-header">
				<span class="flex min-w-0 items-center gap-2">
					<Activity class="size-4 text-muted-foreground" />
					Commits per week
				</span>
				<span class="text-xs font-normal text-muted-foreground">
					{num(totalCommits)} commit{totalCommits === 1 ? '' : 's'} over {weeks.length} week{weeks.length ===
					1
						? ''
						: 's'}
					<span class="ml-2 text-success tabular-nums">+{num(totalAdditions)}</span>
					<span class="ml-1 text-destructive tabular-nums">-{num(totalDeletions)}</span>
				</span>
			</div>
			<div class="p-4">
				{#if recentWeeks.length === 0}
					<p class="text-sm text-muted-foreground">No commits yet.</p>
				{:else}
					<div class="flex h-36 items-end gap-0.5" role="img" aria-label="Weekly commit counts">
						{#each recentWeeks as w (w.week ? timestampDate(w.week).getTime() : Math.random())}
							{@const pct = (w.commits / maxCommits) * 100}
							<div
								class="group relative flex h-full min-w-0 flex-1 items-end"
								title="{weekLabel(w)}: {w.commits} commit{w.commits === 1
									? ''
									: 's'}, +{w.additions} -{w.deletions}"
							>
								<div
									class="w-full rounded-t-sm bg-primary/70 transition-colors group-hover:bg-primary"
									style="height: {w.commits === 0 ? 2 : Math.max(4, pct)}%"
								></div>
							</div>
						{/each}
					</div>
					<div class="mt-2 flex justify-between text-2xs text-muted-foreground">
						<span>{weekLabel(recentWeeks[0])}</span>
						<span>{weekLabel(recentWeeks[recentWeeks.length - 1])}</span>
					</div>
				{/if}
			</div>
		</section>

		<div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_20rem]">
			<section class="box min-w-0">
				<div class="box-header">
					<span class="flex items-center gap-2">
						<GitCommit class="size-4 text-muted-foreground" />
						Recent commits
					</span>
				</div>
				{#if commits.length === 0}
					<p class="box-empty">Nothing on {repo?.defaultBranch || 'the default branch'} yet</p>
				{:else}
					<ul class="divide-y">
						{#each commits as c (c.sha)}
							<li class="flex items-center gap-3 px-4 py-2.5 text-sm">
								<a
									href={resolve('/[namespace]/[name]/commit/[sha]', {
										namespace,
										name,
										sha: c.sha
									})}
									class="shrink-0 font-mono text-xs text-muted-foreground hover:text-primary"
									>{shortSha(c.sha)}</a
								>
								<a
									href={resolve('/[namespace]/[name]/commit/[sha]', {
										namespace,
										name,
										sha: c.sha
									})}
									class="flex-1 truncate transition-colors hover:text-primary"
									>{firstLine(c.message)}</a
								>
								<span class="hidden shrink-0 text-xs text-muted-foreground sm:inline">
									{c.author?.name}{#if c.author?.at}, {relativeTime(
											timestampDate(c.author.at)
										)}{/if}
								</span>
							</li>
						{/each}
					</ul>
				{/if}
			</section>

			<section class="box self-start">
				<div class="box-header">
					<span class="flex items-center gap-2">
						<Code class="size-4 text-muted-foreground" />
						Languages
					</span>
				</div>
				<div class="space-y-3 p-4">
					{#if languages.length === 0}
						<p class="text-sm text-muted-foreground">No languages detected.</p>
					{:else}
						<div
							class="flex h-2.5 w-full gap-0.5 overflow-hidden rounded-full"
							role="img"
							aria-label="Language share"
						>
							{#each languages as l (l.name)}
								<div
									class="h-full first:rounded-l-full last:rounded-r-full"
									style="width: {Math.max(l.percent, 0.5)}%; background: {l.color ||
										'var(--muted-foreground)'}"
									title="{l.name} {l.percent.toFixed(1)}%"
								></div>
							{/each}
						</div>
						<ul class="space-y-1.5">
							{#each languages as l (l.name)}
								<li class="flex items-center gap-2 text-sm">
									<span
										class="size-2.5 shrink-0 rounded-full"
										style="background: {l.color || 'var(--muted-foreground)'}"
									></span>
									<span class="flex-1 truncate">{l.name}</span>
									<span class="text-xs text-muted-foreground tabular-nums"
										>{l.percent.toFixed(1)}%</span
									>
								</li>
							{/each}
						</ul>
					{/if}
				</div>
			</section>
		</div>

		<section class="space-y-4">
			<div class="section-header">
				<div>
					<h2 class="section-title">Contributors</h2>
					<p class="section-subtitle">
						{contributors.length} author{contributors.length === 1 ? '' : 's'} on {repo?.defaultBranch ||
							'the default branch'}
					</p>
				</div>
			</div>
			{#if contributors.length === 0}
				<EmptyState icon={Users} message="No contributors yet" />
			{:else}
				<div class="data-table">
					<Table>
						<TableHeader>
							<TableRow>
								<TableHead>Author</TableHead>
								<TableHead class="text-right">Commits</TableHead>
								<TableHead class="hidden text-right sm:table-cell">Additions</TableHead>
								<TableHead class="hidden text-right sm:table-cell">Deletions</TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{#each contributors as c (c.username || c.email)}
								<TableRow>
									<TableCell>
										{#if c.username}
											<a
												href={resolve('/[namespace]', { namespace: c.username })}
												class="text-sm font-medium transition-colors hover:text-primary"
												>{c.username}</a
											>
											{#if c.isAgent}<BotBadge class="ml-1.5 align-middle" />{/if}
										{:else}
											<span class="text-sm font-medium">{c.displayName || c.email}</span>
										{/if}
										{#if c.displayName && c.username && c.displayName !== c.username}
											<span class="ml-1.5 text-xs text-muted-foreground">{c.displayName}</span>
										{/if}
									</TableCell>
									<TableCell class="text-right text-sm tabular-nums">{num(c.commits)}</TableCell>
									<TableCell
										class="hidden text-right text-sm text-success tabular-nums sm:table-cell"
										>+{num(c.additions)}</TableCell
									>
									<TableCell
										class="hidden text-right text-sm text-destructive tabular-nums sm:table-cell"
										>-{num(c.deletions)}</TableCell
									>
								</TableRow>
							{/each}
						</TableBody>
					</Table>
				</div>
			{/if}
		</section>
	</div>
{/if}
