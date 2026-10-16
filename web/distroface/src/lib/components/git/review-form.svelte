<script lang="ts">
	import { Loader2, MessageSquareText, ChevronDown } from '@lucide/svelte';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { RadioGroup, RadioGroupItem } from '$lib/components/ui/radio-group';
	import CommentBox from './comment-box.svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { ReviewState } from '$lib/proto/distroface/v1/pull_pb';
	import type { Review } from '$lib/proto/distroface/v1/pull_pb';
	import { toast } from 'svelte-sonner';

	let {
		namespace,
		name,
		number,
		pendingReview,
		pendingCount = 0,
		headSha = '',
		isAuthor = false,
		disabled = false,
		onSubmitted
	}: {
		namespace: string;
		name: string;
		number: bigint;
		pendingReview?: Review;
		pendingCount?: number;
		headSha?: string;
		isAuthor?: boolean;
		disabled?: boolean;
		onSubmitted?: () => void;
	} = $props();

	type Choice = 'comment' | 'approve' | 'request_changes';

	let open = $state(false);
	let body = $state('');
	let choice = $state<Choice>('comment');
	let submitting = $state(false);
	let discarding = $state(false);

	$effect(() => {
		if (open && pendingReview?.body && !body) body = pendingReview.body;
	});

	function stateOf(c: Choice): ReviewState {
		switch (c) {
			case 'approve':
				return ReviewState.APPROVED;
			case 'request_changes':
				return ReviewState.CHANGES_REQUESTED;
			default:
				return ReviewState.COMMENTED;
		}
	}

	const canSubmit = $derived(!submitting && (choice !== 'comment' || body.trim() !== '' || pendingCount > 0));

	async function submit() {
		if (!canSubmit) return;
		submitting = true;
		const state = stateOf(choice);
		try {
			if (pendingReview) {
				await rpcClient.pull.submitReview({ namespace, name, number, reviewId: pendingReview.id, state, body });
			} else {
				await rpcClient.pull.createReview({ namespace, name, number, state, body, comments: [], commitSha: headSha });
			}
			toast.success(choice === 'approve' ? 'Approved' : choice === 'request_changes' ? 'Changes requested' : 'Review submitted');
			body = '';
			choice = 'comment';
			open = false;
			onSubmitted?.();
		} catch {
			// Error interceptor already toasted
		} finally {
			submitting = false;
		}
	}

	async function discard() {
		if (!pendingReview || discarding) return;
		discarding = true;
		try {
			const resp = await rpcClient.pull.listReviewComments({ namespace, name, number, path: '' });
			const mine = resp.comments.filter((c) => c.pending && c.reviewId === pendingReview.id);
			for (const c of mine) {
				await rpcClient.pull.deleteReviewComment({ namespace, name, number, id: c.id });
			}
			toast.success('Pending comments discarded');
			body = '';
			open = false;
			onSubmitted?.();
		} catch {
			// Error interceptor already toasted
		} finally {
			discarding = false;
		}
	}
</script>

{#snippet option(value: Choice, label: string, help: string, off = false)}
	<label class="flex items-start gap-3 rounded-lg border border-border/60 px-3 py-2.5 cursor-pointer has-[[data-state=checked]]:border-primary/40 has-[[data-state=checked]]:bg-primary/5 {off ? 'opacity-50 cursor-not-allowed' : ''}">
		<RadioGroupItem {value} class="mt-0.5" disabled={off} />
		<span class="min-w-0 space-y-0.5">
			<span class="block text-[13px] font-medium leading-none">{label}</span>
			<span class="block text-[12px] text-muted-foreground">{help}</span>
		</span>
	</label>
{/snippet}

<Popover bind:open>
	<PopoverTrigger>
		{#snippet child({ props })}
			<Button {...props} size="sm" class="h-8 gap-1.5" {disabled}>
				<MessageSquareText class="h-3.5 w-3.5" />
				Review changes
				{#if pendingCount > 0}
					<Badge variant="secondary" class="h-4.5 px-1.5 text-[10px] tabular-nums">{pendingCount}</Badge>
				{/if}
				<ChevronDown class="h-3.5 w-3.5 opacity-70" />
			</Button>
		{/snippet}
	</PopoverTrigger>
	<PopoverContent class="w-[min(26rem,calc(100vw-2rem))] p-0" align="end">
		<div class="px-4 py-3 border-b border-border/50">
			<p class="text-[13px] font-semibold">Finish your review</p>
			{#if pendingCount > 0}
				<p class="text-[12px] text-muted-foreground mt-0.5">{pendingCount} pending comment{pendingCount !== 1 ? 's' : ''} will be published</p>
			{/if}
		</div>
		<div class="px-4 py-3 space-y-3">
			<CommentBox bind:value={body} placeholder="Leave a summary..." submitLabel="Submit Review" {submitting} onSubmit={submit} hideSubmit />
			<RadioGroup bind:value={choice} class="gap-2">
				{@render option('comment', 'Comment', 'Submit general feedback without explicit approval.')}
				{@render option('approve', 'Approve', isAuthor ? 'Authors cannot approve their own pull request.' : 'Submit feedback and approve merging these changes.', isAuthor)}
				{@render option('request_changes', 'Request changes', 'Submit feedback that must be addressed before merging.')}
			</RadioGroup>
		</div>
		<div class="flex flex-wrap items-center gap-2 px-4 py-2.5 border-t border-border/50 bg-muted/20">
			{#if pendingReview}
				<Button variant="ghost" size="sm" class="h-8 text-destructive hover:text-destructive" onclick={discard} disabled={discarding || submitting}>
					{#if discarding}<Loader2 class="h-3 w-3 animate-spin" />{/if}
					Discard pending
				</Button>
			{/if}
			<Button size="sm" class="h-8 ml-auto" onclick={submit} disabled={!canSubmit}>
				{#if submitting}<Loader2 class="h-3 w-3 animate-spin" />{/if}
				Submit Review
			</Button>
		</div>
	</PopoverContent>
</Popover>
