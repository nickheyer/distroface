<script lang="ts">
	import { resolve } from '$app/paths';
	import { Check, Pencil, Trash2, ChevronDown, ChevronRight, Loader2, Wand2, Reply, Undo2 } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import Markdown from './markdown.svelte';
	import CommentBox from './comment-box.svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import type { ReviewComment } from '$lib/proto/distroface/v1/pull_pb';
	import { parseSuggestions, reviewAnchor } from '$lib/pulls';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	let {
		namespace,
		name,
		number,
		comments,
		canComment = false,
		canModerate = false,
		canApply = false,
		pendingReviewId = '',
		showPath = false,
		onChanged
	}: {
		namespace: string;
		name: string;
		number: bigint;
		comments: ReviewComment[];
		canComment?: boolean;
		canModerate?: boolean;
		canApply?: boolean;
		pendingReviewId?: string;
		showPath?: boolean;
		onChanged?: () => void;
	} = $props();

	const root = $derived(comments[0]);
	const resolved = $derived(!!root?.resolved);
	const threadId = $derived(root?.threadId || root?.id || '');

	let expandedOverride = $state<boolean | null>(null);
	const expanded = $derived(expandedOverride ?? !resolved);

	let replying = $state(false);
	let reply = $state('');
	let submitting = $state(false);
	let resolving = $state(false);
	let editingId = $state('');
	let editBody = $state('');
	let saving = $state(false);
	let deleteId = $state('');
	let deleteOpen = $state(false);
	let deleting = $state(false);
	let applyingId = $state('');

	function isMine(c: ReviewComment): boolean {
		return !!authStore.user && c.author?.id === authStore.user.id;
	}

	function when(c: ReviewComment): string {
		return c.createdAt ? relativeTime(timestampDate(c.createdAt)) : '';
	}

	function lineLabel(c: ReviewComment): string {
		if (c.startLine && c.startLine !== c.line) return `L${c.startLine}-${c.line}`;
		return `L${c.line}`;
	}

	async function send(reviewId: string) {
		if (!reply.trim() || submitting || !root) return;
		submitting = true;
		try {
			await rpcClient.pull.createReviewComment({
				namespace,
				name,
				number,
				reviewId,
				path: root.path,
				line: root.line,
				side: root.side,
				startLine: root.startLine,
				body: reply,
				threadId,
				commitSha: root.commitSha
			});
			reply = '';
			replying = false;
			onChanged?.();
		} catch {
			// Error interceptor already toasted
		} finally {
			submitting = false;
		}
	}

	async function setResolved(value: boolean) {
		if (resolving) return;
		resolving = true;
		try {
			await rpcClient.pull.resolveReviewThread({ namespace, name, number, threadId, resolved: value });
			expandedOverride = null;
			onChanged?.();
		} catch {
			// Error interceptor already toasted
		} finally {
			resolving = false;
		}
	}

	function startEdit(c: ReviewComment) {
		editingId = c.id;
		editBody = c.body;
	}

	async function saveEdit() {
		if (!editBody.trim() || saving) return;
		saving = true;
		try {
			await rpcClient.pull.updateReviewComment({ namespace, name, number, id: editingId, body: editBody });
			editingId = '';
			onChanged?.();
		} catch {
			// Error interceptor already toasted
		} finally {
			saving = false;
		}
	}

	async function confirmDelete() {
		if (deleting) return;
		deleting = true;
		try {
			await rpcClient.pull.deleteReviewComment({ namespace, name, number, id: deleteId });
			deleteOpen = false;
			deleteId = '';
			onChanged?.();
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}

	async function apply(c: ReviewComment) {
		if (applyingId) return;
		applyingId = c.id;
		try {
			await rpcClient.pull.applySuggestion({ namespace, name, number, commentId: c.id, message: '' });
			toast.success('Suggestion applied');
			onChanged?.();
		} catch {
			// Error interceptor already toasted
		} finally {
			applyingId = '';
		}
	}
</script>

{#if root}
	<div class="rounded-lg border border-border/60 bg-card text-[13px] my-1.5 overflow-hidden {resolved ? 'opacity-90' : ''}" id={reviewAnchor(root.id)}>
		<div class="flex flex-wrap items-center gap-x-2 gap-y-1 px-3 py-1.5 bg-muted/30 border-b border-border/50 {expanded ? '' : 'border-b-0'}">
			<button type="button" class="text-muted-foreground hover:text-foreground" onclick={() => (expandedOverride = !expanded)} aria-label="Toggle thread">
				{#if expanded}
					<ChevronDown class="h-3.5 w-3.5" />
				{:else}
					<ChevronRight class="h-3.5 w-3.5" />
				{/if}
			</button>
			{#if showPath}
				<span class="font-mono text-[12px] truncate min-w-0">{root.path}</span>
			{/if}
			<span class="font-mono text-[11px] text-muted-foreground">{lineLabel(root)} {root.side === 'old' ? '(old)' : ''}</span>
			{#if !expanded}
				<span class="text-muted-foreground truncate min-w-0 flex-1">
					{root.author?.username ?? 'unknown'}: {root.body.split('\n')[0]}
				</span>
			{/if}
			<span class="ml-auto flex items-center gap-1.5 shrink-0">
				{#if root.outdated}
					<Badge variant="outline" class="text-[10px] h-4.5 px-1.5 border-amber-500/40 text-amber-600 dark:text-amber-400">Outdated</Badge>
				{/if}
				{#if root.pending}
					<Badge variant="outline" class="text-[10px] h-4.5 px-1.5 border-amber-500/40 text-amber-600 dark:text-amber-400">Pending</Badge>
				{/if}
				{#if resolved}
					<Badge variant="outline" class="text-[10px] h-4.5 px-1.5 border-emerald-500/40 text-emerald-600 dark:text-emerald-400">Resolved</Badge>
				{/if}
				{#if comments.length > 1}
					<span class="text-[11px] text-muted-foreground tabular-nums">{comments.length} comments</span>
				{/if}
			</span>
		</div>

		{#if expanded}
			<div class="divide-y divide-border/50">
				{#each comments as c (c.id)}
					<div class="px-3 py-2.5 space-y-2" id={c.id === root.id ? undefined : reviewAnchor(c.id)}>
						<div class="flex items-center gap-2">
							<a href={resolve('/[namespace]', { namespace: c.author?.username ?? '' })} class="font-medium hover:underline">{c.author?.username ?? 'unknown'}</a>
							<span class="text-muted-foreground text-[12px]">{when(c)}</span>
							{#if c.updatedAt && c.createdAt && Number(c.updatedAt.seconds) > Number(c.createdAt.seconds)}
								<span class="text-muted-foreground text-[11px]">edited</span>
							{/if}
							{#if c.pending && c.id !== root.id}
								<Badge variant="outline" class="text-[10px] h-4.5 px-1.5 border-amber-500/40 text-amber-600 dark:text-amber-400">Pending</Badge>
							{/if}
							<span class="ml-auto flex items-center gap-0.5">
								{#if (isMine(c) || canModerate) && editingId !== c.id}
									<Button variant="ghost" size="icon" class="h-6 w-6" onclick={() => startEdit(c)} title="Edit">
										<Pencil class="h-3 w-3 text-muted-foreground" />
									</Button>
									<Button variant="ghost" size="icon" class="h-6 w-6" onclick={() => { deleteId = c.id; deleteOpen = true; }} title="Delete">
										<Trash2 class="h-3 w-3 text-muted-foreground" />
									</Button>
								{/if}
							</span>
						</div>

						{#if editingId === c.id}
							<CommentBox bind:value={editBody} submitLabel="Save" submitting={saving} onSubmit={saveEdit} autofocus>
								{#snippet extra()}
									<Button variant="outline" size="sm" class="h-8" onclick={() => (editingId = '')} disabled={saving}>Cancel</Button>
								{/snippet}
							</CommentBox>
						{:else}
							{#each parseSuggestions(c.body) as seg, i (i)}
								{#if seg.kind === 'text'}
									<Markdown source={seg.text} class="text-[13px]" />
								{:else}
									<div class="rounded-md border border-border/60 overflow-hidden">
										<div class="flex items-center gap-2 px-2.5 py-1 bg-muted/40 border-b border-border/50 text-[11.5px]">
											<Wand2 class="h-3 w-3 text-muted-foreground" />
											<span class="font-medium">Suggested change</span>
											{#if canApply && !c.pending && !c.outdated}
												<Button variant="outline" size="sm" class="h-7 px-2 ml-auto text-[11.5px]" onclick={() => apply(c)} disabled={applyingId !== ''}>
													{#if applyingId === c.id}<Loader2 class="h-3 w-3 animate-spin" />{/if}
													Apply suggestion
												</Button>
											{:else if c.outdated}
												<span class="ml-auto text-muted-foreground">Outdated</span>
											{/if}
										</div>
										<pre class="px-2.5 py-1.5 font-mono text-[12px] overflow-x-auto bg-[color-mix(in_oklch,var(--success)_10%,transparent)] whitespace-pre">{seg.code}</pre>
									</div>
								{/if}
							{/each}
						{/if}
					</div>
				{/each}
			</div>

			{#if canComment || canModerate}
				<div class="px-3 py-2 border-t border-border/50 bg-muted/20 space-y-2">
					{#if replying}
						<CommentBox
							bind:value={reply}
							placeholder="Reply..."
							submitLabel="Reply"
							{submitting}
							autofocus
							onSubmit={() => send('')}
						>
							{#snippet extra()}
								{#if pendingReviewId}
									<Button variant="outline" size="sm" class="h-8" onclick={() => send(pendingReviewId)} disabled={submitting || !reply.trim()}>
										Add to review
									</Button>
								{/if}
								<Button variant="ghost" size="sm" class="h-8" onclick={() => { replying = false; reply = ''; }} disabled={submitting}>Cancel</Button>
							{/snippet}
						</CommentBox>
					{:else}
						<div class="flex items-center gap-2">
							{#if canComment}
								<button
									type="button"
									class="flex-1 text-left rounded-md border border-border/60 bg-background px-2.5 py-1.5 text-[12.5px] text-muted-foreground hover:border-border inline-flex items-center gap-1.5"
									onclick={() => (replying = true)}
								>
									<Reply class="h-3.5 w-3.5" />Reply...
								</button>
							{/if}
							{#if !root.pending}
								<Button variant="outline" size="sm" class="h-8 shrink-0" onclick={() => setResolved(!resolved)} disabled={resolving}>
									{#if resolving}
										<Loader2 class="h-3 w-3 animate-spin" />
									{:else if resolved}
										<Undo2 class="h-3 w-3" />
									{:else}
										<Check class="h-3 w-3" />
									{/if}
									{resolved ? 'Unresolve' : 'Resolve'}
								</Button>
							{/if}
						</div>
					{/if}
				</div>
			{/if}
		{/if}
	</div>

	<ConfirmDialog
		bind:open={deleteOpen}
		title="Delete comment?"
		confirmLabel="Delete"
		loading={deleting}
		onConfirm={confirmDelete}
	>
		{#snippet description()}This review comment will be removed permanently.{/snippet}
	</ConfirmDialog>
{/if}
