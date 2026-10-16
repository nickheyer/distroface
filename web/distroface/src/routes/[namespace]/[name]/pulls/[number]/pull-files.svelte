<script lang="ts">
	import { page } from '$app/state';
	import { replaceState } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { ChevronDown, ChevronRight, History } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import DiffViewer from '$lib/components/git/diff-viewer.svelte';
	import type { DiffSide } from '$lib/components/git/diff-file.svelte';
	import CommentBox from '$lib/components/git/comment-box.svelte';
	import ReviewThread from '$lib/components/git/review-thread.svelte';
	import ReviewForm from '$lib/components/git/review-form.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { DiffLineKind } from '$lib/proto/distroface/v1/git_pb';
	import type { DiffFile } from '$lib/proto/distroface/v1/git_pb';
	import { ReviewState } from '$lib/proto/distroface/v1/pull_pb';
	import type { PullRequest, Review, ReviewComment } from '$lib/proto/distroface/v1/pull_pb';
	import { groupThreads, threadKey, pullStatus, reviewAnchor } from '$lib/pulls';
	import { SvelteMap, SvelteURLSearchParams } from 'svelte/reactivity';

	let {
		namespace,
		name,
		pull,
		reviewComments,
		pendingReview,
		pendingCount = 0,
		canComment = false,
		canModerate = false,
		canApply = false,
		isAuthor = false,
		onChanged
	}: {
		namespace: string;
		name: string;
		pull: PullRequest;
		reviewComments: ReviewComment[];
		pendingReview?: Review;
		pendingCount?: number;
		canComment?: boolean;
		canModerate?: boolean;
		canApply?: boolean;
		isAuthor?: boolean;
		onChanged: () => void;
	} = $props();

	const number = $derived(pull.issue!.number);
	const headSha = $derived(pull.headSha);
	const status = $derived(pullStatus(pull));
	const canReview = $derived(authStore.isAuthenticated && canComment && status !== 'merged' && status !== 'closed');

	function pathOf(f: DiffFile): string {
		return f.newPath || f.oldPath;
	}

	// Deep links name a file directly or through a review comment
	const hash = $derived(page.url.hash.slice(1));
	const linkedComment = $derived(hash.startsWith('review-comment-') ? reviewComments.find((c) => reviewAnchor(c.id) === hash) : undefined);
	const initialPath = $derived(page.url.searchParams.get('file') || (linkedComment && !linkedComment.outdated ? linkedComment.path : ''));
	const revealId = $derived(linkedComment && !linkedComment.outdated ? hash : '');

	// The selected file lives in the url so reloads and shared links land on it
	function onSelect(path: string) {
		if ((page.url.searchParams.get('file') ?? '') === path) return;
		const params = new SvelteURLSearchParams(page.url.searchParams);
		params.set('tab', 'files');
		if (path) params.set('file', path);
		else params.delete('file');
		replaceState(resolve(`/${namespace}/${name}/pulls/${number}?${params.toString()}`), {});
	}

	let currentFile = $state<DiffFile | undefined>();

	async function loadFile(path: string): Promise<DiffFile | undefined> {
		const resp = await rpcClient.pull.getPullRequestFile({ namespace, name, number, path, startSha: '', endSha: '' }, silentCallOptions);
		return resp.file;
	}

	// Old numbers of context lines map onto their new numbers for the file on screen
	const contextMap = $derived.by(() => {
		const map = new SvelteMap<string, number>();
		const f = currentFile;
		if (!f) return map;
		const path = pathOf(f);
		for (const h of f.hunks) {
			for (const l of h.lines) {
				if (l.kind === DiffLineKind.CONTEXT && l.oldNumber > 0) map.set(`${path}\n${l.oldNumber}`, l.newNumber);
			}
		}
		return map;
	});

	function anchor(path: string, side: DiffSide, line: number): { side: DiffSide; line: number } {
		if (side === 'old') {
			const mapped = contextMap.get(`${path}\n${line}`);
			if (mapped) return { side: 'new', line: mapped };
		}
		return { side, line };
	}

	// Threads anchored to a visible line, outdated ones listed apart
	const threads = $derived([...groupThreads(reviewComments).values()]);
	const inline = $derived.by(() => {
		const map = new SvelteMap<string, ReviewComment[][]>();
		for (const t of threads) {
			const root = t[0];
			if (root.outdated) continue;
			const at = anchor(root.path, root.side === 'old' ? 'old' : 'new', root.line);
			const key = threadKey(root.path, at.side, at.line);
			const list = map.get(key) ?? [];
			list.push(t);
			map.set(key, list);
		}
		return map;
	});
	const outdated = $derived(threads.filter((t) => t[0].outdated));
	const commentCounts = $derived.by(() => {
		const map = new SvelteMap<string, number>();
		for (const t of threads) map.set(t[0].path, (map.get(t[0].path) ?? 0) + t.length);
		return map;
	});
	let showOutdated = $state(false);

	// Shift click widens the draft to a range
	type Draft = { path: string; side: DiffSide; line: number; startLine: number };
	let draft = $state<Draft | null>(null);
	let draftBody = $state('');
	let submitting = $state(false);
	let shift = false;

	function onKey(e: KeyboardEvent) {
		shift = e.shiftKey;
	}

	function onLineClick(path: string, rawSide: DiffSide, rawLine: number) {
		if (!canComment) return;
		const { side, line } = anchor(path, rawSide, rawLine);
		if (shift && draft && draft.path === path && draft.side === side) {
			const lo = Math.min(draft.startLine, line);
			const hi = Math.max(draft.line, line);
			draft = { path, side, line: hi, startLine: lo };
			return;
		}
		if (draft && draft.path === path && draft.side === side && draft.line === line) return;
		draft = { path, side, line, startLine: line };
	}

	function cancelDraft() {
		draft = null;
		draftBody = '';
	}

	function setStart(v: string) {
		if (!draft) return;
		const n = Number(v);
		if (!Number.isFinite(n) || n < 1 || n > draft.line) return;
		draft = { ...draft, startLine: n };
	}

	async function submit(asReview: boolean) {
		if (!draft || !draftBody.trim() || submitting) return;
		submitting = true;
		const startLine = draft.startLine !== draft.line ? draft.startLine : 0;
		try {
			if (asReview && !pendingReview) {
				await rpcClient.pull.createReview({
					namespace,
					name,
					number,
					state: ReviewState.UNSPECIFIED,
					body: '',
					commitSha: headSha,
					comments: [{ path: draft.path, line: draft.line, side: draft.side, startLine, body: draftBody }]
				});
			} else {
				await rpcClient.pull.createReviewComment({
					namespace,
					name,
					number,
					reviewId: asReview ? (pendingReview?.id ?? '') : '',
					path: draft.path,
					line: draft.line,
					side: draft.side,
					startLine,
					body: draftBody,
					threadId: '',
					commitSha: headSha
				});
			}
			cancelDraft();
			onChanged();
		} catch {
			// Error interceptor already toasted
		} finally {
			submitting = false;
		}
	}

	// Viewed files remembered per head blob
	const viewedKey = $derived(`df-pr-viewed:${namespace}/${name}/${number}`);
	let viewed = $state<Record<string, string>>({});
	let viewedLoaded = '';
	$effect(() => {
		const key = viewedKey;
		if (viewedLoaded === key) return;
		viewedLoaded = key;
		try {
			viewed = JSON.parse(localStorage.getItem(key) || '{}');
		} catch {
			viewed = {};
		}
	});

	// Entries whose blob moved since they were checked drop out as their page loads
	function pruneViewed(files: DiffFile[]) {
		const next = { ...viewed };
		let changed = false;
		for (const f of files) {
			const path = pathOf(f);
			if (path in next && next[path] !== (f.newSha || f.oldSha)) {
				delete next[path];
				changed = true;
			}
		}
		if (!changed) return;
		viewed = next;
		localStorage.setItem(viewedKey, JSON.stringify(next));
	}

	function toggleViewed(f: DiffFile) {
		const path = pathOf(f);
		const sha = f.newSha || f.oldSha;
		const next = { ...viewed };
		if (next[path] === sha) delete next[path];
		else next[path] = sha;
		viewed = next;
		localStorage.setItem(viewedKey, JSON.stringify(next));
	}
