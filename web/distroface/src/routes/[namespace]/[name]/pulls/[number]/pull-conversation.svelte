<script lang="ts">
	import { resolve } from '$app/paths';
	import { Pencil, Loader, Lock } from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import { Switch } from '$lib/components/ui/switch';
	import FormField from '$lib/components/form-field.svelte';
	import Markdown from '$lib/components/git/markdown.svelte';
	import Timeline from '$lib/components/git/timeline.svelte';
	import CommentBox from '$lib/components/git/comment-box.svelte';
	import ReactionBar from '$lib/components/git/reaction-bar.svelte';
	import IssueSidebar from '$lib/components/git/issue-sidebar.svelte';
	import ReviewersPanel from '$lib/components/git/reviewers-panel.svelte';
	import ChecksPanel from '$lib/components/git/checks-panel.svelte';
	import AgentSessionsPanel from '$lib/components/git/agent-sessions-panel.svelte';
	import MergeBox from '$lib/components/git/merge-box.svelte';
	import ReviewForm from '$lib/components/git/review-form.svelte';
	import ReviewCard from './review-card.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { CommentType } from '$lib/proto/distroface/v1/issue_pb';
	import type { Comment, IssueUser } from '$lib/proto/distroface/v1/issue_pb';
	import type { PullRequest, Review, ReviewComment } from '$lib/proto/distroface/v1/pull_pb';
	import { pullStatus, headIsFork } from '$lib/pulls';
	import { SvelteMap } from 'svelte/reactivity';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';

	let {
		namespace,
		name,
		pull,
		reviews,
		reviewComments,
		pendingReview,
		pendingCount = 0,
		canComment = false,
		canEdit = false,
		canWrite = false,
		isAuthor = false,
		orgId = '',
		onChanged,
		tick = 0
	}: {
		namespace: string;
		name: string;
		pull: PullRequest;
		reviews: Review[];
		reviewComments: ReviewComment[];
		pendingReview?: Review;
		pendingCount?: number;
		canComment?: boolean;
		canEdit?: boolean;
		canWrite?: boolean;
		isAuthor?: boolean;
		orgId?: string;
		onChanged: () => void;
		// Bumped by the page's background poll
		tick?: number;
	} = $props();

	const issue = $derived(pull.issue!);
	const number = $derived(issue.number);
	const numberStr = $derived(String(number));
	const status = $derived(pullStatus(pull));

	let items = $state<Comment[]>([]);
	let nextToken = $state('');
	let loading = $state(true);
	let loadingMore = $state(false);
	let seq = 0;

	async function loadTimeline(token = '') {
		const mine = ++seq;
		if (token) loadingMore = true;
		else loading = true;
		try {
			const resp = await rpcClient.issue.listComments(
				{
					namespace,
					name,
					number,
					isPull: true,
					commentsOnly: false,
					page: { pageSize: 50, pageToken: token, orderBy: '' }
				},
				silentCallOptions
			);
			if (mine !== seq) return;
			items = token ? [...items, ...resp.comments] : resp.comments;
			nextToken = resp.page?.nextPageToken ?? '';
		} catch {
			if (mine !== seq) return;
			if (!token) items = [];
			nextToken = '';
		} finally {
			if (mine === seq) {
				loading = false;
				loadingMore = false;
			}
		}
	}

	// Reloads the whole timeline after every change upstream
	let lastSeen = '';
	$effect(() => {
		const key = `${issue.id}:${issue.updatedAt?.seconds ?? 0n}:${issue.commentCount}:${reviews.length}`;
		if (lastSeen === key) return;
		lastSeen = key;
		loadTimeline();
	});

	// Review events become cards, everything else stays on the timeline
	type Segment =
		| { kind: 'timeline'; items: Comment[]; key: string }
		| { kind: 'review'; event: Comment; key: string };
	const segments = $derived.by(() => {
		const out: Segment[] = [];
		for (const c of items) {
			if (c.type === CommentType.REVIEW) {
				out.push({ kind: 'review', event: c, key: `r${c.id}` });
				continue;
			}
			const last = out[out.length - 1];
			if (last && last.kind === 'timeline') last.items.push(c);
			else out.push({ kind: 'timeline', items: [c], key: `t${c.id}` });
		}
		return out;
	});

	const reviewById = $derived(new Map(reviews.map((r) => [r.id, r])));
	const commentsByReview = $derived.by(() => {
		const map = new SvelteMap<string, ReviewComment[]>();
		for (const c of reviewComments) {
			if (c.pending) continue;
			const list = map.get(c.reviewId) ?? [];
			list.push(c);
			map.set(c.reviewId, list);
		}
		return map;
	});

	const knownUsers = $derived.by(() => {
		const users: IssueUser[] = [];
		if (issue.author) users.push(issue.author);
		for (const a of issue.assignees) users.push(a);
		for (const r of pull.reviewRequests) if (r.user) users.push(r.user);
		for (const r of reviews) if (r.reviewer) users.push(r.reviewer);
		if (pull.mergedBy) users.push(pull.mergedBy);
		return users;
	});

	let comment = $state('');
	let posting = $state(false);
	let commentBox = $state<ReturnType<typeof CommentBox>>();

	function insertReply(text: string) {
		comment = comment.trim() ? `${comment.trimEnd()}\n\n${text}` : text;
		void commentBox?.focus();
	}

	async function postComment() {
		if (!comment.trim() || posting) return;
		posting = true;
		try {
			await rpcClient.issue.createComment({ namespace, name, number, isPull: true, body: comment });
			comment = '';
			await loadTimeline();
			onChanged();
		} catch {
			// Error interceptor already toasted
		} finally {
			posting = false;
		}
	}

	let editingBody = $state(false);
	let bodyDraft = $state('');
	let savingBody = $state(false);

	function startBody() {
		bodyDraft = issue.body;
		editingBody = true;
	}

	async function saveBody() {
		if (savingBody) return;
		savingBody = true;
		try {
			await rpcClient.pull.updatePullRequest({ namespace, name, number, body: bodyDraft });
			editingBody = false;
			onChanged();
		} catch {
			// Error interceptor already toasted
		} finally {
			savingBody = false;
		}
	}

	function refreshAll() {
		loadTimeline();
		onChanged();
	}

	const opened = $derived(issue.createdAt ? relativeTime(timestampDate(issue.createdAt)) : '');
	const edited = $derived(
		!!issue.updatedAt &&
			!!issue.createdAt &&
			Number(issue.updatedAt.seconds) > Number(issue.createdAt.seconds) + 60
	);
	const canReview = $derived(
		authStore.isAuthenticated && status !== 'merged' && status !== 'closed' && !issue.locked
	);

	// Fork authors decide whether base writers may push to their branch
	const isFork = $derived(headIsFork(pull, `${namespace}/${name}`));
	const canSetMaintainerEdits = $derived(isAuthor && status !== 'merged' && status !== 'closed');
	let maintainerBusy = $state(false);

	async function setMaintainerEdits(allow: boolean) {
		if (maintainerBusy || allow === pull.allowMaintainerEdits) return;
		maintainerBusy = true;
		try {
			await rpcClient.pull.updatePullRequest({
				namespace,
				name,
				number,
				allowMaintainerEdits: allow
			});
			onChanged();
		} catch {
			// Error interceptor already toasted
		} finally {
			maintainerBusy = false;
		}
	}
