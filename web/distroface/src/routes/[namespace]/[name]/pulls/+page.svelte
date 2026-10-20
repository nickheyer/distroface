<script lang="ts">
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import {
		GitPullRequest,
		GitPullRequestClosed,
		GitMerge,
		MessageSquare,
		CheckCircle2,
		XCircle,
		Clock,
		Plus,
		Lock,
		GitPullRequestDraft
	} from '@lucide/svelte';
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
	import { relativeTime, cn } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const repoFullName = $derived(`${namespace}/${name}`);
	const canOpen = $derived(
		authStore.isAuthenticated && !repo.isArchived && gitRepoStore.canRead(GitUnit.PULLS)
	);

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
	const rows = $derived(
		tab === 'merged'
			? pulls.filter((p) => !!p.mergedAt)
			: tab === 'closed'
				? pulls.filter((p) => !p.mergedAt)
				: pulls
	);

	function reviewIcon(p: PullRequest) {
		const latest = latestReviews(p.reviews).filter((r) => !r.dismissed);
		if (latest.some((r) => r.state === ReviewState.CHANGES_REQUESTED))
			return { icon: XCircle, color: 'text-destructive', title: 'Changes requested' };
		if (latest.some((r) => r.state === ReviewState.APPROVED))
			return { icon: CheckCircle2, color: 'text-success', title: 'Approved' };
		if (p.reviewRequests.some((r) => !r.fulfilled))
			return { icon: Clock, color: 'text-warning', title: 'Review requested' };
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
				return { icon: Clock, color: 'text-warning', title: 'Checks pending' };
			default:
				return null;
		}
	}

	function stateIcon(p: PullRequest) {
		switch (pullStatus(p)) {
			case 'merged':
				return { icon: GitMerge, color: 'text-done' };
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

{#snippet tabButton(value: Tab, label: string, count?: number)}
	<button
		type="button"
		class={cn(
			'inline-flex items-center gap-1.5 rounded-md px-2 py-1 text-sm',
			tab === value
				? 'font-semibold text-foreground'
				: 'font-normal text-muted-foreground hover:text-foreground'
		)}
		onclick={() => switchTab(value)}
	>
		{#if value === 'open'}<GitPullRequest class="size-3.5" />{:else if value === 'merged'}<GitMerge
				class="size-3.5"
			/>{:else}<GitPullRequestClosed class="size-3.5" />{/if}
		{label}
		{#if count !== undefined}
			<span class="text-muted-foreground tabular-nums">{count}</span>
		{/if}
	</button>
{/snippet}

{#if !gitRepoStore.unitEnabled(GitUnit.PULLS)}
	<EmptyState
		icon={GitPullRequest}
		message="Pull requests are disabled"
		description="Enable the pull requests unit in the repository settings."
	/>
{:else}
	<div class="space-y-4">
		<div class="flex flex-wrap items-center gap-2">
			<div class="w-full sm:w-auto sm:min-w-64 sm:flex-1">
				<QueryFilterBar {filter} placeholder="Search pull requests" onchange={changed} />
			</div>
			<Select
				type="single"
				value={sort}
				onValueChange={(v) => {
					if (v) {
						sort = v;
						changed();
					}
				}}
			>
				<SelectTrigger class="w-44" aria-label="Sort">
					{sortOptions.find((o) => o.value === sort)?.label ?? 'Sort'}
				</SelectTrigger>
				<SelectContent>
					{#each sortOptions as o (o.value)}
						<SelectItem value={o.value}>{o.label}</SelectItem>
					{/each}
				</SelectContent>
			</Select>
			<PermissionGate allowed={canOpen}>
				<Button href="/{namespace}/{name}/pulls/new">
					<Plus />New pull request
				</Button>
			</PermissionGate>
		</div>

		<div class="box">
			<div class="box-header flex-wrap">
				<div class="flex flex-wrap items-center gap-1">
					{@render tabButton('open', 'Open', openCount)}
					{@render tabButton('merged', 'Merged')}
					{@render tabButton('closed', 'Closed', closedCount)}
				</div>
			</div>

			{#if loading}
				<div class="divide-y">
					{#each { length: 6 }, i (i)}
						<div class="flex items-center gap-3 px-4 py-3">
							<Skeleton class="size-4 rounded-full" />
							<div class="flex-1 space-y-2">
								<Skeleton class="h-4 w-2/3" />
								<Skeleton class="h-3 w-1/3" />
							</div>
						</div>
					{/each}
				</div>
			{:else if rows.length === 0}
				<div class="box-empty">
					<p class="font-semibold text-foreground">
						{filter.active
							? 'No pull requests match these filters'
							: tab === 'open'
								? 'No open pull requests'
								: tab === 'merged'
									? 'No merged pull requests on this page'
									: 'No closed pull requests'}
					</p>
					{#if filter.active}
						<p class="mt-1">Try clearing a filter.</p>
					{:else if tab === 'open' && canOpen}
						<p class="mt-1">Compare two branches to propose a change.</p>
						<div class="mt-4 flex justify-center gap-2">
							<Button href="/{namespace}/{name}/pulls/new"><Plus />New pull request</Button>
						</div>
					{/if}
				</div>
			{:else}
				<ul class="divide-y">
					{#each rows as p (p.issue?.id ?? p.headSha)}
						{@const st = stateIcon(p)}
						{@const StIcon = st.icon}
						{@const rv = reviewIcon(p)}
						{@const ck = checksIcon(p)}
						{@const n = Number(p.issue?.number ?? 0n)}
						<li class="flex items-start gap-3 px-4 py-3 transition-colors hover:bg-muted/30">
							<StIcon class="mt-0.5 size-4 shrink-0 {st.color}" />
							<div class="min-w-0 flex-1">
								<div class="flex flex-wrap items-center gap-2">
									<a
										href={resolve('/[namespace]/[name]/pulls/[number]', {
											namespace,
											name,
											number: String(n)
										})}
										class="min-w-0 text-sm leading-snug font-semibold wrap-break-word hover:text-primary"
										>{p.issue?.title}</a
									>
									{#if p.draft && !p.mergedAt && p.issue?.state === IssueState.OPEN}
										<Badge variant="outline">Draft</Badge>
									{/if}
									{#if p.issue?.locked}<Lock
											class="size-3 text-muted-foreground"
											aria-label="Locked"
										/>{/if}
									{#each p.issue?.labels ?? [] as l (l.id)}
										<LabelChip
											name={l.name}
											color={l.color}
											description={l.description}
											size="sm"
										/>
									{/each}
								</div>
								<p
									class="mt-0.5 flex flex-wrap items-center gap-x-1.5 gap-y-0.5 text-xs text-muted-foreground"
								>
									<span>#{n}</span>
									<span>opened {opened(p)} by</span>
									<a
										href={resolve('/[namespace]', { namespace: p.issue?.author?.username ?? '' })}
										class="hover:text-primary">{p.issue?.author?.username ?? 'unknown'}</a
									>
									<span class="text-muted-foreground/50">|</span>
									<span class="font-mono">{headLabel(p, repoFullName)}</span>
									<span>into</span>
									<span class="font-mono">{p.baseBranch}</span>
									{#if p.issue?.milestone}
										<span class="text-muted-foreground/50">|</span>
										<span>{p.issue.milestone.title}</span>
									{/if}
									{#if p.issue?.assignees?.length}
										<span class="sm:hidden"
											>{p.issue.assignees.map((a) => a.username).join(', ')}</span
										>
									{/if}
									{#if (p.issue?.commentCount ?? 0) > 0}
										<span class="inline-flex items-center gap-1 tabular-nums sm:hidden"
											><MessageSquare class="size-3" />{p.issue?.commentCount}</span
										>
									{/if}
								</p>
							</div>
							<div
								class="mt-0.5 hidden shrink-0 items-center gap-2.5 text-xs text-muted-foreground sm:flex"
							>
								{#if rv}
									{@const RvIcon = rv.icon}
									<RvIcon class="size-4 {rv.color}" aria-label={rv.title} />
								{/if}
								{#if ck}
									{@const CkIcon = ck.icon}
									<CkIcon class="size-4 {ck.color}" aria-label={ck.title} />
								{/if}
								{#if p.issue?.assignees?.length}
									<span class="max-w-32 truncate" title="Assignees"
										>{p.issue.assignees.map((a) => a.username).join(', ')}</span
									>
								{/if}
								{#if (p.issue?.commentCount ?? 0) > 0}
									<span class="inline-flex items-center gap-1 tabular-nums"
										><MessageSquare class="size-3.5" />{p.issue?.commentCount}</span
									>
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
