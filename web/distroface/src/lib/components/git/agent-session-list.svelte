<script lang="ts">
	import { resolve } from '$app/paths';
	import { Bot, Ban, RotateCcw, Loader, Timer, GitPullRequest, CircleDot } from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import AgentStatusIcon from '$lib/components/git/agent-status-icon.svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import type { AgentSession } from '$lib/proto/distroface/v1/agents_pb';
	import {
		formatTokens,
		isSessionActive,
		sessionState,
		sessionStateLabels,
		sessionTitle,
		triggerLabel
	} from './agents-state';
	import { durationOf, formatDuration } from './actions-state';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	let {
		namespace,
		name,
		sessions,
		loading = false,
		canWrite = false,
		emptyMessage = 'No agent sessions yet',
		emptyDescription = 'Sessions appear here when an agent is asked to work on this repository.',
		onChanged
	}: {
		namespace: string;
		name: string;
		sessions: AgentSession[];
		loading?: boolean;
		canWrite?: boolean;
		emptyMessage?: string;
		emptyDescription?: string;
		onChanged?: () => void;
	} = $props();

	let now = $state(Date.now());
	const anyActive = $derived(sessions.some((s) => isSessionActive(s.status)));

	$effect(() => {
		if (!anyActive) return;
		const t = setInterval(() => (now = Date.now()), 1000);
		return () => clearInterval(t);
	});

	let busy = $state<Record<string, boolean>>({});
	let cancelOpen = $state(false);
	let cancelTarget = $state<AgentSession | null>(null);

	async function cancel(s: AgentSession) {
		if (busy[s.id]) return;
		busy[s.id] = true;
		try {
			await rpcClient.agents.cancelAgentSession({ namespace, name, id: s.id });
			toast.success(`Session #${s.number} cancelled`);
			cancelOpen = false;
			onChanged?.();
		} catch {
			// Error interceptor already toasted
		} finally {
			delete busy[s.id];
		}
	}

	async function rerun(s: AgentSession) {
		if (busy[s.id]) return;
		busy[s.id] = true;
		try {
			await rpcClient.agents.rerunAgentSession({ namespace, name, id: s.id });
			toast.success(`Session #${s.number} queued again`);
			onChanged?.();
		} catch {
			// Error interceptor already toasted
		} finally {
			delete busy[s.id];
		}
	}

	function duration(s: AgentSession): string {
		const ms = durationOf(s.startedAt, s.finishedAt, now);
		return ms === undefined ? '' : formatDuration(ms);
	}

	function created(s: AgentSession): Date | undefined {
		return s.createdAt ? timestampDate(s.createdAt) : undefined;
	}

	function targetHref(s: AgentSession): string {
		const num = s.resultPullNumber > 0n ? s.resultPullNumber : s.issueNumber;
		const isPull = s.resultPullNumber > 0n || s.isPull;
		return resolve(
			isPull ? '/[namespace]/[name]/pulls/[number]' : '/[namespace]/[name]/issues/[number]',
			{
				namespace,
				name,
				number: String(num)
			}
		);
	}
</script>