</script>

<svelte:window onkeydown={onKey} onkeyup={onKey} />

{#snippet lineExtra({ path, side, line }: { path: string; side: DiffSide; line: number })}
	{@const list = inline.get(threadKey(path, side, line))}
	{#if list || (draft && draft.path === path && draft.side === side && draft.line === line)}
		<div class="font-sans text-[13px] leading-normal whitespace-normal px-3 py-1 bg-muted/20 border-y border-border/40">
			{#if list}
				{#each list as thread (thread[0].id)}
					<ReviewThread
						{namespace}
						{name}
						{number}
						comments={thread}
						{canComment}
						{canModerate}
						{canApply}
						pendingReviewId={pendingReview?.id ?? ''}
						{onChanged}
					/>
				{/each}
			{/if}
			{#if draft && draft.path === path && draft.side === side && draft.line === line}
				<div class="my-1.5 space-y-1.5">
					<p class="text-[12px] text-muted-foreground flex flex-wrap items-center gap-2">
						Commenting on
						{#if draft.startLine !== draft.line}
							lines
							<Input type="number" min="1" max={draft.line} value={draft.startLine} class="w-20 h-8 tabular-nums" aria-label="First line" onchange={(e) => setStart((e.currentTarget as HTMLInputElement).value)} />
							to {draft.line}
						{:else}
							line {draft.line}
						{/if}
						<span class="text-muted-foreground/70">({side === 'old' ? 'old' : 'new'} side, shift click another line for a range)</span>
					</p>
					<CommentBox
						bind:value={draftBody}
						placeholder="Leave a comment, use a suggestion fence to propose code..."
						submitLabel={pendingReview ? 'Add review comment' : 'Start a review'}
						{submitting}
						autofocus
						onSubmit={() => submit(true)}
					>
						{#snippet extra()}
							<Button variant="outline" size="sm" class="h-8" onclick={() => submit(false)} disabled={submitting || !draftBody.trim()}>Add single comment</Button>
							<Button variant="ghost" size="sm" class="h-8" onclick={cancelDraft} disabled={submitting}>Cancel</Button>
						{/snippet}
					</CommentBox>
				</div>
			{/if}
		</div>
	{/if}
{/snippet}

{#snippet toolbar()}
	{#if pendingCount > 0}
		<Badge variant="outline" class="text-[10px] h-4.5 px-1.5 border-amber-500/40 text-amber-600 dark:text-amber-400">
			{pendingCount} pending
		</Badge>
	{/if}
	{#if canReview}
		<ReviewForm {namespace} {name} {number} {pendingReview} {pendingCount} {headSha} {isAuthor} onSubmitted={onChanged} />
	{/if}
{/snippet}

<div class="space-y-4">
	<DiffViewer
		{namespace}
		{name}
		head={headSha}
		base={pull.mergeBaseSha}
		resetKey="{number}:{headSha}"
		listFiles={async (p) => {
			const resp = await rpcClient.pull.getPullRequestDiff({ namespace, name, number, startSha: '', endSha: '', page: p }, silentCallOptions);
			pruneViewed(resp.diff?.files ?? []);
			return { files: resp.diff?.files ?? [], page: resp.page, totalFiles: resp.diff?.totalFiles ?? 0, additions: resp.diff?.additions ?? 0, deletions: resp.diff?.deletions ?? 0 };
		}}
		{loadFile}
		{initialPath}
		{revealId}
		{onSelect}
		onFile={(f) => (currentFile = f)}
		onLineClick={canComment ? onLineClick : undefined}
		{lineExtra}
		{viewed}
		onToggleViewed={toggleViewed}
		{commentCounts}
		{toolbar}
	/>

	{#if outdated.length > 0}
		<section class="rounded-xl border border-border/60 bg-card overflow-hidden">
			<button type="button" class="w-full flex items-center gap-2 px-4 py-2.5 text-left text-[13px] font-medium hover:bg-muted/30" onclick={() => (showOutdated = !showOutdated)}>
				{#if showOutdated}<ChevronDown class="h-4 w-4 text-muted-foreground" />{:else}<ChevronRight class="h-4 w-4 text-muted-foreground" />{/if}
				<History class="h-4 w-4 text-muted-foreground" />
				Outdated comments
				<span class="text-muted-foreground tabular-nums">{outdated.length}</span>
			</button>
			{#if showOutdated}
				<div class="px-4 py-2 border-t border-border/50 space-y-1">
					{#each outdated as thread (thread[0].id)}
						{#if thread[0].diffHunk}
							<pre class="mt-2 rounded-md border border-border/60 bg-muted/20 px-3 py-2 font-mono text-[11.5px] whitespace-pre-wrap break-all text-muted-foreground">{thread[0].diffHunk}</pre>
						{/if}
						<ReviewThread
							{namespace}
							{name}
							{number}
							comments={thread}
							{canComment}
							{canModerate}
							canApply={false}
							pendingReviewId={pendingReview?.id ?? ''}
							showPath
							{onChanged}
						/>
					{/each}
				</div>
			{/if}
		</section>
	{/if}
</div>
