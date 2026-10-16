<script lang="ts">
	import { resolve } from '$app/paths';
	import type { Component } from 'svelte';
	import { SvelteMap } from 'svelte/reactivity';
	import {
		Tag, Users, Milestone as MilestoneIcon, CircleCheck, CircleDot, Link2, Pencil, Lock, Unlock, Pin, PinOff,
		CalendarDays, Blocks, Clock, Eye, EyeOff, Upload, Replace, GitMerge, Zap, GitPullRequestDraft, GitPullRequest,
		GitBranch, SquareKanban, MessageSquare, Trash2, MoreHorizontal, Mail, CircleSlash, History
	} from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from '$lib/components/ui/dropdown-menu';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import Markdown from '$lib/components/git/markdown.svelte';
	import LabelChip from '$lib/components/git/label-chip.svelte';
	import CommentBox from '$lib/components/git/comment-box.svelte';
	import ReactionBar from '$lib/components/git/reaction-bar.svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { CommentType, type Comment, type IssueUser } from '$lib/proto/distroface/v1/issue_pb';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	let {
		namespace,
		name,
		number,
		items,
		canComment,
		canModerate,
		loading = false,
		onChanged,
		knownUsers = []
	}: {
		namespace: string;
		name: string;
		number: number;
		items: Comment[];
		canComment: boolean;
		canModerate: boolean;
		loading?: boolean;
		onChanged?: () => void;
		// Username hints for events carrying only user ids
		knownUsers?: IssueUser[];
	} = $props();

	type Payload = Record<string, unknown>;

	function payload(c: Comment): Payload {
		try {
			const v = JSON.parse(c.payload || '{}');
			return v && typeof v === 'object' ? (v as Payload) : {};
		} catch {
			return {};
		}
	}

	const str = (p: Payload, k: string) => (p[k] === undefined || p[k] === null ? '' : String(p[k]));
	const num = (p: Payload, k: string) => Number(p[k] ?? 0);

	// Usernames from every author on the page plus the hints
	const userNames = $derived.by(() => {
		const m = new SvelteMap<string, string>();
		for (const u of knownUsers) if (u.id) m.set(u.id, u.username);
		for (const c of items) if (c.author?.id) m.set(c.author.id, c.author.username);
		if (authStore.user) m.set(authStore.user.id, authStore.user.username);
		return m;
	});
	const userName = (id: string) => userNames.get(id) ?? 'a user';

	function when(c: Comment): { rel: string; abs: string } {
		if (!c.createdAt) return { rel: '', abs: '' };
		const d = timestampDate(c.createdAt);
		return { rel: relativeTime(d), abs: d.toLocaleString() };
	}

	function duration(seconds: number): string {
		const h = Math.floor(seconds / 3600);
		const m = Math.floor((seconds % 3600) / 60);
		if (h && m) return `${h}h ${m}m`;
		if (h) return `${h}h`;
		if (m) return `${m}m`;
		return `${seconds}s`;
	}

	const isSha = (s: string) => /^[0-9a-f]{7,64}$/i.test(s);
	const short = (s: string) => s.slice(0, 10);

	function reviewText(state: string): string {
		switch (state) {
			case 'approved':
				return 'approved these changes';
			case 'changes_requested':
				return 'requested changes';
			default:
				return 'reviewed';
		}
	}

	function iconFor(c: Comment): { icon: Component<{ class?: string }>; tone: string } {
		const p = payload(c);
		switch (c.type) {
			case CommentType.LABELED:
			case CommentType.UNLABELED:
				return { icon: Tag, tone: '' };
			case CommentType.ASSIGNED:
			case CommentType.UNASSIGNED:
				return { icon: Users, tone: '' };
			case CommentType.MILESTONED:
			case CommentType.DEMILESTONED:
				return { icon: MilestoneIcon, tone: '' };
			case CommentType.CLOSED:
				return { icon: CircleCheck, tone: 'bg-purple-500/15 text-purple-600 dark:text-purple-400' };
			case CommentType.REOPENED:
				return { icon: CircleDot, tone: 'bg-success/15 text-success' };
			case CommentType.REFERENCED:
				return { icon: p.branch && !p.sha ? GitBranch : Link2, tone: '' };
			case CommentType.RENAMED:
				return { icon: Pencil, tone: '' };
			case CommentType.LOCKED:
				return { icon: Lock, tone: '' };
			case CommentType.UNLOCKED:
				return { icon: Unlock, tone: '' };
			case CommentType.PINNED:
				return { icon: Pin, tone: '' };
			case CommentType.UNPINNED:
				return { icon: PinOff, tone: '' };
			case CommentType.DUE_DATE:
				return { icon: CalendarDays, tone: '' };
			case CommentType.DEPENDENCY_ADDED:
			case CommentType.DEPENDENCY_REMOVED:
				return { icon: Blocks, tone: '' };
			case CommentType.BLOCKER_CLOSED:
				return { icon: CircleCheck, tone: 'bg-purple-500/15 text-purple-600 dark:text-purple-400' };
			case CommentType.BLOCKER_REOPENED:
				return { icon: CircleDot, tone: 'bg-amber-500/15 text-amber-600 dark:text-amber-400' };
			case CommentType.TIME_TRACKED:
				return { icon: Clock, tone: '' };
			case CommentType.REVIEW_REQUESTED:
				return { icon: Eye, tone: '' };
			case CommentType.REVIEW_REQUEST_REMOVED:
				return { icon: EyeOff, tone: '' };
			case CommentType.REVIEW:
				return str(p, 'state') === 'approved'
					? { icon: CircleCheck, tone: 'bg-success/15 text-success' }
					: str(p, 'state') === 'changes_requested'
						? { icon: CircleSlash, tone: 'bg-destructive/15 text-destructive' }
						: { icon: Eye, tone: '' };
			case CommentType.REVIEW_DISMISSED:
				return { icon: EyeOff, tone: '' };
			case CommentType.HEAD_PUSHED:
				return { icon: Upload, tone: '' };
			case CommentType.FORCE_PUSHED:
				return { icon: Replace, tone: 'bg-amber-500/15 text-amber-600 dark:text-amber-400' };
			case CommentType.MERGED:
				return { icon: GitMerge, tone: 'bg-purple-500/15 text-purple-600 dark:text-purple-400' };
			case CommentType.AUTO_MERGE_ARMED:
			case CommentType.AUTO_MERGE_CANCELLED:
				return { icon: Zap, tone: '' };
			case CommentType.CONVERTED_TO_DRAFT:
				return { icon: GitPullRequestDraft, tone: '' };
			case CommentType.READY_FOR_REVIEW:
				return { icon: GitPullRequest, tone: 'bg-success/15 text-success' };
			case CommentType.BASE_CHANGED:
				return { icon: GitBranch, tone: '' };
			case CommentType.PROJECT_CHANGED:
				return { icon: SquareKanban, tone: '' };
			default:
				return { icon: History, tone: '' };
		}
	}

	function typeName(c: Comment): string {
		return (CommentType[c.type] ?? 'event').toLowerCase().replace(/_/g, ' ');
	}

	const viewerId = $derived(authStore.user?.id ?? '');
	const canEdit = (c: Comment) => canModerate || (!!viewerId && c.author?.id === viewerId);

	let editingId = $state('');
	let editBody = $state('');
	let saving = $state(false);

	function startEdit(c: Comment) {
		editingId = c.id;
		editBody = c.body;
	}

	async function saveEdit() {
		if (!editingId || saving) return;
		saving = true;
		try {
			await rpcClient.issue.updateComment({ namespace, name, id: editingId, body: editBody });
			editingId = '';
			onChanged?.();
		} catch {
			// Error interceptor already toasted
		} finally {
			saving = false;
		}
	}

	let deleteOpen = $state(false);
	let deleteTarget = $state('');
	let deleting = $state(false);

	async function confirmDelete() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await rpcClient.issue.deleteComment({ namespace, name, id: deleteTarget });
			toast.success('Comment deleted');
			deleteOpen = false;
			onChanged?.();
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}

	function copyLink(c: Comment) {
		const url = `${location.origin}${resolve('/[namespace]/[name]/issues/[number]', { namespace, name, number: String(number) })}#comment-${c.id}`;
		navigator.clipboard?.writeText(url).then(() => toast.success('Link copied'));
	}
