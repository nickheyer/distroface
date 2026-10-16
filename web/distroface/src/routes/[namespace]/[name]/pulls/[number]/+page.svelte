<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { GitPullRequest, GitMerge, GitPullRequestClosed, GitPullRequestDraft, Pencil, Check, X, Loader2, MessageSquare, GitCommit, FileDiff, Terminal } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import PullConversation from './pull-conversation.svelte';
	import PullCommits from './pull-commits.svelte';
	import PullFiles from './pull-files.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import { ReviewState } from '$lib/proto/distroface/v1/pull_pb';
	import type { PullRequest, Review, ReviewComment } from '$lib/proto/distroface/v1/pull_pb';
	import { pullStatus, pullStatusLabel, pullStatusClass, headLabel } from '$lib/pulls';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const repoFullName = $derived(`${namespace}/${name}`);
	const numberParam = $derived(page.params.number ?? '0');
	const number = $derived(BigInt(/^\d+$/.test(numberParam) ? numberParam : '0'));

	type Tab = 'conversation' | 'commits' | 'files';
	const tab = $derived<Tab>(((): Tab => {
		const t = page.url.searchParams.get('tab');
		return t === 'commits' || t === 'files' ? t : 'conversation';
	})());

	let pull = $state<PullRequest | undefined>();
	let reviews = $state<Review[]>([]);
	let reviewComments = $state<ReviewComment[]>([]);
	let loading = $state(true);
	let missing = $state(false);
	let seq = 0;

	async function load(silent = false) {
		const mine = ++seq;
		if (!silent) loading = true;
		missing = false;
		try {
			const [p, r, c] = await Promise.all([
				rpcClient.pull.getPullRequest({ namespace, name, number }, silentCallOptions),
				rpcClient.pull.listReviews({ namespace, name, number, page: { pageSize: 200, pageToken: '', orderBy: '' } }, silentCallOptions).catch(() => undefined),
				rpcClient.pull.listReviewComments({ namespace, name, number, path: '' }, silentCallOptions).catch(() => undefined)
			]);
			if (mine !== seq) return;
			pull = p.pullRequest;
			missing = !p.pullRequest?.issue;
			reviews = r?.reviews ?? [];
			reviewComments = c?.comments ?? [];
		} catch {
			if (mine !== seq) return;
			pull = undefined;
			missing = true;
		} finally {
			if (mine === seq) loading = false;
		}
	}

	$effect(() => {
		void number;
		void repo.id;
		load();
	});

	const reload = () => load(true);

	const issue = $derived(pull?.issue);
	const status = $derived(pull ? pullStatus(pull) : 'open');
	const me = $derived(authStore.user?.id ?? '');
	const isAuthor = $derived(!!me && issue?.author?.id === me);
	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.PULLS) && !repo.isArchived);
	const canEdit = $derived(canWrite || (isAuthor && !repo.isArchived));
	const canComment = $derived(authStore.isAuthenticated && !repo.isArchived && (!issue?.locked || canWrite));
	const orgId = $derived(repo.isOrgNamespace ? repo.ownerId : '');
	const pendingReview = $derived(reviews.find((r) => r.state === ReviewState.PENDING && r.reviewer?.id === me));
	const pendingCount = $derived(reviewComments.filter((c) => c.pending).length);

	let editingTitle = $state(false);
	let titleDraft = $state('');
	let savingTitle = $state(false);

	function startTitle() {
		titleDraft = issue?.title ?? '';
		editingTitle = true;
	}

	async function saveTitle() {
		if (!titleDraft.trim() || savingTitle) return;
		savingTitle = true;
		try {
			await rpcClient.pull.updatePullRequest({ namespace, name, number, title: titleDraft.trim() });
			editingTitle = false;
			await reload();
		} catch {
			// Error interceptor already toasted
		} finally {
			savingTitle = false;
		}
	}

	function statusIcon(s: typeof status) {
		switch (s) {
			case 'merged':
				return GitMerge;
			case 'closed':
				return GitPullRequestClosed;
			case 'draft':
				return GitPullRequestDraft;
			default:
				return GitPullRequest;
		}
	}

	const opened = $derived(issue?.createdAt ? relativeTime(timestampDate(issue.createdAt)) : '');
	const pullParams = $derived({ namespace, name, number: numberParam });
	const fetchHint = $derived(pull?.headRef ? `git fetch origin ${pull.headRef}:pr-${numberParam}` : '');
