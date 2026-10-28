<script lang="ts">
	import { resolve } from '$app/paths';
	import { CheckCircle, XCircle, MessageSquare, Ban, FileDiff } from '$lib/icons';
	import { Badge } from '$lib/components/ui/badge';
	import Markdown from '$lib/components/git/markdown.svelte';
	import { ReviewState } from '$lib/proto/distroface/v1/pull_pb';
	import type { Review, ReviewComment } from '$lib/proto/distroface/v1/pull_pb';
	import type { Comment } from '$lib/proto/distroface/v1/issue_pb';
	import { reviewStateLabel, reviewAnchor } from '$lib/pulls';
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
				return CheckCircle;
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

	function badgeVariant(s: ReviewState): 'outline' | 'success' | 'destructive' | 'warning' {
		if (dismissed) return 'outline';
		switch (s) {
			case ReviewState.APPROVED:
				return 'success';
			case ReviewState.CHANGES_REQUESTED:
				return 'destructive';
			case ReviewState.PENDING:
				return 'warning';
			default:
				return 'outline';
		}
	}

	const Icon = $derived(icon(state));
</script>

<div class="box" id="review-{reviewId}">
	<div
		class="flex flex-wrap items-center gap-x-2 gap-y-1 border-b px-4 py-2 text-xs text-muted-foreground"
	>
		<Icon class="size-4 shrink-0 {color(state)}" />
		<a
			href={resolve('/[namespace]', { namespace: event.author?.username ?? '' })}
			class="font-medium text-foreground hover:text-primary"
			>{event.author?.username ?? 'unknown'}</a
		>
		<span>{verb(state)}</span>
		{#if sha}
			<a
				href={resolve('/[namespace]/[name]/commit/[sha]', { namespace, name, sha })}
				class="font-mono hover:text-primary">{sha.slice(0, 7)}</a
			>
		{/if}
		<span>{when}</span>
		<Badge variant={badgeVariant(state)} class="ml-auto">
			{dismissed ? 'Dismissed' : reviewStateLabel(state)}
		</Badge>
	</div>
	{#if body.trim()}
		<div class="px-4 py-3 text-sm">
			<Markdown source={body} />
		</div>
	{/if}
	{#if comments.length > 0}
		<div class="border-t px-4 py-2 text-xs">
			<p class="mb-1 inline-flex items-center gap-1.5 text-muted-foreground">
				<FileDiff class="size-3.5" />
				{comments.length} inline comment{comments.length !== 1 ? 's' : ''}
			</p>
			<ul class="space-y-0.5">
				{#each comments as c (c.id)}
					<li class="flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5">
						<a
							href="{resolve('/[namespace]/[name]/pulls/[number]', {
								namespace,
								name,
								number
							})}?tab=files#{reviewAnchor(c.threadId || c.id)}"
							class="link max-w-full truncate font-mono"
						>
							{c.path}:{c.startLine && c.startLine !== c.line ? `${c.startLine}-${c.line}` : c.line}
						</a>
						<span class="min-w-0 truncate text-muted-foreground">{c.body.split('\n')[0]}</span>
						{#if c.outdated}
							<Badge variant="warning" class="shrink-0">Outdated</Badge>
						{/if}
					</li>
				{/each}
			</ul>
		</div>
	{/if}
</div>
