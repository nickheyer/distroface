<script lang="ts">
	import { resolve } from '$app/paths';
	import { Pencil, Loader2, Lock } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import Markdown from '$lib/components/git/markdown.svelte';
	import Timeline from '$lib/components/git/timeline.svelte';
	import CommentBox from '$lib/components/git/comment-box.svelte';
	import ReactionBar from '$lib/components/git/reaction-bar.svelte';
	import IssueSidebar from '$lib/components/git/issue-sidebar.svelte';
	import ReviewersPanel from '$lib/components/git/reviewers-panel.svelte';
	import ChecksPanel from '$lib/components/git/checks-panel.svelte';
	import MergeBox from '$lib/components/git/merge-box.svelte';
	import ReviewForm from '$lib/components/git/review-form.svelte';
	import ReviewCard from './review-card.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { CommentType } from '$lib/proto/distroface/v1/issue_pb';
	import type { Comment, IssueUser } from '$lib/proto/distroface/v1/issue_pb';
	import type { PullRequest, Review, ReviewComment } from '$lib/proto/distroface/v1/pull_pb';
	import { pullStatus } from '$lib/pulls';
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
		onChanged
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
				{ namespace, name, number, commentsOnly: false, page: { pageSize: 50, pageToken: token, orderBy: '' } },
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
	type Segment = { kind: 'timeline'; items: Comment[]; key: string } | { kind: 'review'; event: Comment; key: string };
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

	async function postComment() {
		if (!comment.trim() || posting) return;
		posting = true;
		try {
			await rpcClient.issue.createComment({ namespace, name, number, body: comment });
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
	const edited = $derived(!!issue.updatedAt && !!issue.createdAt && Number(issue.updatedAt.seconds) > Number(issue.createdAt.seconds) + 60);
	const canReview = $derived(authStore.isAuthenticated && status !== 'merged' && status !== 'closed' && !issue.locked);
</script>

<div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_17rem]">
	<div class="space-y-4 min-w-0">
		<div class="rounded-xl border border-border/60 bg-card overflow-hidden">
			<div class="flex flex-wrap items-center gap-2 px-4 py-2 bg-muted/20 border-b border-border/50 text-[13px]">
				<a href={resolve('/[namespace]', { namespace: issue.author?.username ?? '' })} class="font-medium hover:underline">{issue.author?.username ?? 'unknown'}</a>
				<span class="text-muted-foreground">opened {opened}</span>
				{#if edited}
					<span class="text-muted-foreground text-[11.5px]">edited</span>
				{/if}
				{#if canEdit && !editingBody}
					<Button variant="ghost" size="icon" class="h-6 w-6 ml-auto" onclick={startBody} title="Edit description">
						<Pencil class="h-3 w-3 text-muted-foreground" />
					</Button>
				{/if}
			</div>
			<div class="px-4 py-3">
				{#if editingBody}
					<CommentBox bind:value={bodyDraft} submitLabel="Update" submitting={savingBody} onSubmit={saveBody} autofocus>
						{#snippet extra()}
							<Button variant="outline" size="sm" class="h-8" onclick={() => (editingBody = false)} disabled={savingBody}>Cancel</Button>
						{/snippet}
					</CommentBox>
				{:else if issue.body.trim()}
					<Markdown source={issue.body} class="text-[13.5px]" />
				{:else}
					<p class="text-[13px] text-muted-foreground italic">No description provided.</p>
				{/if}
			</div>
			<div class="px-4 pb-2.5">
				<ReactionBar {namespace} {name} number={Number(number)} reactions={issue.reactions} canReact={canComment} onChanged={onChanged} />
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
				<Timeline {namespace} {name} number={Number(number)} items={seg.items} {canComment} canModerate={canWrite} {knownUsers} onChanged={refreshAll} />
			{/if}
		{/each}
		{#if loading && items.length === 0}
			<Timeline {namespace} {name} number={Number(number)} items={[]} canComment={false} canModerate={false} loading />
		{/if}
		{#if nextToken}
			<div class="flex justify-center">
				<Button variant="outline" size="sm" onclick={() => loadTimeline(nextToken)} disabled={loadingMore}>
					{#if loadingMore}<Loader2 class="h-3.5 w-3.5 animate-spin" />{/if}
					Load more
				</Button>
			</div>
		{/if}

		<MergeBox {namespace} {name} {pull} canMerge={canWrite} {canEdit} {onChanged} />

		{#if canComment}
			<CommentBox bind:value={comment} placeholder="Leave a comment..." submitLabel="Comment" submitting={posting} onSubmit={postComment}>
				{#snippet extra()}
					{#if canReview}
						<ReviewForm {namespace} {name} {number} {pendingReview} {pendingCount} headSha={pull.headSha} {isAuthor} onSubmitted={refreshAll} />
					{/if}
				{/snippet}
			</CommentBox>
		{:else if issue.locked}
			<div class="rounded-xl border border-border/60 bg-muted/20 px-4 py-3 text-[13px] text-muted-foreground inline-flex items-center gap-2 w-full">
				<Lock class="h-4 w-4" />
				<span>This conversation has been locked{#if issue.lockReason}&#32;as <strong class="text-foreground">{issue.lockReason}</strong>{/if} and limited to collaborators with write access.</span>
			</div>
		{:else if !authStore.isAuthenticated}
			<div class="rounded-xl border border-border/60 bg-muted/20 px-4 py-3 text-[13px] text-muted-foreground">
				<a href={resolve('/login')} class="text-primary hover:underline">Sign in</a> to join the conversation.
			</div>
		{/if}
	</div>

	<aside class="space-y-5 text-sm">
		<section class="space-y-2">
			<ReviewersPanel {namespace} {name} {pull} canRequest={canEdit && status !== 'merged'} canDismiss={canWrite} {orgId} {onChanged} />
		</section>
		<section class="lg:pt-5 lg:border-t lg:border-border/50">
			<IssueSidebar {namespace} {name} {issue} canWrite={canEdit} canModerate={canWrite} {onChanged} />
		</section>
		<section class="space-y-2 lg:pt-5 lg:border-t lg:border-border/50">
			<span class="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Checks</span>
			<ChecksPanel {namespace} {name} sha={pull.headSha} />
		</section>
	</aside>
</div>