</script>

<svelte:head>
	<title>{issue ? `${issue.title} #${numberParam}` : `Pull request #${numberParam}`} - {repoFullName}</title>
</svelte:head>

{#if loading && !pull}
	<div class="space-y-4">
		<Skeleton class="h-7 w-2/3" />
		<Skeleton class="h-4 w-1/2" />
		<div class="flex gap-4 border-b border-border/60 pb-2 pt-2">
			{#each { length: 3 }, i (i)}
				<Skeleton class="h-5 w-24" />
			{/each}
		</div>
		<Skeleton class="h-64 w-full rounded-xl" />
	</div>
{:else if missing || !pull || !issue}
	<EmptyState icon={GitPullRequest} message="Pull request not found" description="#{numberParam} does not exist in {repoFullName} or you cannot see it.">
		{#snippet actions()}
			<Button variant="outline" href="/{namespace}/{name}/pulls">Back to pull requests</Button>
		{/snippet}
	</EmptyState>
{:else}
	{@const StatusIcon = statusIcon(status)}
	<div class="space-y-4">
		<header class="space-y-2">
			{#if editingTitle}
				<div class="flex flex-col sm:flex-row sm:items-center gap-2">
					<Input bind:value={titleDraft} class="flex-1" aria-label="Pull request title" onkeydown={(e) => { if (e.key === 'Enter') saveTitle(); if (e.key === 'Escape') editingTitle = false; }} autofocus />
					<div class="flex items-center gap-2 justify-end">
						<Button variant="outline" size="sm" class="h-8" onclick={() => (editingTitle = false)} disabled={savingTitle}><X class="h-3.5 w-3.5 mr-1" />Cancel</Button>
						<Button size="sm" class="h-8" onclick={saveTitle} disabled={savingTitle || !titleDraft.trim()}>
							{#if savingTitle}<Loader2 class="h-3.5 w-3.5 animate-spin mr-1" />{:else}<Check class="h-3.5 w-3.5 mr-1" />{/if}
							Save
						</Button>
					</div>
				</div>
			{:else}
				<div class="flex items-start gap-2">
					<h1 class="text-xl sm:text-2xl font-semibold tracking-tight leading-snug min-w-0 flex-1 break-words">
						{issue.title}
						<span class="text-muted-foreground font-normal">#{numberParam}</span>
					</h1>
					{#if canEdit}
						<Button variant="ghost" size="icon" class="h-7 w-7 shrink-0" onclick={startTitle} title="Edit title">
							<Pencil class="h-3.5 w-3.5 text-muted-foreground" />
						</Button>
					{/if}
				</div>
			{/if}
			<div class="flex flex-wrap items-center gap-x-2 gap-y-1 text-[13px] text-muted-foreground">
				<span class="inline-flex items-center gap-1.5 rounded-full border px-2.5 py-0.5 text-[12px] font-medium {pullStatusClass(status)}">
					<StatusIcon class="h-3.5 w-3.5" />
					{pullStatusLabel(status)}
				</span>
				<span>
					<a href={resolve('/[namespace]', { namespace: issue.author?.username ?? '' })} class="font-medium text-foreground hover:underline">{issue.author?.username ?? 'unknown'}</a>
					{status === 'merged' ? 'merged' : 'wants to merge'}
					<a href="{resolve('/[namespace]/[name]/pulls/[number]', pullParams)}?tab=commits" class="font-medium text-foreground hover:underline">{pull.commitCount} commit{pull.commitCount !== 1 ? 's' : ''}</a>
					into
				</span>
				<span class="inline-flex items-center gap-0.5">
					<a href={resolve('/[namespace]/[name]/tree/[ref]', { namespace, name, ref: encodeURIComponent(pull.baseBranch) })} class="font-mono text-[12px] rounded bg-primary/10 text-primary px-1.5 py-0.5 hover:underline">{pull.baseBranch}</a>
					<CopyButton text={pull.baseBranch} label="Base branch copied" />
				</span>
				<span>from</span>
				<span class="inline-flex items-center gap-0.5">
					{#if pull.headDeleted}
						<span class="font-mono text-[12px] rounded bg-muted px-1.5 py-0.5 line-through">{headLabel(pull, repoFullName)}</span>
					{:else}
						{@const [headNs, headName] = (pull.headRepoFullName || repoFullName).split('/')}
						<a href={resolve('/[namespace]/[name]/tree/[ref]', { namespace: headNs, name: headName, ref: encodeURIComponent(pull.headBranch) })} class="font-mono text-[12px] rounded bg-primary/10 text-primary px-1.5 py-0.5 hover:underline">{headLabel(pull, repoFullName)}</a>
					{/if}
					<CopyButton text={headLabel(pull, repoFullName)} label="Head branch copied" />
				</span>
				<span class="text-muted-foreground/50">|</span>
				<span>opened {opened}</span>
				{#if fetchHint}
					<span class="inline-flex items-center gap-0.5 sm:ml-auto" title={fetchHint}>
						<Terminal class="h-3.5 w-3.5" />
						<span class="font-mono text-[11.5px] hidden md:inline">{fetchHint}</span>
						<CopyButton text={fetchHint} label="Fetch command copied" />
					</span>
				{/if}
			</div>
		</header>

		<nav class="flex items-center gap-1 border-b border-border/60 text-[13px] overflow-x-auto">
			<a href={resolve('/[namespace]/[name]/pulls/[number]', pullParams)} class="inline-flex items-center gap-1.5 px-3 py-2 border-b-2 -mb-px whitespace-nowrap {tab === 'conversation' ? 'border-primary text-foreground font-medium' : 'border-transparent text-muted-foreground hover:text-foreground'}">
				<MessageSquare class="h-3.5 w-3.5" />Conversation
				<span class="tabular-nums text-muted-foreground text-[12px]">{issue.commentCount}</span>
			</a>
			<a href="{resolve('/[namespace]/[name]/pulls/[number]', pullParams)}?tab=commits" class="inline-flex items-center gap-1.5 px-3 py-2 border-b-2 -mb-px whitespace-nowrap {tab === 'commits' ? 'border-primary text-foreground font-medium' : 'border-transparent text-muted-foreground hover:text-foreground'}">
				<GitCommit class="h-3.5 w-3.5" />Commits
				<span class="tabular-nums text-muted-foreground text-[12px]">{pull.commitCount}</span>
			</a>
			<a href="{resolve('/[namespace]/[name]/pulls/[number]', pullParams)}?tab=files" class="inline-flex items-center gap-1.5 px-3 py-2 border-b-2 -mb-px whitespace-nowrap {tab === 'files' ? 'border-primary text-foreground font-medium' : 'border-transparent text-muted-foreground hover:text-foreground'}">
				<FileDiff class="h-3.5 w-3.5" />Files changed
				<span class="tabular-nums text-muted-foreground text-[12px]">{pull.changedFiles}</span>
			</a>
			<span class="ml-auto pl-3 text-[12px] tabular-nums whitespace-nowrap">
				<span class="text-emerald-600 dark:text-emerald-400">+{pull.additions}</span>
				<span class="text-red-600 dark:text-red-400 ml-1">-{pull.deletions}</span>
			</span>
		</nav>

		{#if tab === 'commits'}
			<PullCommits {namespace} {name} {number} headSha={pull.headSha} />
		{:else if tab === 'files'}
			<PullFiles
				{namespace}
				{name}
				{pull}
				{reviewComments}
				{pendingReview}
				{pendingCount}
				{canComment}
				canModerate={canWrite}
				canApply={canEdit && status !== 'merged' && status !== 'closed'}
				{isAuthor}
				onChanged={reload}
			/>
		{:else}
			<PullConversation
				{namespace}
				{name}
				{pull}
				{reviews}
				{reviewComments}
				{pendingReview}
				{pendingCount}
				{canComment}
				{canEdit}
				{canWrite}
				{isAuthor}
				{orgId}
				onChanged={reload}
			/>
		{/if}
	</div>
{/if}
