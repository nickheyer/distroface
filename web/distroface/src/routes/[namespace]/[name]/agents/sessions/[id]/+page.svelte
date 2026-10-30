<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import {
		ArrowLeft,
		Ban,
		RotateCcw,
		Bot,
		GitBranch,
		GitPullRequest,
		CircleDot,
		Timer,
		Coins,
		ShieldAlert,
		ShieldCheck,
		ShieldX,
		Loader,
		MessageSquarePlus,
		GitCommit
	} from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Alert, AlertDescription, AlertTitle } from '$lib/components/ui/alert';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import CommitBadge from '$lib/components/git/commit-badge.svelte';
	import AgentStatusIcon from '$lib/components/git/agent-status-icon.svelte';
	import AgentTranscript from '$lib/components/git/agent-transcript.svelte';
	import { rpcClient, silentCallOptions, backgroundCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import {
		AgentEventKind,
		AgentSessionStatus,
		type AgentSession,
		type AgentSessionEvent
	} from '$lib/proto/distroface/v1/agents_pb';
	import {
		cacheShare,
		formatTokens,
		isSessionActive,
		kindLabels,
		parseEventData,
		sessionState,
		sessionStateLabels,
		sessionTitle,
		triggerLabel
	} from '$lib/components/git/agents-state';
	import { durationOf, formatDuration } from '$lib/components/git/actions-state';
	import { relativeTime, issueRef } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const id = $derived(page.params.id ?? '');
	const repo = $derived(gitRepoStore.repo);
	const agentsHref = $derived(resolve('/[namespace]/[name]/agents', { namespace, name }));

	$effect(() => {
		if (namespace && name) gitRepoStore.load(namespace, name);
	});

	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.AGENTS) && !repo?.isArchived);

	let session = $state<AgentSession | undefined>();
	let loading = $state(true);
	let missing = $state(false);
	let now = $state(Date.now());
	let seq = 0;

	async function load(silent = false) {
		const opts = silent ? backgroundCallOptions : silentCallOptions;
		const mine = ++seq;
		if (!silent) loading = true;
		try {
			const resp = await rpcClient.agents.getAgentSession({ namespace, name, id }, opts);
			if (mine !== seq) return;
			session = resp.session;
			missing = !resp.session;
		} catch {
			if (mine === seq && !silent) {
				session = undefined;
				missing = true;
			}
		} finally {
			if (mine === seq) loading = false;
		}
	}

	$effect(() => {
		void id;
		load();
	});

	const active = $derived(!!session && isSessionActive(session.status));
	const waiting = $derived(session?.status === AgentSessionStatus.WAITING_APPROVAL);

	$effect(() => {
		if (!active) return;
		const t = setInterval(() => {
			now = Date.now();
			load(true);
		}, 4000);
		return () => clearInterval(t);
	});

	// The stream announces state changes faster than polling
	function onStatus(status: AgentSessionStatus, done: boolean) {
		if (session && (session.status !== status || done)) load(true);
		if (done) void gitRepoStore.refresh();
	}

	// Issues, pulls, and comments the agent makes move the header counts
	const countTools = new Set([
		'create_issue',
		'open_pull_request',
		'update_issue',
		'commit',
		'post_comment',
		'submit_review'
	]);
	function onEvent(ev: AgentSessionEvent) {
		if (!active) return;
		if (
			ev.kind === AgentEventKind.RESULT ||
			(ev.kind === AgentEventKind.TOOL_RESULT && countTools.has(ev.tool))
		)
			void gitRepoStore.refresh();
	}

	let busy = $state('');
	let cancelOpen = $state(false);
	let note = $state('');
	let followUp = $state('');

	async function cancel() {
		if (!session || busy) return;
		busy = 'cancel';
		try {
			await rpcClient.agents.cancelAgentSession({ namespace, name, id });
			toast.success('Session cancelled');
			cancelOpen = false;
			await load(true);
		} catch {
			// Error interceptor already toasted
		} finally {
			busy = '';
		}
	}

	async function rerun() {
		if (!session || busy) return;
		busy = 'rerun';
		try {
			const resp = await rpcClient.agents.rerunAgentSession({ namespace, name, id });
			toast.success(`Session #${resp.session?.number} queued`);
			if (resp.session) {
				window.location.href = resolve('/[namespace]/[name]/agents/sessions/[id]', {
					namespace,
					name,
					id: resp.session.id
				});
			}
		} catch {
			// Error interceptor already toasted
		} finally {
			busy = '';
		}
	}

	async function resolveApproval(approve: boolean) {
		if (!session?.pendingApproval || busy) return;
		busy = approve ? 'approve' : 'reject';
		try {
			await rpcClient.agents.resolveAgentApproval({
				namespace,
				name,
				id,
				approvalId: session.pendingApproval.id,
				approve,
				note: note.trim()
			});
			toast.success(approve ? 'Action approved' : 'Action rejected');
			note = '';
			await load(true);
		} catch {
			// Error interceptor already toasted
		} finally {
			busy = '';
		}
	}

	async function rejectAndStop() {
		if (!session?.pendingApproval || busy) return;
		busy = 'stop';
		try {
			await rpcClient.agents.resolveAgentApproval({
				namespace,
				name,
				id,
				approvalId: session.pendingApproval.id,
				approve: false,
				note: note.trim() || 'stopped by reviewer'
			});
			await rpcClient.agents.cancelAgentSession({ namespace, name, id });
			toast.success('Session stopped');
			note = '';
			await load(true);
		} catch {
			// Error interceptor already toasted
		} finally {
			busy = '';
		}
	}

	async function sendFollowUp() {
		if (!session || !followUp.trim() || busy) return;
		busy = 'continue';
		try {
			await rpcClient.agents.continueAgentSession({ namespace, name, id, prompt: followUp.trim() });
			toast.success('Follow up queued');
			followUp = '';
			await load(true);
		} catch {
			// Error interceptor already toasted
		} finally {
			busy = '';
		}
	}

	const duration = $derived.by(() => {
		if (!session) return '';
		const ms = durationOf(session.startedAt, session.finishedAt, now);
		return ms === undefined ? '' : formatDuration(ms);
	});

	const approvalArgs = $derived(
		session?.pendingApproval ? parseEventData(session.pendingApproval.arguments) : {}
	);
	const approvalBody = $derived.by(() => {
		const a = approvalArgs.arguments as Record<string, unknown> | undefined;
		if (!a) return '';
		for (const key of ['command', 'message', 'body', 'title']) {
			if (typeof a[key] === 'string' && a[key]) return String(a[key]);
		}
		return JSON.stringify(a, null, 2);
	});

	function targetHref(s: AgentSession): string {
		return resolve(
			s.isPull ? '/[namespace]/[name]/pulls/[number]' : '/[namespace]/[name]/issues/[number]',
			{
				namespace,
				name,
				number: String(s.issueNumber)
			}
		);
	}
