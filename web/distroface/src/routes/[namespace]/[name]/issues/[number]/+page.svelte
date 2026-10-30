<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import {
		CircleDot,
		CheckCircle,
		Lock,
		Pencil,
		Blocks,
		Pin,
		Ellipsis,
		Link as Link2,
		ArrowLeft,
		Loader
	} from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import {
		DropdownMenu,
		DropdownMenuContent,
		DropdownMenuItem,
		DropdownMenuTrigger
	} from '$lib/components/ui/dropdown-menu';
	import EmptyState from '$lib/components/empty-state.svelte';
	import Markdown from '$lib/components/git/markdown.svelte';
	import CommentBox from '$lib/components/git/comment-box.svelte';
	import ReactionBar from '$lib/components/git/reaction-bar.svelte';
	import Timeline from '$lib/components/git/timeline.svelte';
	import IssueSidebar from '$lib/components/git/issue-sidebar.svelte';
	import AgentSessionsPanel from '$lib/components/git/agent-sessions-panel.svelte';
	import { rpcClient, silentCallOptions, backgroundCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import { IssueState, type Issue, type Comment } from '$lib/proto/distroface/v1/issue_pb';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const base = $derived(`/${namespace}/${name}`);
	const number = $derived(Number(page.params.number ?? 0));

	let issue = $state<Issue | undefined>();
	let notFound = $state(false);
	let comments = $state<Comment[]>([]);
	let nextToken = $state('');
	let commentsLoading = $state(false);
	let loadedFor = $state(0);

	const viewerId = $derived(authStore.user?.id ?? '');
	const isAuthor = $derived(!!issue && !!viewerId && issue.author?.id === viewerId);
	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.ISSUES) && !repo.isArchived);
	const canEditIssue = $derived(canWrite || (isAuthor && !repo.isArchived));
	const canComment = $derived(
		authStore.isAuthenticated &&
			gitRepoStore.canRead(GitUnit.ISSUES) &&
			!repo.isArchived &&
			(!issue?.locked || canWrite)
	);

	async function loadIssue(silent = false) {
		try {
			const resp = await rpcClient.issue.getIssue(
				{ namespace, name, number: BigInt(number) },
				silent ? backgroundCallOptions : silentCallOptions
			);
			if (silent && !resp.issue) return;
			issue = resp.issue;
			notFound = !issue;
		} catch {
			if (silent) return;
			issue = undefined;
			notFound = true;
		}
	}

	async function loadComments(reset = true, silent = false) {
		if (!silent) commentsLoading = true;
		try {
			const resp = await rpcClient.issue.listComments(
				{
					namespace,
					name,
					number: BigInt(number),
					page: { pageSize: 50, pageToken: reset ? '' : nextToken, orderBy: '' }
				},
				silentCallOptions
			);
			comments = reset ? resp.comments : [...comments, ...resp.comments];
			nextToken = resp.page?.nextPageToken ?? '';
		} catch {
			if (reset) comments = [];
		} finally {
			commentsLoading = false;
		}
	}

	function refresh() {
		loadIssue();
		loadComments();
	}

	// Background refresh while visible, comments only after a change
	const POLL_MS = 10000;
	let pollTick = $state(0);
	const activity = (i: NonNullable<typeof issue>) =>
		`${i.updatedAt?.seconds ?? 0n}:${i.commentCount}:${i.state}`;
	async function poll() {
		if (!issue || document.visibilityState !== 'visible') return;
		const before = activity(issue);
		await loadIssue(true);
		if (issue && activity(issue) !== before) await loadComments(true, true);
		pollTick++;
	}
	$effect(() => {
		const t = setInterval(() => void poll(), POLL_MS);
		return () => clearInterval(t);
	});

	$effect(() => {
		if (!number || loadedFor === number) return;
		loadedFor = number;
		issue = undefined;
		notFound = false;
		gitRepoStore.load(namespace, name);
		refresh();
	});

	// Title editing
	let editingTitle = $state(false);
	let titleDraft = $state('');
	let savingTitle = $state(false);

	async function saveTitle() {
		if (!issue || !titleDraft.trim() || savingTitle) return;
		if (titleDraft.trim() === issue.title) {
			editingTitle = false;
			return;
		}
		savingTitle = true;
		try {
			const resp = await rpcClient.issue.updateIssue({
				namespace,
				name,
				number: issue.number,
				title: titleDraft.trim()
			});
			if (resp.issue) issue = resp.issue;
			editingTitle = false;
			loadComments();
		} catch {
			// Error interceptor already toasted
		} finally {
			savingTitle = false;
		}
	}

	// Body editing
	let editingBody = $state(false);
	let bodyDraft = $state('');
	let savingBody = $state(false);

	async function saveBody() {
		if (!issue || savingBody) return;
		savingBody = true;
		try {
			const resp = await rpcClient.issue.updateIssue({
				namespace,
				name,
				number: issue.number,
				body: bodyDraft
			});
			if (resp.issue) issue = resp.issue;
			editingBody = false;
		} catch {
			// Error interceptor already toasted
		} finally {
			savingBody = false;
		}
	}

	// New comment and state changes
	let draft = $state('');
	let posting = $state(false);

	async function postComment() {
		if (!issue || !draft.trim() || posting) return;
		posting = true;
		try {
			await rpcClient.issue.createComment({ namespace, name, number: issue.number, body: draft });
			draft = '';
			refresh();
		} catch {
			// Error interceptor already toasted
		} finally {
			posting = false;
		}
	}

	async function setState(state: IssueState) {
		if (!issue || posting) return;
		posting = true;
		try {
			if (draft.trim()) {
				await rpcClient.issue.createComment({ namespace, name, number: issue.number, body: draft });
				draft = '';
			}
			const resp = await rpcClient.issue.updateIssue({
				namespace,
				name,
				number: issue.number,
				state
			});
			if (resp.issue) issue = resp.issue;
			toast.success(state === IssueState.CLOSED ? 'Issue closed' : 'Issue reopened');
			loadComments();
		} catch {
			// Error interceptor already toasted
		} finally {
			posting = false;
		}
	}

	function copyLink() {
		navigator.clipboard
			?.writeText(
				`${location.origin}${resolve('/[namespace]/[name]/issues/[number]', { namespace, name, number: String(number) })}`
			)
			.then(() => toast.success('Link copied'));
	}

	const isOpen = $derived(issue?.state !== IssueState.CLOSED);
	const knownUsers = $derived(
		issue
			? [
					...issue.assignees,
					...(issue.author ? [issue.author] : []),
					...(issue.closedBy ? [issue.closedBy] : [])
				]
			: []
	);
