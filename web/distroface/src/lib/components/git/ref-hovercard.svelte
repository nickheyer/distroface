<script lang="ts">
	import { resolve } from '$app/paths';
	import {
		CircleDot,
		CheckCircle,
		GitPullRequest,
		GitPullRequestDraft,
		GitMerge,
		GitCommit,
		GitBranch,
		Tag,
		Calendar,
		CheckCircle as CheckCircle2,
		XCircle,
		Clock
	} from '$lib/icons';
	import UserAvatar from '$lib/components/user-avatar.svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import LabelChip from '$lib/components/git/label-chip.svelte';
	import { rpcClient, backgroundCallOptions } from '$lib/api/rpc-client';
	import type { User } from '$lib/proto/distroface/v1/types_pb';
	import { IssueState, type Issue } from '$lib/proto/distroface/v1/issue_pb';
	import { CommitStatusState, type GetCommitResponse } from '$lib/proto/distroface/v1/git_pb';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { SvelteMap } from 'svelte/reactivity';

	type Target =
		| { kind: 'user'; key: string; user: string }
		| { kind: 'issue'; key: string; repo: string; number: string }
		| { kind: 'commit'; key: string; repo: string; sha: string };
	type Card =
		| { kind: 'user'; user: User }
		| { kind: 'issue'; issue: Issue }
		| { kind: 'commit'; repo: string; commit: GetCommitResponse }
		| { kind: 'missing' };

	const SHOW_MS = 350;
	const HIDE_MS = 200;
	const WIDTH = 320;
	const cache = new SvelteMap<string, Card>();

	let target = $state<Target | undefined>();
	let card = $state<Card | undefined>();
	let anchor = $state({ top: 0, bottom: 0, left: 0 });
	let box = $state<HTMLDivElement | undefined>();
	let height = $state(0);
	let link: HTMLAnchorElement | null = null;
	let showTimer: ReturnType<typeof setTimeout> | undefined;
	let hideTimer: ReturnType<typeof setTimeout> | undefined;

	const style = $derived.by(() => {
		const h = height || 120;
		const below = anchor.bottom + 6;
		const top = below + h > window.innerHeight - 8 ? Math.max(8, anchor.top - 6 - h) : below;
		const left = Math.max(8, Math.min(anchor.left, window.innerWidth - WIDTH - 8));
		return `top:${top}px;left:${left}px;width:${WIDTH}px`;
	});

	// Rendered links say what they point at through data attributes
	function targetOf(el: HTMLAnchorElement): Target | undefined {
		const d = el.dataset;
		switch (d.hover) {
			case 'user':
				return d.user
					? { kind: 'user', key: `user:${d.user.toLowerCase()}`, user: d.user }
					: undefined;
			case 'issue':
				return d.repo && d.number
					? {
							kind: 'issue',
							key: `issue:${d.repo.toLowerCase()}#${d.number}`,
							repo: d.repo,
							number: d.number
						}
					: undefined;
			case 'commit':
				return d.repo && d.sha
					? {
							kind: 'commit',
							key: `commit:${d.repo.toLowerCase()}@${d.sha}`,
							repo: d.repo,
							sha: d.sha
						}
					: undefined;
		}
		return undefined;
	}

	async function fetchCard(t: Target): Promise<Card> {
		try {
			switch (t.kind) {
				case 'user': {
					const r = await rpcClient.user.getUser({ username: t.user }, backgroundCallOptions);
					return r.user ? { kind: 'user', user: r.user } : { kind: 'missing' };
				}
				case 'issue': {
					const [namespace, name] = t.repo.split('/');
					const r = await rpcClient.issue.getIssue(
						{ namespace, name, number: BigInt(t.number) },
						backgroundCallOptions
					);
					return r.issue ? { kind: 'issue', issue: r.issue } : { kind: 'missing' };
				}
				case 'commit': {
					const [namespace, name] = t.repo.split('/');
					const r = await rpcClient.gitFiles.getCommit(
						{ namespace, name, sha: t.sha },
						backgroundCallOptions
					);
					return r.commit ? { kind: 'commit', repo: t.repo, commit: r } : { kind: 'missing' };
				}
			}
		} catch {
			return { kind: 'missing' };
		}
	}

	function show(el: HTMLAnchorElement) {
		const t = targetOf(el);
		if (!t) return;
		const r = el.getBoundingClientRect();
		anchor = { top: r.top, bottom: r.bottom, left: r.left };
		target = t;
		card = cache.get(t.key);
		if (card) return;
		void fetchCard(t).then((c) => {
			if (c.kind !== 'missing') cache.set(t.key, c);
			if (target?.key === t.key) card = c;
		});
	}

	function hide() {
		clearTimeout(showTimer);
		clearTimeout(hideTimer);
		target = undefined;
		card = undefined;
		link = null;
	}

	function scheduleHide() {
		clearTimeout(hideTimer);
		hideTimer = setTimeout(hide, HIDE_MS);
	}

	function linkOf(e: Event): HTMLAnchorElement | null {
		const el = e.target instanceof Element ? e.target.closest('a[data-hover]') : null;
		return el instanceof HTMLAnchorElement ? el : null;
	}

	function onOver(e: MouseEvent) {
		const el = linkOf(e);
		if (!el) return;
		clearTimeout(hideTimer);
		if (el === link) return;
		link = el;
		clearTimeout(showTimer);
		showTimer = setTimeout(() => {
			if (link === el) show(el);
		}, SHOW_MS);
	}

	function onOut(e: MouseEvent) {
		const el = linkOf(e);
		if (!el || el !== link) return;
		if (e.relatedTarget instanceof Node && el.contains(e.relatedTarget)) return;
		clearTimeout(showTimer);
		link = null;
		if (target) scheduleHide();
	}

	$effect(() => {
		const esc = (e: KeyboardEvent) => {
			if (e.key === 'Escape') hide();
		};
		document.addEventListener('mouseover', onOver);
		document.addEventListener('mouseout', onOut);
		document.addEventListener('keydown', esc);
		window.addEventListener('scroll', hide, true);
		return () => {
			document.removeEventListener('mouseover', onOver);
			document.removeEventListener('mouseout', onOut);
			document.removeEventListener('keydown', esc);
			window.removeEventListener('scroll', hide, true);
			clearTimeout(showTimer);
			clearTimeout(hideTimer);
		};
	});

	$effect(() => {
		void card;
		height = box?.offsetHeight ?? 0;
	});

	function issueIcon(i: Issue) {
		if (i.isPull) {
			if (i.pull?.merged) return GitMerge;
			if (i.pull?.draft) return GitPullRequestDraft;
			return GitPullRequest;
		}
		return i.state === IssueState.CLOSED ? CheckCircle : CircleDot;
	}

	function issueTone(i: Issue): string {
		if (i.isPull) {
			if (i.pull?.merged) return 'text-done';
			if (i.pull?.draft) return 'text-muted-foreground';
			return i.state === IssueState.CLOSED ? 'text-destructive' : 'text-success';
		}
		return i.state === IssueState.CLOSED ? 'text-done' : 'text-success';
	}

	function issueParams(i: Issue): { namespace: string; name: string; number: string } {
		const [namespace, name] = i.repoFullName.split('/');
		return { namespace, name, number: String(i.number) };
	}

	// First lines of a body as plain text
	function excerpt(body: string): string {
		const text = body
			.replace(/```[\s\S]*?```/g, ' ')
			.replace(/!\[[^\]]*\]\([^)]*\)/g, ' ')
			.replace(/\[([^\]]*)\]\([^)]*\)/g, '$1')
			.replace(/[#>*_`~]+/g, '')
			.replace(/\s+/g, ' ')
			.trim();
		return text.length > 180 ? `${text.slice(0, 180).trimEnd()}...` : text;
	}

	// Repository and the day it was opened
	function origin(i: Issue): string {
		return i.createdAt
			? `${i.repoFullName} on ${timestampDate(i.createdAt).toLocaleDateString()}`
			: i.repoFullName;
	}

	function title(message: string): string {
		return message.split('\n')[0].trim();
	}

	function statusIcon(s: CommitStatusState) {
		switch (s) {
			case CommitStatusState.SUCCESS:
				return { icon: CheckCircle2, tone: 'text-success' };
			case CommitStatusState.FAILURE:
			case CommitStatusState.ERROR:
				return { icon: XCircle, tone: 'text-destructive' };
			case CommitStatusState.PENDING:
				return { icon: Clock, tone: 'text-warning' };
			default:
				return undefined;
		}
	}
</script>

{#snippet issueTitle(i: Issue)}
	{@const Icon = issueIcon(i)}
	<Icon class="mt-0.5 size-4 shrink-0 {issueTone(i)}" />
	<span class="min-w-0 wrap-break-word"
		>{i.title} <span class="font-normal text-muted-foreground">#{i.number}</span></span
	>
{/snippet}

{#if target}
	<div
		bind:this={box}
		class="fixed z-50 rounded-lg border bg-popover p-3 text-sm text-popover-foreground shadow-lg dark:ring-1 dark:ring-white/5"
		{style}
		role="tooltip"
		onmouseenter={() => clearTimeout(hideTimer)}
		onmouseleave={scheduleHide}
	>
		{#if !card}
			<div class="space-y-2">
				<Skeleton class="h-4 w-2/3" />
				<Skeleton class="h-3 w-full" />
				<Skeleton class="h-3 w-1/2" />
			</div>
		{:else if card.kind === 'missing'}
			<p class="text-xs text-muted-foreground">Not found, or not visible to you</p>
		{:else if card.kind === 'user'}
			{@const u = card.user}
			<div class="flex items-start gap-3">
				<UserAvatar
					username={u.username}
					displayName={u.displayName}
					avatarUrl={u.avatarUrl}
					class="size-10"
				/>
				<div class="min-w-0 flex-1">
					<a
						href={resolve('/[namespace]', { namespace: u.username })}
						class="block truncate font-semibold hover:text-primary">{u.displayName || u.username}</a
					>
					<p class="truncate text-xs text-muted-foreground">@{u.username}</p>
					{#if u.createdAt}
						<p class="mt-1.5 flex items-center gap-1 text-xs text-muted-foreground">
							<Calendar class="size-3" />Joined {relativeTime(timestampDate(u.createdAt))}
						</p>
					{/if}
				</div>
			</div>
		{:else if card.kind === 'issue'}
			{@const i = card.issue}
			{@const body = excerpt(i.body)}
			<p class="truncate text-xs text-muted-foreground">{origin(i)}</p>
			{#if i.isPull}
				<a
					href={resolve('/[namespace]/[name]/pulls/[number]', issueParams(i))}
					class="mt-1 flex items-start gap-1.5 font-semibold hover:text-primary"
					>{@render issueTitle(i)}</a
				>
			{:else}
				<a
					href={resolve('/[namespace]/[name]/issues/[number]', issueParams(i))}
					class="mt-1 flex items-start gap-1.5 font-semibold hover:text-primary"
					>{@render issueTitle(i)}</a
				>
			{/if}
			{#if body}
				<p class="mt-1 line-clamp-2 text-xs text-muted-foreground">{body}</p>
			{/if}
			{#if i.author || i.pull}
				<p class="mt-1.5 truncate text-xs text-muted-foreground">
					{#if i.pull}
						<span class="font-mono">{i.pull.headBranch}</span> into
						<span class="font-mono">{i.pull.baseBranch}</span>{#if i.author}, by {i.author
								.username}{/if}
					{:else if i.author}
						Opened by {i.author.username}
					{/if}
				</p>
			{/if}
			{#if i.labels.length}
				<div class="mt-2 flex flex-wrap gap-1">
					{#each i.labels.slice(0, 6) as l (l.id)}
						<LabelChip name={l.name} color={l.color} size="sm" />
					{/each}
				</div>
			{/if}
		{:else if card.kind === 'commit' && card.commit.commit}
			{@const c = card.commit.commit}
			{@const [namespace, name] = card.repo.split('/')}
			{@const status = statusIcon(c.status)}
			<p class="flex items-center gap-1.5 text-xs text-muted-foreground">
				<GitCommit class="size-3.5" />
				<span class="font-mono">{c.sha.slice(0, 7)}</span>
				{#if status}
					{@const SIcon = status.icon}
					<SIcon class="size-3.5 {status.tone}" />
				{/if}
				<span class="truncate">{card.repo}</span>
			</p>
			<a
				href={resolve('/[namespace]/[name]/commit/[sha]', { namespace, name, sha: c.sha })}
				class="mt-1 block font-semibold wrap-break-word hover:text-primary">{title(c.message)}</a
			>
			<p class="mt-1 truncate text-xs text-muted-foreground">
				{c.author?.username || c.author?.name || 'someone'} committed
				{#if c.author?.at}{relativeTime(timestampDate(c.author.at))}{/if}
			</p>
			<p class="mt-1 text-xs tabular-nums">
				<span class="text-success">+{c.additions}</span>
				<span class="text-destructive">-{c.deletions}</span>
				<span class="text-muted-foreground"
					>in {c.changedFiles} file{c.changedFiles === 1 ? '' : 's'}</span
				>
			</p>
			{#if card.commit.branches.length || card.commit.tags.length}
				<div class="mt-2 flex flex-wrap gap-1 text-xs text-muted-foreground">
					{#each card.commit.branches.slice(0, 3) as b (b)}
						<span class="inline-flex items-center gap-1 rounded-full border px-2 py-0.5 font-mono"
							><GitBranch class="size-3" />{b}</span
						>
					{/each}
					{#each card.commit.tags.slice(0, 3) as t (t)}
						<span class="inline-flex items-center gap-1 rounded-full border px-2 py-0.5 font-mono"
							><Tag class="size-3" />{t}</span
						>
					{/each}
				</div>
			{/if}
		{/if}
	</div>
{/if}