</script>

<div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_17rem]">
	<div class="min-w-0 space-y-4">
		<div class="box">
			<div
				class="flex flex-wrap items-center gap-2 border-b px-4 py-2 text-xs text-muted-foreground"
			>
				<a
					href={resolve('/[namespace]', { namespace: issue.author?.username ?? '' })}
					class="font-medium text-foreground hover:text-primary"
					>{issue.author?.username ?? 'unknown'}</a
				>
				<span>opened {opened}</span>
				{#if edited}
					<span>(edited)</span>
				{/if}
				{#if canEdit && !editingBody}
					<Button
						variant="ghost-muted"
						size="icon-xs"
						class="ml-auto"
						onclick={startBody}
						title="Edit description"
						aria-label="Edit description"
					>
						<Pencil />
					</Button>
				{/if}
			</div>
			<div class="px-4 py-3 text-sm">
				{#if editingBody}
					<CommentBox
						bind:value={bodyDraft}
						submitLabel="Update"
						submitting={savingBody}
						onSubmit={saveBody}
						autofocus
					>
						{#snippet extra()}
							<Button variant="outline" onclick={() => (editingBody = false)} disabled={savingBody}
								>Cancel</Button
							>
						{/snippet}
					</CommentBox>
				{:else if issue.body.trim()}
					<Markdown source={issue.body} />
				{:else}
					<p class="text-muted-foreground italic">No description provided</p>
				{/if}
			</div>
			<div class="px-4 pb-3">
				<ReactionBar
					{namespace}
					{name}
					number={Number(number)}
					isPull
					reactions={issue.reactions}
					canReact={canComment}
					{onChanged}
				/>
			</div>
		</div>

		{#each segments as seg (seg.key)}
			{#if seg.kind === 'review'}
				<ReviewCard
					{namespace}
					{name}
					number={numberStr}
					event={seg.event}
					review={reviewById.get(seg.event.reviewId)}
					comments={commentsByReview.get(seg.event.reviewId) ?? []}
				/>
			{:else}
				<Timeline
					{namespace}
					{name}
					number={Number(number)}
					isPull
					items={seg.items}
					{canComment}
					canModerate={canWrite}
					{knownUsers}
					onChanged={refreshAll}
					onReply={insertReply}
				/>
			{/if}
		{/each}
		{#if loading && items.length === 0}
			<Timeline
				{namespace}
				{name}
				number={Number(number)}
				isPull
				items={[]}
				canComment={false}
				canModerate={false}
				loading
			/>
		{/if}
		{#if nextToken}
			<div class="flex justify-center">
				<Button variant="outline" onclick={() => loadTimeline(nextToken)} disabled={loadingMore}>
					{#if loadingMore}<Loader class="animate-spin" />{/if}
					Load more
				</Button>
			</div>
		{/if}

		<MergeBox
			{namespace}
			{name}
			{pull}
			canMerge={canWrite && !gitRepoStore.isMirror}
			{canEdit}
			{onChanged}
		/>

		{#if canComment}
			<CommentBox
				bind:this={commentBox}
				bind:value={comment}
				placeholder="Leave a comment"
				submitLabel="Comment"
				submitting={posting}
				onSubmit={postComment}
			>
				{#snippet extra()}
					{#if canReview}
						<ReviewForm
							{namespace}
							{name}
							{number}
							{pendingReview}
							{pendingCount}
							headSha={pull.headSha}
							{isAuthor}
							onSubmitted={refreshAll}
						/>
					{/if}
				{/snippet}
			</CommentBox>
		{:else if issue.locked}
			<Alert>
				<Lock />
				<AlertDescription>
					<span
						>This conversation has been locked {#if issue.lockReason}as <strong
								class="text-foreground">{issue.lockReason}</strong
							>{/if} and limited to collaborators with write access.</span
					>
				</AlertDescription>
			</Alert>
		{:else if !authStore.isAuthenticated}
			<Alert>
				<AlertDescription>
					<span><a href={resolve('/login')} class="link">Sign in</a> to join the conversation</span>
				</AlertDescription>
			</Alert>
		{/if}
	</div>

	<aside class="space-y-5 text-sm">
		<section class="space-y-2">
			<ReviewersPanel
				{namespace}
				{name}
				{pull}
				canRequest={canEdit && status !== 'merged'}
				canDismiss={canWrite}
				{orgId}
				{onChanged}
			/>
		</section>
		<section class="lg:border-t lg:pt-5">
			<IssueSidebar
				{namespace}
				{name}
				{issue}
				canWrite={canEdit}
				canModerate={canWrite}
				{onChanged}
			/>
		</section>
		<AgentSessionsPanel
			{namespace}
			{name}
			issueNumber={number}
			isPull
			canStart={canWrite && !gitRepoStore.isMirror}
			{tick}
		/>
		{#if isFork}
			<section class="space-y-2 lg:border-t lg:pt-5">
				<div class="flex min-h-6 items-center">
					<span class="eyebrow">Maintainer edits</span>
				</div>
				{#if canSetMaintainerEdits}
					<FormField
						label="Allow edits by maintainers"
						layout="toggle"
						help="Writers on this repository may push to your branch"
					>
						<Switch
							checked={pull.allowMaintainerEdits}
							onCheckedChange={setMaintainerEdits}
							disabled={maintainerBusy}
							aria-label="Allow edits by maintainers"
						/>
					</FormField>
				{:else}
					<p class="text-xs text-muted-foreground">
						{pull.allowMaintainerEdits
							? 'Writers on this repository may push to the head branch'
							: 'Only the author can push to the head branch'}
					</p>
				{/if}
			</section>
		{/if}
		<section class="space-y-2 lg:border-t lg:pt-5">
			<div class="flex min-h-6 items-center">
				<span class="eyebrow">Checks</span>
			</div>
			<ChecksPanel {namespace} {name} sha={pull.headSha} />
		</section>
	</aside>
</div>
