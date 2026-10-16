<script lang="ts">
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { GitPullRequest, GitPullRequestClosed, GitMerge, MessageSquare, CheckCircle2, XCircle, Clock, Plus, GitPullRequestDraft } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import LabelChip from '$lib/components/git/label-chip.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { GitUnit, CommitStatusState } from '$lib/proto/distroface/v1/git_pb';
	import { IssueState } from '$lib/proto/distroface/v1/issue_pb';
	import { ReviewState } from '$lib/proto/distroface/v1/pull_pb';
	import type { PullRequest } from '$lib/proto/distroface/v1/pull_pb';
	import { pullStatus, headLabel, latestReviews } from '$lib/pulls';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const repoFullName = $derived(`${namespace}/${name}`);
	const canOpen = $derived(authStore.isAuthenticated && !repo.isArchived && gitRepoStore.canRead(GitUnit.PULLS));

	type Tab = 'open' | 'closed' | 'merged';
	let tab = $state<Tab>((page.url.searchParams.get('state') as Tab) || 'open');

	const filter = new QueryFilter([
		{ key: 'label', label: 'Label' },
		{ key: 'milestone', label: 'Milestone' },
		{ key: 'author', label: 'Author' },
		{ key: 'assignee', label: 'Assignee' },
		{ key: 'reviewer', label: 'Reviewer' },
		{ key: 'base', label: 'Base' },
		{ key: 'head', label: 'Head' }
	]);
	const pager = new Pager(25);
	let sort = $state('created_at desc');
	const sortOptions = [
		{ value: 'created_at desc', label: 'Newest' },
		{ value: 'created_at asc', label: 'Oldest' },
		{ value: 'updated_at desc', label: 'Recently updated' },
		{ value: 'comments desc', label: 'Most commented' }
	];

	let pulls = $state<PullRequest[]>([]);
	let openCount = $state(0);
	let closedCount = $state(0);
	let loading = $state(true);
	let seq = 0;

	function firstFilter(key: string): string {
		return filter.filters.find((f) => f.field === key)?.value ?? '';
	}

	async function load() {
		const mine = ++seq;
		loading = true;
		const q = filter.request();
		try {
			const resp = await rpcClient.pull.listPullRequests(
				{
					namespace,
					name,
					state: tab === 'open' ? IssueState.OPEN : IssueState.CLOSED,
					labels: filter.filters.filter((f) => f.field === 'label').map((f) => f.value),
					milestone: firstFilter('milestone'),
					assignee: firstFilter('assignee'),
					author: firstFilter('author'),
					reviewer: firstFilter('reviewer'),
					base: firstFilter('base'),
					head: firstFilter('head'),
					page: pager.request({ text: q.text, filters: [] }, sort)
				},
				silentCallOptions
			);
			if (mine !== seq) return;
			pulls = resp.pullRequests;
			pager.apply(resp.page);
			openCount = Number(resp.openCount);
			closedCount = Number(resp.closedCount);
		} catch {
			if (mine !== seq) return;
			pulls = [];
			pager.apply();
		} finally {
			if (mine === seq) loading = false;
		}
	}

	function changed() {
		pager.reset();
		load();
	}

	function switchTab(next: Tab) {
		if (tab === next) return;
		tab = next;
		changed();
	}

	onMount(load);

	// Merged rows hide inside the closed state server side
	const rows = $derived(tab === 'merged' ? pulls.filter((p) => !!p.mergedAt) : tab === 'closed' ? pulls.filter((p) => !p.mergedAt) : pulls);

	function reviewIcon(p: PullRequest) {
		const latest = latestReviews(p.reviews).filter((r) => !r.dismissed);
		if (latest.some((r) => r.state === ReviewState.CHANGES_REQUESTED)) return { icon: XCircle, color: 'text-destructive', title: 'Changes requested' };
		if (latest.some((r) => r.state === ReviewState.APPROVED)) return { icon: CheckCircle2, color: 'text-success', title: 'Approved' };
		if (p.reviewRequests.some((r) => !r.fulfilled)) return { icon: Clock, color: 'text-amber-500', title: 'Review requested' };
		return null;
	}

	function checksIcon(p: PullRequest) {
		switch (p.status) {
			case CommitStatusState.SUCCESS:
				return { icon: CheckCircle2, color: 'text-success', title: 'Checks passed' };
			case CommitStatusState.FAILURE:
			case CommitStatusState.ERROR:
				return { icon: XCircle, color: 'text-destructive', title: 'Checks failed' };
			case CommitStatusState.PENDING:
				return { icon: Clock, color: 'text-amber-500', title: 'Checks pending' };
			default:
				return null;
		}
	}

	function stateIcon(p: PullRequest) {
		switch (pullStatus(p)) {
			case 'merged':
				return { icon: GitMerge, color: 'text-purple-600 dark:text-purple-400' };
			case 'closed':
				return { icon: GitPullRequestClosed, color: 'text-destructive' };
			case 'draft':
				return { icon: GitPullRequestDraft, color: 'text-muted-foreground' };
			default:
				return { icon: GitPullRequest, color: 'text-success' };
		}
	}

	function opened(p: PullRequest): string {
		const ts = p.issue?.createdAt;
		return ts ? relativeTime(timestampDate(ts)) : '';
	}