{#if loading && sessions.length === 0}
	<div class="box divide-y">
		{#each { length: 5 }, i (i)}
			<div class="flex items-center gap-3 px-4 py-3">
				<Skeleton class="size-4 rounded-full" />
				<div class="flex-1 space-y-1.5">
					<Skeleton class="h-3.5 w-64" />
					<Skeleton class="h-3 w-40" />
				</div>
				<Skeleton class="h-3 w-16" />
			</div>
		{/each}
	</div>
{:else if sessions.length === 0}
	<EmptyState icon={Bot} message={emptyMessage} description={emptyDescription} />
{:else}
	<div class="box divide-y transition-opacity {loading ? 'opacity-60' : ''}">
		{#each sessions as s (s.id)}
			{@const at = created(s)}
			{@const dur = duration(s)}
			{@const state = sessionState(s.status, s.conclusion)}
			{@const tokens = s.inputTokens + s.outputTokens}
			<div class="flex items-start gap-3 px-4 py-3 transition-colors hover:bg-muted/30">
				<div class="pt-0.5">
					<AgentStatusIcon status={s.status} conclusion={s.conclusion} />
				</div>
				<div class="min-w-0 flex-1">
					<div class="flex min-w-0 flex-wrap items-center gap-2">
						<a
							href={resolve('/[namespace]/[name]/agents/sessions/[id]', {
								namespace,
								name,
								id: s.id
							})}
							class="truncate text-sm font-medium hover:text-primary"
						>
							{sessionTitle(s)}
						</a>
						<span class="shrink-0 text-xs text-muted-foreground tabular-nums">#{s.number}</span>
						{#if state === 'approval'}
							<Badge variant="warning">Needs approval</Badge>
						{:else if state === 'budget'}
							<Badge variant="warning">{sessionStateLabels[state]}</Badge>
						{/if}
					</div>
					<div
						class="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted-foreground"
					>
						<span class="inline-flex items-center gap-1 text-foreground">
							<Bot class="size-3" />@{s.botUsername || s.agentSlug}
						</span>
						<Badge variant="secondary">{triggerLabel(s.trigger)}</Badge>
						{#if s.issueNumber > 0n || s.resultPullNumber > 0n}
							<!-- eslint-disable svelte/no-navigation-without-resolve -->
							<a href={targetHref(s)} class="inline-flex items-center gap-1 hover:text-primary">
								{#if s.resultPullNumber > 0n || s.isPull}
									<GitPullRequest class="size-3" />
								{:else}
									<CircleDot class="size-3" />
								{/if}
								#{s.resultPullNumber > 0n ? s.resultPullNumber : s.issueNumber}
							</a>
							<!-- eslint-enable svelte/no-navigation-without-resolve -->
						{/if}
						{#if s.model}
							<span class="truncate font-mono">{s.model}</span>
						{/if}
						{#if s.triggerUser}
							<a
								href={resolve('/[namespace]', { namespace: s.triggerUser })}
								class="hover:text-primary">{s.triggerUser}</a
							>
						{/if}
						{#if tokens > 0n}
							<span class="tabular-nums">{formatTokens(tokens)} tokens</span>
						{/if}
					</div>
				</div>
				<div class="flex shrink-0 items-center gap-3">
					<div class="hidden text-right text-xs text-muted-foreground tabular-nums sm:block">
						{#if dur}
							<div class="inline-flex items-center gap-1"><Timer class="size-3" />{dur}</div>
						{/if}
						{#if at}
							<div>{relativeTime(at)}</div>
						{/if}
					</div>
					<PermissionGate allowed={canWrite}>
						<div class="flex items-center gap-1">
							{#if isSessionActive(s.status)}
								<Button
									variant="ghost-muted"
									size="icon-sm"
									title="Cancel session"
									aria-label="Cancel session #{s.number}"
									disabled={!!busy[s.id]}
									onclick={() => {
										cancelTarget = s;
										cancelOpen = true;
									}}
								>
									{#if busy[s.id]}<Loader class="animate-spin" />{:else}<Ban />{/if}
								</Button>
							{:else}
								<Button
									variant="ghost-muted"
									size="icon-sm"
									title="Run again"
									aria-label="Run session #{s.number} again"
									disabled={!!busy[s.id]}
									onclick={() => rerun(s)}
								>
									{#if busy[s.id]}<Loader class="animate-spin" />{:else}<RotateCcw />{/if}
								</Button>
							{/if}
						</div>
					</PermissionGate>
				</div>
			</div>
		{/each}
	</div>
{/if}

<ConfirmDialog
	bind:open={cancelOpen}
	title="Cancel session"
	confirmLabel="Cancel session"
	cancelLabel="Keep running"
	onConfirm={() => cancelTarget && cancel(cancelTarget)}
	loading={!!(cancelTarget && busy[cancelTarget.id])}
	icon={Ban}
>
	{#snippet description()}
		Stop session <strong>#{cancelTarget?.number}</strong>? Commits already made stay on the branch.
	{/snippet}
</ConfirmDialog>
