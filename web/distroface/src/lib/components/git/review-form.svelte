<script lang="ts">
	import { Loader2, MessageSquareText, ChevronDown } from '@lucide/svelte';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { RadioGroup, RadioGroupItem } from '$lib/components/ui/radio-group';
	import CommentBox from './comment-box.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
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
	let discardOpen = $state(false);

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

	const canSubmit = $derived(
		!submitting && (choice !== 'comment' || body.trim() !== '' || pendingCount > 0)
	);

	async function submit() {
		if (!canSubmit) return;
		submitting = true;
		const state = stateOf(choice);
		try {
			if (pendingReview) {
				await rpcClient.pull.submitReview({
					namespace,
					name,
					number,
					reviewId: pendingReview.id,
					state,
					body
				});
			} else {
				await rpcClient.pull.createReview({
					namespace,
					name,
					number,
					state,
					body,
					comments: [],
					commitSha: headSha
				});
			}
			toast.success(
				choice === 'approve'
					? 'Approved'
					: choice === 'request_changes'
						? 'Changes requested'
						: 'Review submitted'
			);
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
			discardOpen = false;
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
	<label
		class="flex cursor-pointer items-start gap-3 rounded-lg border px-3 py-2.5 has-data-[state=checked]:border-primary/40 has-data-[state=checked]:bg-primary/5 {off
			? 'cursor-not-allowed opacity-50'
			: ''}"
	>
		<RadioGroupItem {value} class="mt-0.5" disabled={off} />
		<span class="min-w-0 space-y-0.5">
			<span class="block text-sm leading-none font-medium">{label}</span>
			<span class="block text-xs text-muted-foreground">{help}</span>
		</span>
	</label>
{/snippet}

<Popover bind:open>
	<PopoverTrigger>
		{#snippet child({ props })}
			<Button {...props} {disabled}>
				<MessageSquareText />
				Review changes
				{#if pendingCount > 0}
					<Badge variant="secondary" class="tabular-nums">{pendingCount}</Badge>
				{/if}
				<ChevronDown class="opacity-70" />
			</Button>
		{/snippet}
	</PopoverTrigger>
	<PopoverContent class="w-[min(26rem,calc(100vw-2rem))] p-0" align="end">
		<div class="border-b px-4 py-3">
			<p class="text-sm font-semibold">Finish your review</p>
			{#if pendingCount > 0}
				<p class="mt-0.5 text-xs text-muted-foreground">
					{pendingCount} pending comment{pendingCount !== 1 ? 's' : ''} will be published
				</p>
			{/if}
		</div>
		<div class="space-y-4 px-4 py-3">
			<CommentBox
				bind:value={body}
				placeholder="Leave a summary"
				submitLabel="Submit review"
				{submitting}
				onSubmit={submit}
				hideSubmit
			/>
			<RadioGroup bind:value={choice} class="gap-2">
				{@render option('comment', 'Comment', 'Submit general feedback without explicit approval')}
				{@render option(
					'approve',
					'Approve',
					isAuthor
						? 'Authors cannot approve their own pull request'
						: 'Submit feedback and approve merging these changes',
					isAuthor
				)}
				{@render option(
					'request_changes',
					'Request changes',
					'Submit feedback that must be addressed before merging'
				)}
			</RadioGroup>
		</div>
		<div class="flex flex-wrap items-center gap-2 border-t bg-muted/30 px-4 py-2.5">
			{#if pendingReview}
				<Button
					variant="outline-destructive"
					onclick={() => (discardOpen = true)}
					disabled={discarding || submitting}
				>
					{#if discarding}<Loader2 class="animate-spin" />{/if}
					Discard pending
				</Button>
			{/if}
			<Button class="ml-auto" onclick={submit} disabled={!canSubmit}>
				{#if submitting}<Loader2 class="animate-spin" />{/if}
				Submit review
			</Button>
		</div>
	</PopoverContent>
</Popover>

<ConfirmDialog
	bind:open={discardOpen}
	title="Discard pending review"
	confirmLabel="Discard review"
	cancelLabel="Keep review"
	onConfirm={discard}
	loading={discarding}
>
	{#snippet description()}
		Every pending comment in this review is deleted along with the draft summary. This cannot be
		undone.
	{/snippet}
</ConfirmDialog>