</script>

<div class="space-y-5">
	<!-- eslint-disable svelte/no-navigation-without-resolve -->
	<a
		href={agentsHref}
		class="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-primary"
	>
		<ArrowLeft class="size-3.5" />All sessions
	</a>
	<!-- eslint-enable svelte/no-navigation-without-resolve -->

	{#if loading && !session}
		<div class="space-y-4">
			<Skeleton class="h-20 w-full rounded-lg" />
			<Skeleton class="h-96 w-full rounded-lg" />
		</div>
	{:else if missing || !session}
		<EmptyState
			icon={Bot}
			message="Session not found"
			description="This session does not exist or was pruned by retention."
		/>
	{:else}
		{@const state = sessionState(session.status, session.conclusion)}
		<div class="rounded-lg border bg-card px-5 py-4">
			<div class="flex flex-wrap items-start gap-3">
				<div class="pt-0.5">
					<AgentStatusIcon status={session.status} conclusion={session.conclusion} class="size-6" />
				</div>
				<div class="min-w-0 flex-1">
					<h1 class="flex flex-wrap items-center gap-2 text-lg font-semibold tracking-tight">
						<span class="truncate">{sessionTitle(session)}</span>
						<span class="font-normal text-muted-foreground">#{session.number}</span>
						<Badge
							variant={state === 'success'
								? 'success'
								: state === 'failure' || state === 'timed_out'
									? 'destructive'
									: state === 'approval' || state === 'budget'
										? 'warning'
										: 'secondary'}
						>
							{sessionStateLabels[state]}
						</Badge>
					</h1>
					<div
						class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground"
					>
						<span class="inline-flex items-center gap-1 text-foreground"
							><Bot class="size-3.5" />@{session.botUsername || session.agentSlug}</span
						>
						<Badge variant="secondary">{kindLabels[session.agentKind] ?? 'Agent'}</Badge>
						<Badge variant="outline">{triggerLabel(session.trigger)}</Badge>
						{#if session.triggerUser}
							<span
								>by <a
									href={resolve('/[namespace]', { namespace: session.triggerUser })}
									class="hover:text-primary">{session.triggerUser}</a
								></span
							>
						{/if}
						{#if session.createdAt}<span>{relativeTime(timestampDate(session.createdAt))}</span
							>{/if}
						{#if duration}<span class="inline-flex items-center gap-1"
								><Timer class="size-3" />{duration}</span
							>{/if}
						<span class="inline-flex items-center gap-1 tabular-nums"
							><Coins class="size-3" />{formatTokens(
								session.inputTokens + session.outputTokens
							)}{session.maxTokens > 0n ? `/${formatTokens(session.maxTokens)}` : ''} tokens,
							{session.turns}{session.maxTurns > 0 ? `/${session.maxTurns}` : ''} turns{cacheShare(
								session.cacheReadTokens,
								session.promptTokens
							)
								? `, ${cacheShare(session.cacheReadTokens, session.promptTokens)} cached`
								: ''}</span
						>
						{#if session.model}<span class="font-mono">{session.model}</span>{/if}
						{#if session.providerName}<span>{session.providerName}</span>{/if}
					</div>
					<div class="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs">
						{#if session.issueNumber > 0n}
							<!-- eslint-disable svelte/no-navigation-without-resolve -->
							<a
								href={targetHref(session)}
								class="inline-flex items-center gap-1 hover:text-primary"
							>
								{#if session.isPull}<GitPullRequest class="size-3.5" />{:else}<CircleDot
										class="size-3.5"
									/>{/if}
								{session.isPull ? 'Pull request' : 'Issue'}
								{issueRef(session.isPull, session.issueNumber)}
							</a>
						{/if}
						{#if session.resultPullNumber > 0n}
							<a
								href={resolve('/[namespace]/[name]/pulls/[number]', {
									namespace,
									name,
									number: String(session.resultPullNumber)
								})}
								class="inline-flex items-center gap-1 font-medium hover:text-primary"
							>
								<GitPullRequest class="size-3.5" />Opened PR !{session.resultPullNumber}
							</a>
						{/if}
						{#if session.branch && session.resultCommits.length > 0}
							<a
								href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', {
									namespace,
									name,
									ref: encodeURIComponent(session.branch),
									path: ''
								})}
								class="inline-flex items-center gap-1 font-mono hover:text-primary"
							>
								<GitBranch class="size-3.5" />{session.branch}
							</a>
						{/if}
						{#if session.resultCommits.length > 0}
							<span class="inline-flex items-center gap-1 text-muted-foreground"
								><GitCommit class="size-3.5" />{session.resultCommits.length} commit{session
									.resultCommits.length === 1
									? ''
									: 's'}</span
							>
							{#each session.resultCommits.slice(-3) as sha (sha)}
								<CommitBadge {sha} {namespace} {name} copy={false} />
							{/each}
						{/if}
						{#if session.resultReviewId && session.issueNumber > 0n}
							<a href={targetHref(session)} class="hover:text-primary">Review submitted</a>
						{/if}
						<!-- eslint-enable svelte/no-navigation-without-resolve -->
					</div>
				</div>
				<PermissionGate allowed={canWrite}>
					<div class="flex shrink-0 flex-wrap items-center gap-2">
						{#if active}
							<Button variant="outline" onclick={() => (cancelOpen = true)} disabled={!!busy}>
								<Ban />Cancel
							</Button>
						{:else}
							<Button variant="outline" onclick={rerun} disabled={!!busy}>
								{#if busy === 'rerun'}<Loader class="animate-spin" />{:else}<RotateCcw />{/if}
								Run again
							</Button>
						{/if}
					</div>
				</PermissionGate>
			</div>
			{#if session.error}
				<Alert variant="destructive" class="mt-3">
					<AlertTitle>{sessionStateLabels[state]}</AlertTitle>
					<AlertDescription class="font-mono text-xs wrap-break-word"
						>{session.error}</AlertDescription
					>
				</Alert>
			{/if}
		</div>

		{#if waiting && session.pendingApproval}
			<div class="rounded-lg border border-warning/40 bg-warning/5 px-5 py-4">
				<div class="flex items-start gap-3">
					<ShieldAlert class="mt-0.5 size-5 shrink-0 text-warning" />
					<div class="min-w-0 flex-1 space-y-3">
						<div>
							<h2 class="text-sm font-semibold">
								Approval needed: {session.pendingApproval.title}
							</h2>
							<p class="text-xs text-muted-foreground">
								The agent wants to call <span class="font-mono">{session.pendingApproval.tool}</span
								>. Nothing happens until someone decides.
							</p>
						</div>
						<pre
							class="max-h-72 overflow-auto rounded-md border bg-card p-3 font-mono text-xs whitespace-pre-wrap">{approvalBody}</pre>
						<PermissionGate allowed={canWrite}>
							<Textarea
								bind:value={note}
								rows={2}
								placeholder="Optional note for the agent, shown when rejecting"
							/>
							<div class="flex flex-wrap items-center gap-2">
								<Button onclick={() => resolveApproval(true)} disabled={!!busy}>
									{#if busy === 'approve'}<Loader class="animate-spin" />{:else}<ShieldCheck />{/if}
									Approve
								</Button>
								<Button variant="outline" onclick={() => resolveApproval(false)} disabled={!!busy}>
									{#if busy === 'reject'}<Loader class="animate-spin" />{:else}<ShieldX />{/if}
									Reject and continue
								</Button>
								<Button variant="ghost-muted" onclick={rejectAndStop} disabled={!!busy}>
									{#if busy === 'stop'}<Loader class="animate-spin" />{:else}<Ban />{/if}
									Reject and stop
								</Button>
							</div>
						</PermissionGate>
					</div>
				</div>
			</div>
		{/if}

		{#key session.id}
			<AgentTranscript {namespace} {name} sessionId={session.id} {active} {onStatus} {onEvent} />
		{/key}

		{#if !active}
			<PermissionGate allowed={canWrite}>
				<div class="rounded-lg border bg-card px-4 py-3">
					<div class="mb-2 flex items-center gap-2 text-sm font-medium">
						<MessageSquarePlus class="size-4 text-muted-foreground" />Follow up
					</div>
					<Textarea
						bind:value={followUp}
						rows={3}
						placeholder="Tell the agent what to change or do next. It keeps the transcript and the working branch."
					/>
					<div class="mt-2 flex justify-end">
						<Button onclick={sendFollowUp} disabled={!!busy || !followUp.trim()}>
							{#if busy === 'continue'}<Loader class="animate-spin" />{:else}<Bot />{/if}
							Continue session
						</Button>
					</div>
				</div>
			</PermissionGate>
		{/if}
	{/if}
</div>

<ConfirmDialog
	bind:open={cancelOpen}
	title="Cancel session"
	confirmLabel="Cancel session"
	cancelLabel="Keep running"
	onConfirm={cancel}
	loading={busy === 'cancel'}
	icon={Ban}
>
	{#snippet description()}
		Stop session <strong>#{session?.number}</strong>? Commits already made stay on the branch.
	{/snippet}
</ConfirmDialog>