</script>

{#if notFound}
	<EmptyState
		icon={CircleDot}
		message="Issue not found"
		description="#{number} does not exist in this repository or you cannot see it."
	>
		{#snippet actions()}
			<Button variant="outline" href="{base}/issues"><ArrowLeft />Back to issues</Button>
		{/snippet}
	</EmptyState>
{:else if !issue}
	<div class="space-y-4">
		<Skeleton class="h-8 w-2/3" />
		<Skeleton class="h-4 w-1/3" />
		<Skeleton class="h-40 w-full rounded-lg" />
	</div>
{:else}
	<div class="space-y-5">
		<header class="space-y-2">
			{#if editingTitle}
				<div class="flex flex-col gap-2 sm:flex-row sm:items-center">
					<Input
						bind:value={titleDraft}
						class="flex-1"
						aria-label="Issue title"
						onkeydown={(e) => {
							if (e.key === 'Enter') saveTitle();
							if (e.key === 'Escape') editingTitle = false;
						}}
						autofocus
					/>
					<div class="flex items-center justify-end gap-2">
						<Button variant="outline" onclick={() => (editingTitle = false)}>Cancel</Button>
						<Button onclick={saveTitle} disabled={savingTitle || !titleDraft.trim()}>
							{#if savingTitle}
								<Loader class="animate-spin" />
							{/if}
							{savingTitle ? 'Saving' : 'Save'}
						</Button>
					</div>
				</div>
			{:else}
				<div class="flex items-start gap-3">
					<h1
						class="min-w-0 flex-1 text-2xl leading-snug font-semibold tracking-tight wrap-break-word"
					>
						{issue.title} <span class="font-normal text-muted-foreground">#{issue.number}</span>
					</h1>
					{#if issue.pinned}<Pin
							class="mt-1.5 size-4 shrink-0 text-muted-foreground"
							aria-label="Pinned"
						/>{/if}
					{#if canEditIssue}
						<Button
							variant="outline"
							class="shrink-0"
							onclick={() => {
								titleDraft = issue!.title;
								editingTitle = true;
							}}><Pencil />Edit</Button
						>
					{/if}
					<DropdownMenu>
						<DropdownMenuTrigger>
							{#snippet child({ props })}
								<Button {...props} variant="outline" size="icon" class="shrink-0" aria-label="More"
									><Ellipsis /></Button
								>
							{/snippet}
						</DropdownMenuTrigger>
						<DropdownMenuContent align="end" class="w-40">
							<DropdownMenuItem onclick={copyLink}><Link2 />Copy link</DropdownMenuItem>
						</DropdownMenuContent>
					</DropdownMenu>
				</div>
			{/if}
			<div class="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
				{#if isOpen}
					<Badge variant="success"><CircleDot />Open</Badge>
				{:else}
					<Badge variant="done"><CheckCircle />Closed</Badge>
				{/if}
				{#if issue.author}
					<a
						href={resolve('/[namespace]', { namespace: issue.author.username })}
						class="font-medium text-foreground hover:text-primary"
						data-hover="user"
						data-user={issue.author.username}>{issue.author.username}</a
					>
				{/if}
				<span
					>opened this issue{#if issue.createdAt}
						<span title={timestampDate(issue.createdAt).toLocaleString()}
							>{relativeTime(timestampDate(issue.createdAt))}</span
						>{/if}</span
				>
				<span class="text-muted-foreground/50">/</span>
				<span>{issue.commentCount} comment{issue.commentCount === 1 ? '' : 's'}</span>
				{#if !isOpen && issue.closedBy}
					<span class="text-muted-foreground/50">/</span>
					<span
						>closed by <a
							href={resolve('/[namespace]', { namespace: issue.closedBy.username })}
							class="hover:text-primary">{issue.closedBy.username}</a
						>{#if issue.closedAt}
							{relativeTime(timestampDate(issue.closedAt))}{/if}</span
					>
				{/if}
			</div>
		</header>

		{#if issue.locked}
			<Alert>
				<Lock />
				<AlertDescription>
					<span
						>This conversation has been locked{#if issue.lockReason}&#32;as <strong
								class="text-foreground">{issue.lockReason}</strong
							>{/if} and limited to collaborators with write access.</span
					>
				</AlertDescription>
			</Alert>
		{/if}
		{#if issue.blocked}
			<Alert variant="warning">
				<Blocks />
				<AlertDescription
					>This issue is blocked by open dependencies and cannot be closed until they are resolved.</AlertDescription
				>
			</Alert>
		{/if}

		<div class="grid gap-6 lg:grid-cols-[minmax(0,1fr)_16rem]">
			<div class="min-w-0 space-y-4">
				<div class="box">
					<div class="flex items-center gap-2 border-b px-4 py-2 text-xs text-muted-foreground">
						<Avatar class="size-5">
							<AvatarFallback class="text-2xs"
								>{(issue.author?.username ?? '?').charAt(0)}</AvatarFallback
							>
						</Avatar>
						{#if issue.author}<a
								href={resolve('/[namespace]', { namespace: issue.author.username })}
								class="font-medium text-foreground hover:text-primary"
								data-hover="user"
								data-user={issue.author.username}>{issue.author.username}</a
							>{/if}
						<span>opened</span>
						{#if issue.createdAt}<span title={timestampDate(issue.createdAt).toLocaleString()}
								>{relativeTime(timestampDate(issue.createdAt))}</span
							>{/if}
						{#if issue.updatedAt && issue.createdAt && timestampDate(issue.updatedAt).getTime() - timestampDate(issue.createdAt).getTime() > 60000}
							<span>(edited)</span>
						{/if}
						{#if canEditIssue && !editingBody}
							<div class="ml-auto">
								<Button
									variant="ghost-muted"
									size="icon-xs"
									title="Edit description"
									aria-label="Edit description"
									onclick={() => {
										bodyDraft = issue!.body;
										editingBody = true;
									}}><Pencil /></Button
								>
							</div>
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
									<Button variant="outline" onclick={() => (editingBody = false)}>Cancel</Button>
								{/snippet}
							</CommentBox>
						{:else if issue.body.trim()}
							<Markdown source={issue.body} />
						{:else}
							<p class="text-muted-foreground italic">No description provided</p>
						{/if}
					</div>
					{#if !editingBody && (issue.reactions.some((r) => r.count > 0) || canComment)}
						<div class="px-4 pb-3">
							<ReactionBar
								{namespace}
								{name}
								{number}
								reactions={issue.reactions}
								canReact={canComment}
								onChanged={loadIssue}
							/>
						</div>
					{/if}
				</div>

				<Timeline
					{namespace}
					{name}
					{number}
					items={comments}
					{canComment}
					canModerate={canWrite}
					loading={commentsLoading && comments.length === 0}
					onChanged={refresh}
					{knownUsers}
				/>

				{#if nextToken}
					<div class="flex justify-center">
						<Button
							variant="outline"
							onclick={() => loadComments(false)}
							disabled={commentsLoading}
						>
							{#if commentsLoading}
								<Loader class="animate-spin" />
							{/if}
							{commentsLoading ? 'Loading' : 'Load more'}
						</Button>
					</div>
				{/if}

				{#if canComment}
					<div class="border-t pt-4">
						<CommentBox bind:value={draft} submitting={posting} onSubmit={postComment}>
							{#snippet extra()}
								{#if canEditIssue}
									{#if isOpen}
										<Button
											variant="outline"
											onclick={() => setState(IssueState.CLOSED)}
											disabled={posting || issue?.blocked}
										>
											<CheckCircle class="text-done" />{draft.trim()
												? 'Close with comment'
												: 'Close issue'}
										</Button>
									{:else}
										<Button
											variant="outline"
											onclick={() => setState(IssueState.OPEN)}
											disabled={posting}
										>
											<CircleDot class="text-success" />{draft.trim()
												? 'Reopen with comment'
												: 'Reopen issue'}
										</Button>
									{/if}
								{/if}
							{/snippet}
						</CommentBox>
					</div>
				{:else if !authStore.isAuthenticated}
					<p class="py-3 text-center text-sm text-muted-foreground">
						<a href={resolve('/login')} class="link">Sign in</a> to join the conversation
					</p>
				{:else if issue.locked}
					<p
						class="inline-flex w-full items-center justify-center gap-1.5 py-3 text-center text-sm text-muted-foreground"
					>
						<Lock class="size-3.5" />Locked, only writers can comment
					</p>
				{/if}
			</div>

			<aside class="space-y-5 text-sm">
				<IssueSidebar
					{namespace}
					{name}
					bind:issue
					{canWrite}
					canModerate={canWrite}
					onChanged={refresh}
				/>
				<AgentSessionsPanel
					{namespace}
					{name}
					issueNumber={issue.number}
					canStart={canWrite}
					tick={pollTick}
				/>
			</aside>
		</div>
	</div>
{/if}

<svelte:head>
	<title>{issue ? `${issue.title} #${issue.number}` : `#${number}`} - {namespace}/{name}</title>
</svelte:head>