</script>

<svelte:head>
	<title>Pull requests - {repoFullName}</title>
</svelte:head>

{#if !gitRepoStore.unitEnabled(GitUnit.PULLS)}
	<EmptyState icon={GitPullRequest} message="Pull requests are disabled" description="Enable the pull requests unit in the repository settings." />
{:else}
	<div class="space-y-4">
		<div class="flex flex-wrap items-center gap-2">
			<div class="w-full sm:w-auto sm:flex-1 sm:min-w-64">
				<QueryFilterBar {filter} placeholder="Search pull requests..." onchange={changed} />
			</div>
			<Select type="single" value={sort} onValueChange={(v) => { if (v) { sort = v; changed(); } }}>
				<SelectTrigger class="h-8 w-44 text-[13px]" size="sm" aria-label="Sort">
					{sortOptions.find((o) => o.value === sort)?.label ?? 'Sort'}
				</SelectTrigger>
				<SelectContent>
					{#each sortOptions as o (o.value)}
						<SelectItem value={o.value}>{o.label}</SelectItem>
					{/each}
				</SelectContent>
			</Select>
			<PermissionGate allowed={canOpen}>
				<Button size="sm" class="h-8" href="/{namespace}/{name}/pulls/new">
					<Plus class="h-3.5 w-3.5 mr-1.5" />New Pull Request
				</Button>
			</PermissionGate>
		</div>

		<div class="rounded-xl border border-border/60 bg-card overflow-hidden">
			<div class="flex flex-wrap items-center gap-1 px-3 py-2 bg-muted/20 border-b border-border/50 text-[13px]">
				<button type="button" class="inline-flex items-center gap-1.5 px-2 py-1 rounded-md {tab === 'open' ? 'bg-background font-medium shadow-xs' : 'text-muted-foreground hover:text-foreground'}" onclick={() => switchTab('open')}>
					<GitPullRequest class="h-3.5 w-3.5" />Open
					<span class="tabular-nums text-muted-foreground">{openCount}</span>
				</button>
				<button type="button" class="inline-flex items-center gap-1.5 px-2 py-1 rounded-md {tab === 'merged' ? 'bg-background font-medium shadow-xs' : 'text-muted-foreground hover:text-foreground'}" onclick={() => switchTab('merged')}>
					<GitMerge class="h-3.5 w-3.5" />Merged
				</button>
				<button type="button" class="inline-flex items-center gap-1.5 px-2 py-1 rounded-md {tab === 'closed' ? 'bg-background font-medium shadow-xs' : 'text-muted-foreground hover:text-foreground'}" onclick={() => switchTab('closed')}>
					<GitPullRequestClosed class="h-3.5 w-3.5" />Closed
					<span class="tabular-nums text-muted-foreground">{closedCount}</span>
				</button>
			</div>

			{#if loading}
				<div class="divide-y divide-border/50">
					{#each { length: 6 }, i (i)}
						<div class="px-4 py-3 flex items-center gap-3">
							<Skeleton class="h-4 w-4 rounded-full" />
							<div class="flex-1 space-y-1.5">
								<Skeleton class="h-4 w-2/3" />
								<Skeleton class="h-3 w-1/3" />
							</div>
						</div>
					{/each}
				</div>
			{:else if rows.length === 0}
				<div class="p-3">
					<EmptyState icon={GitPullRequest} message={filter.active ? 'No pull requests match these filters' : tab === 'open' ? 'No open pull requests' : tab === 'merged' ? 'No merged pull requests on this page' : 'No closed pull requests'} description={filter.active ? 'Try clearing a filter.' : tab === 'open' && canOpen ? 'Compare two branches to propose a change.' : ''}>
						{#snippet actions()}
							{#if tab === 'open' && !filter.active && canOpen}
								<Button size="sm" href="/{namespace}/{name}/pulls/new"><Plus class="h-3.5 w-3.5 mr-1.5" />New Pull Request</Button>
							{/if}
						{/snippet}
					</EmptyState>
				</div>
			{:else}
				<ul class="divide-y divide-border/50">
					{#each rows as p (p.issue?.id ?? p.headSha)}
						{@const st = stateIcon(p)}
						{@const StIcon = st.icon}
						{@const rv = reviewIcon(p)}
						{@const ck = checksIcon(p)}
						{@const n = Number(p.issue?.number ?? 0n)}
						<li class="px-4 py-2.5 flex items-start gap-3 hover:bg-muted/20 transition-colors">
							<StIcon class="h-4 w-4 mt-0.5 shrink-0 {st.color}" />
							<div class="min-w-0 flex-1">
								<div class="flex flex-wrap items-center gap-2">
									<a href={resolve('/[namespace]/[name]/pulls/[number]', { namespace, name, number: String(n) })} class="text-[14px] font-medium hover:text-primary leading-tight min-w-0 break-words">{p.issue?.title}</a>
									{#if p.draft && !p.mergedAt && p.issue?.state === IssueState.OPEN}
										<Badge variant="outline" class="text-[10px] h-4.5 px-1.5 text-muted-foreground">Draft</Badge>
									{/if}
									{#each p.issue?.labels ?? [] as l (l.id)}
										<LabelChip name={l.name} color={l.color} description={l.description} size="sm" />
									{/each}
								</div>
								<p class="text-[12px] text-muted-foreground mt-1 flex flex-wrap items-center gap-x-1.5 gap-y-0.5">
									<span>#{n}</span>
									<span>opened {opened(p)} by</span>
									<a href={resolve('/[namespace]', { namespace: p.issue?.author?.username ?? '' })} class="hover:underline">{p.issue?.author?.username ?? 'unknown'}</a>
									<span class="text-muted-foreground/50">|</span>
									<span class="font-mono">{headLabel(p, repoFullName)}</span>
									<span class="text-muted-foreground/60">into</span>
									<span class="font-mono">{p.baseBranch}</span>
									{#if p.issue?.milestone}
										<span class="text-muted-foreground/50">|</span>
										<span>{p.issue.milestone.title}</span>
									{/if}
									{#if p.issue?.assignees?.length}
										<span class="sm:hidden">{p.issue.assignees.map((a) => a.username).join(', ')}</span>
									{/if}
									{#if (p.issue?.commentCount ?? 0) > 0}
										<span class="inline-flex items-center gap-1 tabular-nums sm:hidden"><MessageSquare class="h-3 w-3" />{p.issue?.commentCount}</span>
									{/if}
								</p>
							</div>
							<div class="hidden sm:flex items-center gap-2.5 shrink-0 text-[12px] text-muted-foreground mt-0.5">
								{#if rv}
									{@const RvIcon = rv.icon}
									<RvIcon class="h-4 w-4 {rv.color}" aria-label={rv.title} />
								{/if}
								{#if ck}
									{@const CkIcon = ck.icon}
									<CkIcon class="h-4 w-4 {ck.color}" aria-label={ck.title} />
								{/if}
								{#if p.issue?.assignees?.length}
									<span class="truncate max-w-[8rem]" title="Assignees">{p.issue.assignees.map((a) => a.username).join(', ')}</span>
								{/if}
								{#if (p.issue?.commentCount ?? 0) > 0}
									<span class="inline-flex items-center gap-1 tabular-nums"><MessageSquare class="h-3.5 w-3.5" />{p.issue?.commentCount}</span>
								{/if}
							</div>
						</li>
					{/each}
				</ul>
			{/if}
			<DataPagination {pager} onChange={load} pageSizeOptions={[25, 50, 100]} attached />
		</div>
	</div>
{/if}
