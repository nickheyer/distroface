<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import {
		GitPullRequest,
		GitMerge,
		GitPullRequestClosed,
		GitPullRequestDraft,
		Pencil,
		Check,
		X,
		Loader2,
		MessageSquare,
		GitCommit,
		FileDiff,
		Terminal
	} from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
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
	import { pullStatus, pullStatusLabel, headLabel } from '$lib/pulls';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const repoFullName = $derived(`${namespace}/${name}`);
	const numberParam = $derived(page.params.number ?? '0');
	const number = $derived(BigInt(/^\d+$/.test(numberParam) ? numberParam : '0'));

	type Tab = 'conversation' | 'commits' | 'files';
	const tab = $derived<Tab>(
		((): Tab => {
			const t = page.url.searchParams.get('tab');
			return t === 'commits' || t === 'files' ? t : 'conversation';
		})()
	);

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
				rpcClient.pull
					.listReviews(
						{ namespace, name, number, page: { pageSize: 200, pageToken: '', orderBy: '' } },
						silentCallOptions
					)
					.catch(() => undefined),
				rpcClient.pull
					.listReviewComments({ namespace, name, number, path: '' }, silentCallOptions)
					.catch(() => undefined)
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
	const canComment = $derived(
		authStore.isAuthenticated && !repo.isArchived && (!issue?.locked || canWrite)
	);
	const orgId = $derived(repo.isOrgNamespace ? repo.ownerId : '');
	const pendingReview = $derived(
		reviews.find((r) => r.state === ReviewState.PENDING && r.reviewer?.id === me)
	);
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

	function statusVariant(s: typeof status): 'success' | 'done' | 'destructive' | 'secondary' {
		switch (s) {
			case 'merged':
				return 'done';
			case 'closed':
				return 'destructive';
			case 'draft':
				return 'secondary';
			default:
				return 'success';
		}
	}

	const opened = $derived(issue?.createdAt ? relativeTime(timestampDate(issue.createdAt)) : '');
	const pullParams = $derived({ namespace, name, number: numberParam });
	const fetchHint = $derived(
		pull?.headRef ? `git fetch origin ${pull.headRef}:pr-${numberParam}` : ''
	);
</script>

<svelte:head>
	<title
		>{issue ? `${issue.title} #${numberParam}` : `Pull request #${numberParam}`} - {repoFullName}</title
	>
</svelte:head>

