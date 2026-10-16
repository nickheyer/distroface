<script lang="ts">
	import { resolve } from '$app/paths';
	import { CheckCircle2, XCircle, MessageSquare, Ban, FileDiff } from '@lucide/svelte';
	import { Badge } from '$lib/components/ui/badge';
	import Markdown from '$lib/components/git/markdown.svelte';
	import { ReviewState } from '$lib/proto/distroface/v1/pull_pb';
	import type { Review, ReviewComment } from '$lib/proto/distroface/v1/pull_pb';
	import type { Comment } from '$lib/proto/distroface/v1/issue_pb';
	import { reviewStateLabel, reviewStateClass, reviewAnchor } from '$lib/pulls';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';

	let {
		namespace,
		name,
		number,
		event,
		review,
		comments
	}: {
		namespace: string;
		name: string;
		number: string;
		event: Comment;
		review?: Review;
		comments: ReviewComment[];
	} = $props();

	type Payload = { state?: string; body?: string; commit_sha?: string };

	const payload = $derived.by((): Payload => {
		try {
			return JSON.parse(event.payload || '{}');
		} catch {
			return {};
		}
	});

	const state = $derived.by((): ReviewState => {
		if (review) return review.state;
		switch (payload.state) {
			case 'approved':
				return ReviewState.APPROVED;
			case 'changes_requested':
				return ReviewState.CHANGES_REQUESTED;
			default:
				return ReviewState.COMMENTED;
		}
	});
	const body = $derived(review?.body ?? payload.body ?? event.body);
	const dismissed = $derived(review?.dismissed ?? false);
	const reviewId = $derived(review?.id || event.reviewId);
	const when = $derived(event.createdAt ? relativeTime(timestampDate(event.createdAt)) : '');
	const sha = $derived(review?.commitSha || payload.commit_sha || '');

	function verb(s: ReviewState): string {
		switch (s) {
			case ReviewState.APPROVED:
				return 'approved these changes';
			case ReviewState.CHANGES_REQUESTED:
				return 'requested changes';
			default:
				return 'reviewed';
		}
	}

	function icon(s: ReviewState) {
		if (dismissed) return Ban;
		switch (s) {
			case ReviewState.APPROVED:
				return CheckCircle2;
			case ReviewState.CHANGES_REQUESTED:
				return XCircle;
			default:
				return MessageSquare;
		}
	}

	function color(s: ReviewState): string {
		if (dismissed) return 'text-muted-foreground';
		switch (s) {
			case ReviewState.APPROVED:
				return 'text-success';
			case ReviewState.CHANGES_REQUESTED:
				return 'text-destructive';
			default:
				return 'text-muted-foreground';
		}
	}

	const Icon = $derived(icon(state));
</script>

<div class="rounded-xl border border-border/60 bg-card overflow-hidden" id="review-{reviewId}">
	<div class="flex flex-wrap items-center gap-x-2 gap-y-1 px-4 py-2 bg-muted/20 border-b border-border/50 text-[13px]">
		<Icon class="h-4 w-4 shrink-0 {color(state)}" />
		<a href={resolve('/[namespace]', { namespace: event.author?.username ?? '' })} class="font-medium hover:underline">{event.author?.username ?? 'unknown'}</a>
		<span class="text-muted-foreground">{verb(state)}</span>
		{#if sha}
			<a href={resolve('/[namespace]/[name]/commit/[sha]', { namespace, name, sha })} class="font-mono text-[11.5px] text-muted-foreground hover:text-primary">{sha.slice(0, 7)}</a>
		{/if}
		<span class="text-muted-foreground text-[12px]">{when}</span>
		<Badge variant="outline" class="ml-auto text-[10px] h-4.5 px-1.5 {reviewStateClass(state, dismissed)}">
			{dismissed ? 'Dismissed' : reviewStateLabel(state)}
		</Badge>
	</div>
	{#if body.trim()}
		<div class="px-4 py-3">
			<Markdown source={body} class="text-[13.5px]" />
		</div>
	{/if}
	{#if comments.length > 0}
		<div class="border-t border-border/50 px-4 py-2 text-[12.5px]">
			<p class="text-muted-foreground mb-1 inline-flex items-center gap-1.5">
				<FileDiff class="h-3.5 w-3.5" />
				{comments.length} inline comment{comments.length !== 1 ? 's' : ''}
			</p>
			<ul class="space-y-0.5">
				{#each comments as c (c.id)}
					<li class="flex flex-wrap items-center gap-x-2 gap-y-0.5 min-w-0">
						<a href="{resolve('/[namespace]/[name]/pulls/[number]', { namespace, name, number })}?tab=files#{reviewAnchor(c.threadId || c.id)}" class="font-mono text-[12px] text-primary hover:underline truncate max-w-full">
							{c.path}:{c.startLine && c.startLine !== c.line ? `${c.startLine}-${c.line}` : c.line}
						</a>
						<span class="text-muted-foreground truncate min-w-0">{c.body.split('\n')[0]}</span>
						{#if c.outdated}
							<span class="text-[10px] uppercase tracking-wide text-amber-600 dark:text-amber-400 shrink-0">outdated</span>
						{/if}
					</li>
				{/each}
			</ul>
		</div>
	{/if}
</div>