</script>

{#snippet actor(c: Comment)}
	{@const u = c.author?.username || 'system'}
	<a href={resolve('/[namespace]', { namespace: u })} class="font-medium text-foreground hover:text-primary">{u}</a>
{/snippet}

{#snippet userLink(id: string)}
	<a href={resolve('/[namespace]', { namespace: userName(id) })} class="font-medium text-foreground hover:text-primary">{userName(id)}</a>
{/snippet}

{#snippet issueLink(repo: string, n: string, pull: boolean, title: string)}
	{@const [ns, nm] = (repo || `${namespace}/${name}`).split('/')}
	{@const params = { namespace: ns, name: nm, number: n }}
	{#if pull}
		<a href={resolve('/[namespace]/[name]/pulls/[number]', params)} class="font-medium text-foreground hover:text-primary">
			{#if repo && repo !== `${namespace}/${name}`}{repo}{/if}#{n}
		</a>
	{:else}
		<a href={resolve('/[namespace]/[name]/issues/[number]', params)} class="font-medium text-foreground hover:text-primary">
			{#if repo && repo !== `${namespace}/${name}`}{repo}{/if}#{n}
		</a>
	{/if}
	{#if title}<span class="text-foreground/80">{title}</span>{/if}
{/snippet}

{#snippet shaLink(repo: string, sha: string)}
	{@const [ns, nm] = (repo || `${namespace}/${name}`).split('/')}
	<a href={resolve('/[namespace]/[name]/commit/[sha]', { namespace: ns, name: nm, sha })} class="font-mono text-[12px] text-foreground hover:text-primary">{short(sha)}</a>
{/snippet}

{#snippet event(c: Comment)}
	{@const p = payload(c)}
	{@const { icon: Icon, tone } = iconFor(c)}
	{@const t = when(c)}
	<div class="flex items-start gap-3 py-1.5 pl-1" id="event-{c.id}">
		<span class="h-7 w-7 shrink-0 rounded-full inline-flex items-center justify-center {tone || 'bg-muted text-muted-foreground'}">
			<Icon class="h-3.5 w-3.5" />
		</span>
		<div class="min-w-0 flex-1 text-[13px] text-muted-foreground pt-1 leading-relaxed">
			{@render actor(c)}
			{#if c.type === CommentType.LABELED}
				added the <LabelChip name={str(p, 'name')} color={str(p, 'color')} size="sm" /> label
			{:else if c.type === CommentType.UNLABELED}
				removed the <LabelChip name={str(p, 'name')} color={str(p, 'color')} size="sm" /> label
			{:else if c.type === CommentType.ASSIGNED}
				{#if str(p, 'user_id') === c.author?.id}self-assigned this{:else}assigned {@render userLink(str(p, 'user_id'))}{/if}
			{:else if c.type === CommentType.UNASSIGNED}
				{#if str(p, 'user_id') === c.author?.id}removed their assignment{:else}unassigned {@render userLink(str(p, 'user_id'))}{/if}
			{:else if c.type === CommentType.MILESTONED}
				added this to the <span class="font-medium text-foreground">{str(p, 'title')}</span> milestone
			{:else if c.type === CommentType.DEMILESTONED}
				removed this from the <span class="font-medium text-foreground">{str(p, 'title')}</span> milestone
			{:else if c.type === CommentType.CLOSED}
				closed this
				{#if str(p, 'via')}
					{#if isSha(str(p, 'via'))}in commit {@render shaLink('', str(p, 'via'))}{:else}via {str(p, 'via')}{/if}
				{/if}
			{:else if c.type === CommentType.REOPENED}
				reopened this
			{:else if c.type === CommentType.REFERENCED}
				{#if str(p, 'sha')}
					referenced this in commit {@render shaLink(str(p, 'repo'), str(p, 'sha'))}
					{#if str(p, 'branch')}on <span class="font-mono text-[12px]">{str(p, 'branch')}</span>{/if}
				{:else if p.number !== undefined}
					referenced this from {@render issueLink(str(p, 'repo'), str(p, 'number'), p.is_pull === true, str(p, 'title'))}
				{:else if str(p, 'branch')}
					deleted the head branch <span class="font-mono text-[12px] text-foreground">{str(p, 'branch')}</span>
				{:else}
					referenced this
				{/if}
			{:else if c.type === CommentType.RENAMED}
				changed the title <span class="line-through text-foreground/70">{str(p, 'from')}</span> <span class="font-medium text-foreground">{str(p, 'to')}</span>
			{:else if c.type === CommentType.LOCKED}
				locked this conversation{#if str(p, 'reason')}&#32;as <span class="font-medium text-foreground">{str(p, 'reason')}</span>{/if}
			{:else if c.type === CommentType.UNLOCKED}
				unlocked this conversation
			{:else if c.type === CommentType.PINNED}
				pinned this issue
			{:else if c.type === CommentType.UNPINNED}
				unpinned this issue
			{:else if c.type === CommentType.DUE_DATE}
				{#if str(p, 'due')}set the due date to <span class="font-medium text-foreground">{new Date(str(p, 'due')).toLocaleDateString()}</span>{:else}removed the due date{/if}
			{:else if c.type === CommentType.DEPENDENCY_ADDED}
				{#if p.blocking === true}
					marked this as blocking {@render issueLink(str(p, 'repo'), str(p, 'number'), p.is_pull === true, str(p, 'title'))}
				{:else}
					marked this as blocked by {@render issueLink(str(p, 'repo'), str(p, 'number'), p.is_pull === true, str(p, 'title'))}
				{/if}
			{:else if c.type === CommentType.DEPENDENCY_REMOVED}
				removed {@render issueLink(str(p, 'repo'), str(p, 'number'), p.is_pull === true, str(p, 'title'))} as a blocker
			{:else if c.type === CommentType.BLOCKER_CLOSED}
				closed {@render issueLink(str(p, 'repo'), str(p, 'number'), p.is_pull === true, str(p, 'title'))}, which was blocking this
			{:else if c.type === CommentType.BLOCKER_REOPENED}
				reopened {@render issueLink(str(p, 'repo'), str(p, 'number'), p.is_pull === true, str(p, 'title'))}, which blocks this again
			{:else if c.type === CommentType.TIME_TRACKED}
				tracked <span class="font-medium text-foreground tabular-nums">{duration(num(p, 'seconds'))}</span>{#if str(p, 'note')}, {str(p, 'note')}{/if}
			{:else if c.type === CommentType.REVIEW_REQUESTED}
				requested a review from <span class="font-medium text-foreground">{str(p, 'team') || userName(str(p, 'user_id'))}</span>
			{:else if c.type === CommentType.REVIEW_REQUEST_REMOVED}
				removed the review request for <span class="font-medium text-foreground">{str(p, 'team') || (str(p, 'team_id') ? 'a team' : userName(str(p, 'user_id')))}</span>
			{:else if c.type === CommentType.REVIEW}
				<a href="#review-{c.reviewId || str(p, 'review_id')}" class="hover:text-primary">{reviewText(str(p, 'state'))}</a>
				{#if str(p, 'commit_sha')}at {@render shaLink('', str(p, 'commit_sha'))}{/if}
			{:else if c.type === CommentType.REVIEW_DISMISSED}
				{#if p.stale === true}
					dismissed {num(p, 'count')} stale review{num(p, 'count') === 1 ? '' : 's'}
				{:else}
					dismissed <a href="#review-{str(p, 'review_id')}" class="hover:text-primary">a review</a>{#if str(p, 'message')}, {str(p, 'message')}{/if}
				{/if}
			{:else if c.type === CommentType.HEAD_PUSHED}
				pushed {num(p, 'commits')} commit{num(p, 'commits') === 1 ? '' : 's'}
				{#if str(p, 'before') && str(p, 'after')}
					<a href={resolve('/[namespace]/[name]/compare/[range]', { namespace, name, range: `${str(p, 'before')}...${str(p, 'after')}` })} class="font-mono text-[12px] text-foreground hover:text-primary">{short(str(p, 'before'))}..{short(str(p, 'after'))}</a>
				{/if}
			{:else if c.type === CommentType.FORCE_PUSHED}
				force pushed from {@render shaLink('', str(p, 'before'))} to {@render shaLink('', str(p, 'after'))}
			{:else if c.type === CommentType.MERGED}
				merged commit {@render shaLink('', str(p, 'sha'))} into <span class="font-mono text-[12px] text-foreground">{str(p, 'base')}</span>{#if str(p, 'method')} by {str(p, 'method').replace(/_/g, ' ')}{/if}
			{:else if c.type === CommentType.AUTO_MERGE_ARMED}
				enabled auto merge{#if str(p, 'method')} ({str(p, 'method').replace(/_/g, ' ')}){/if}
			{:else if c.type === CommentType.AUTO_MERGE_CANCELLED}
				disabled auto merge
			{:else if c.type === CommentType.CONVERTED_TO_DRAFT}
				converted this to a draft
			{:else if c.type === CommentType.READY_FOR_REVIEW}
				marked this as ready for review
			{:else if c.type === CommentType.BASE_CHANGED}
				changed the base branch from <span class="font-mono text-[12px] text-foreground">{str(p, 'from')}</span> to <span class="font-mono text-[12px] text-foreground">{str(p, 'to')}</span>
			{:else if c.type === CommentType.PROJECT_CHANGED}
				{#if str(p, 'column')}
					moved this to <span class="font-medium text-foreground">{str(p, 'column')}</span> on a project board
				{:else}
					changed the project <span class="font-medium text-foreground">{str(p, 'title')}</span>
				{/if}
			{:else}
				{typeName(c)}
			{/if}
			{#if t.rel}
				<span class="mx-1 text-muted-foreground/60">/</span><span title={t.abs}>{t.rel}</span>
			{/if}
		</div>
	</div>
{/snippet}

{#snippet card(c: Comment)}
	{@const t = when(c)}
	{@const u = c.author?.username || 'system'}
	<div id="comment-{c.id}" class="rounded-xl border border-border/60 bg-card overflow-hidden scroll-mt-20">
		<div class="flex items-center gap-2 px-4 py-2 border-b border-border/50 bg-muted/20 text-[12.5px] text-muted-foreground">
			<span class="h-5 w-5 rounded-full bg-primary/10 text-primary text-[10px] font-semibold inline-flex items-center justify-center shrink-0">{u.charAt(0).toUpperCase()}</span>
			<a href={resolve('/[namespace]', { namespace: u })} class="font-medium text-foreground hover:text-primary">{u}</a>
			<span>commented</span>
			{#if t.rel}<span title={t.abs}>{t.rel}</span>{/if}
			{#if c.edited}<span class="text-muted-foreground/70" title="This comment was edited">(edited)</span>{/if}
			{#if c.viaEmail}<span class="inline-flex items-center gap-1 text-muted-foreground/70" title="Posted from an email reply"><Mail class="h-3 w-3" />via email</span>{/if}
			<div class="ml-auto">
				<DropdownMenu>
					<DropdownMenuTrigger>
						{#snippet child({ props })}
							<Button {...props} variant="ghost" size="icon" class="h-6 w-6" aria-label="Comment actions">
								<MoreHorizontal class="h-3.5 w-3.5" />
							</Button>
						{/snippet}
					</DropdownMenuTrigger>
					<DropdownMenuContent align="end" class="w-40">
						<DropdownMenuItem onclick={() => copyLink(c)}><Link2 class="h-3.5 w-3.5 mr-2" />Copy link</DropdownMenuItem>
						{#if canEdit(c)}
							<DropdownMenuItem onclick={() => startEdit(c)}><Pencil class="h-3.5 w-3.5 mr-2" />Edit</DropdownMenuItem>
							<DropdownMenuItem class="text-destructive" onclick={() => { deleteTarget = c.id; deleteOpen = true; }}><Trash2 class="h-3.5 w-3.5 mr-2" />Delete</DropdownMenuItem>
						{/if}
					</DropdownMenuContent>
				</DropdownMenu>
			</div>
		</div>
		<div class="px-4 py-3 text-sm">
			{#if editingId === c.id}
				<CommentBox bind:value={editBody} submitLabel="Update comment" submitting={saving} onSubmit={saveEdit} autofocus>
					{#snippet extra()}
						<Button variant="outline" size="sm" class="h-8" onclick={() => (editingId = '')}>Cancel</Button>
					{/snippet}
				</CommentBox>
			{:else if c.body.trim()}
				<Markdown source={c.body} />
			{:else}
				<p class="text-muted-foreground italic">No description provided.</p>
			{/if}
		</div>
		{#if editingId !== c.id && (c.reactions.some((r) => r.count > 0) || canComment)}
			<div class="px-4 pb-3">
				<ReactionBar {namespace} {name} {number} commentId={c.id} reactions={c.reactions} canReact={canComment} {onChanged} />
			</div>
		{/if}
	</div>
{/snippet}

<div class="space-y-3">
	{#each items as c (c.id)}
		{#if c.type === CommentType.COMMENT}
			{@render card(c)}
		{:else}
			{@render event(c)}
		{/if}
	{/each}
	{#if loading}
		<div class="space-y-3">
			<Skeleton class="h-6 w-2/3" />
			<Skeleton class="h-24 w-full rounded-xl" />
		</div>
	{:else if items.length === 0}
		<div class="flex items-center gap-2 text-[13px] text-muted-foreground py-2 pl-1">
			<MessageSquare class="h-4 w-4" />No activity yet
		</div>
	{/if}
</div>

<ConfirmDialog bind:open={deleteOpen} title="Delete comment" confirmLabel="Delete" onConfirm={confirmDelete} loading={deleting} icon={Trash2}>
	{#snippet description()}
		This removes the comment and its reactions permanently.
	{/snippet}
</ConfirmDialog>