{#snippet tabLink(value: Tab, label: string, count: number)}
	{@const query = value === 'conversation' ? '' : `?tab=${value}`}
	<a
		href={resolve(
			`${resolve('/[namespace]/[name]/pulls/[number]', pullParams)}${query}` as `/${string}`
		)}
		class="-mb-px inline-flex items-center gap-1.5 border-b-2 px-3 py-2 text-sm font-medium whitespace-nowrap {tab ===
		value
			? 'border-primary text-foreground'
			: 'border-transparent text-muted-foreground hover:text-foreground'}"
	>
		{#if value === 'conversation'}<MessageSquare
				class="size-3.5"
			/>{:else if value === 'commits'}<GitCommit class="size-3.5" />{:else}<FileDiff
				class="size-3.5"
			/>{/if}
		{label}
		<span class="rounded-full bg-muted px-1.5 text-xs text-muted-foreground tabular-nums"
			>{count}</span
		>
	</a>
{/snippet}

{#if loading && !pull}
	<div class="space-y-4">
		<Skeleton class="h-7 w-2/3" />
		<Skeleton class="h-4 w-1/2" />
		<div class="flex gap-4 border-b pt-2 pb-2">
			{#each { length: 3 }, i (i)}
				<Skeleton class="h-5 w-24" />
			{/each}
		</div>
		<Skeleton class="h-64 w-full rounded-lg" />
	</div>
{:else if missing || !pull || !issue}
	<EmptyState
		icon={GitPullRequest}
		message="Pull request not found"
		description="#{numberParam} does not exist in {repoFullName} or you cannot see it."
	>
		{#snippet actions()}
			<Button variant="outline" href="/{namespace}/{name}/pulls">Back to pull requests</Button>
		{/snippet}
	</EmptyState>
{:else}
	{@const StatusIcon = statusIcon(status)}
	<div class="space-y-4">
		<header class="space-y-2">
			{#if editingTitle}
				<div class="flex flex-col gap-2 sm:flex-row sm:items-center">
					<Input
						bind:value={titleDraft}
						class="flex-1"
						aria-label="Pull request title"
						onkeydown={(e) => {
							if (e.key === 'Enter') saveTitle();
							if (e.key === 'Escape') editingTitle = false;
						}}
						autofocus
					/>
					<div class="flex items-center justify-end gap-2">
						<Button variant="outline" onclick={() => (editingTitle = false)} disabled={savingTitle}
							><X />Cancel</Button
						>
						<Button onclick={saveTitle} disabled={savingTitle || !titleDraft.trim()}>
							{#if savingTitle}<Loader2 class="animate-spin" />{:else}<Check />{/if}
							Save
						</Button>
					</div>
				</div>
			{:else}
				<div class="flex items-start gap-2">
					<h1
						class="min-w-0 flex-1 text-2xl leading-snug font-semibold tracking-tight wrap-break-word"
					>
						{issue.title}
						<span class="font-normal text-muted-foreground">#{numberParam}</span>
					</h1>
					{#if canEdit}
						<Button
							variant="ghost-muted"
							size="icon-sm"
							class="shrink-0"
							onclick={startTitle}
							title="Edit title"
							aria-label="Edit title"
						>
							<Pencil />
						</Button>
					{/if}
				</div>
			{/if}
			<div class="flex flex-wrap items-center gap-x-2 gap-y-1 text-sm text-muted-foreground">
				<Badge variant={statusVariant(status)}>
					<StatusIcon />
					{pullStatusLabel(status)}
				</Badge>
				<span>
					<a
						href={resolve('/[namespace]', { namespace: issue.author?.username ?? '' })}
						class="font-medium text-foreground hover:text-primary"
						>{issue.author?.username ?? 'unknown'}</a
					>
					{status === 'merged' ? 'merged' : 'wants to merge'}
					<a
						href="{resolve('/[namespace]/[name]/pulls/[number]', pullParams)}?tab=commits"
						class="font-medium text-foreground hover:text-primary"
						>{pull.commitCount} commit{pull.commitCount !== 1 ? 's' : ''}</a
					>
					into
				</span>
				<span class="inline-flex items-center gap-0.5">
					<Badge
						href={resolve('/[namespace]/[name]/tree/[ref]', {
							namespace,
							name,
							ref: encodeURIComponent(pull.baseBranch)
						})}
						class="font-mono">{pull.baseBranch}</Badge
					>
					<CopyButton text={pull.baseBranch} label="Base branch copied" />
				</span>
				<span>from</span>
				<span class="inline-flex items-center gap-0.5">
					{#if pull.headDeleted}
						<Badge variant="secondary" class="font-mono line-through"
							>{headLabel(pull, repoFullName)}</Badge
						>
					{:else}
						{@const [headNs, headName] = (pull.headRepoFullName || repoFullName).split('/')}
						<Badge
							href={resolve('/[namespace]/[name]/tree/[ref]', {
								namespace: headNs,
								name: headName,
								ref: encodeURIComponent(pull.headBranch)
							})}
							class="font-mono">{headLabel(pull, repoFullName)}</Badge
						>
					{/if}
					<CopyButton text={headLabel(pull, repoFullName)} label="Head branch copied" />
				</span>
				<span class="text-muted-foreground/50">|</span>
				<span>opened {opened}</span>
				{#if fetchHint}
					<span class="inline-flex items-center gap-0.5 sm:ml-auto" title={fetchHint}>
						<Terminal class="size-3.5" />
						<span class="hidden font-mono text-xs md:inline">{fetchHint}</span>
						<CopyButton text={fetchHint} label="Fetch command copied" />
					</span>
				{/if}
			</div>
		</header>

		<nav class="flex items-center gap-1 overflow-x-auto border-b">
			{@render tabLink('conversation', 'Conversation', issue.commentCount)}
			{@render tabLink('commits', 'Commits', pull.commitCount)}
			{@render tabLink('files', 'Files changed', pull.changedFiles)}
			<span class="ml-auto pl-3 text-xs whitespace-nowrap tabular-nums">
				<span class="text-success">+{pull.additions}</span>
				<span class="ml-1 text-destructive">-{pull.deletions}</span>
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
