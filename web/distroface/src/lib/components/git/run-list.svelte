<script lang="ts">
	import { resolve } from '$app/paths';
	import { GitBranch, Tag, RotateCcw, Ban, Loader, Timer, Play, Bot } from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import CommitBadge from '$lib/components/git/commit-badge.svelte';
	import RunStatusIcon from '$lib/components/git/run-status-icon.svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { ActionsStatus, type WorkflowRun } from '$lib/proto/distroface/v1/actions_pb';
	import {
		durationOf,
		eventLabel,
		formatDuration,
		isActive,
		refInfo,
		runTitle
	} from './actions-state';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	let {
		namespace,
		name,
		runs,
		loading = false,
		canWrite = false,
		emptyMessage = 'No workflow runs yet',
		onChanged
	}: {
		namespace: string;
		name: string;
		runs: WorkflowRun[];
		loading?: boolean;
		canWrite?: boolean;
		emptyMessage?: string;
		onChanged?: () => void;
	} = $props();

	let now = $state(Date.now());
	const anyActive = $derived(runs.some((r) => isActive(r.status)));

	$effect(() => {
		if (!anyActive) return;
		const t = setInterval(() => (now = Date.now()), 1000);
		return () => clearInterval(t);
	});

	let busy = $state<Record<string, boolean>>({});
	let cancelOpen = $state(false);
	let cancelTarget = $state<WorkflowRun | null>(null);

	function openCancel(run: WorkflowRun) {
		cancelTarget = run;
		cancelOpen = true;
	}

	async function cancel(run: WorkflowRun) {
		if (busy[run.id]) return;
		busy[run.id] = true;
		try {
			await rpcClient.actions.cancelRun({ namespace, name, id: run.id });
			toast.success(`Run #${run.number} cancelled`);
			cancelOpen = false;
			onChanged?.();
		} catch {
			// Error interceptor already toasted
		} finally {
			delete busy[run.id];
		}
	}

	async function rerun(run: WorkflowRun) {
		if (busy[run.id]) return;
		busy[run.id] = true;
		try {
			await rpcClient.actions.rerunRun({ namespace, name, id: run.id, failedOnly: false });
			toast.success(`Run #${run.number} queued again`);
			onChanged?.();
		} catch {
			// Error interceptor already toasted
		} finally {
			delete busy[run.id];
		}
	}

	function duration(run: WorkflowRun): string {
		const ms = durationOf(run.startedAt, run.finishedAt, now);
		return ms === undefined ? '' : formatDuration(ms);
	}

	function created(run: WorkflowRun): Date | undefined {
		return run.createdAt ? timestampDate(run.createdAt) : undefined;
	}
</script>

{#if loading && runs.length === 0}
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
{:else if runs.length === 0}
	<EmptyState
		icon={Play}
		message={emptyMessage}
		description="Runs appear here when a workflow is triggered."
	/>
{:else}
	<div class="box divide-y transition-opacity {loading ? 'opacity-60' : ''}">
		{#each runs as run (run.id)}
			{@const ref = refInfo(run.ref)}
			{@const at = created(run)}
			{@const dur = duration(run)}
			<div class="flex items-start gap-3 px-4 py-3 transition-colors hover:bg-muted/30">
				<div class="pt-0.5">
					<RunStatusIcon
						status={run.status}
						conclusion={run.conclusion}
						needsApproval={run.needsApproval}
					/>
				</div>
				<div class="min-w-0 flex-1">
					<div class="flex min-w-0 flex-wrap items-center gap-2">
						<a
							href={resolve('/[namespace]/[name]/actions/runs/[id]', {
								namespace,
								name,
								id: run.id
							})}
							class="truncate text-sm font-medium hover:text-primary"
						>
							{runTitle(run)}
						</a>
						<span class="shrink-0 text-xs text-muted-foreground tabular-nums">#{run.number}</span>
						{#if run.needsApproval}
							<Badge variant="warning">Needs approval</Badge>
						{/if}
						{#if run.agentSessionId}
							<a
								href={resolve('/[namespace]/[name]/agents/sessions/[id]', {
									namespace,
									name,
									id: run.agentSessionId
								})}
								class="inline-flex"
								title="Open the agent session"
							>
								<Badge variant="secondary"><Bot />{run.agentSlug || 'agent'}</Badge>
							</a>
						{/if}
					</div>
					<div
						class="mt-0.5 flex min-w-0 flex-wrap items-center gap-x-2 gap-y-0.5 text-xs text-muted-foreground"
					>
						<span class="truncate text-foreground">{run.workflowName || run.workflowPath}</span>
						<Badge variant="secondary">{eventLabel(run.event)}</Badge>
						{#if ref.name}
							<a
								href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', {
									namespace,
									name,
									ref: encodeURIComponent(ref.name),
									path: ''
								})}
								class="inline-flex max-w-48 items-center gap-1 truncate font-mono hover:text-primary"
								title={run.ref}
							>
								{#if ref.kind === 'tag'}
									<Tag class="size-3 shrink-0" />
								{:else}
									<GitBranch class="size-3 shrink-0" />
								{/if}
								<span class="truncate">{ref.name}</span>
							</a>
						{/if}
						{#if run.sha}
							<CommitBadge sha={run.sha} {namespace} {name} copy={false} />
						{/if}
						{#if run.triggerUser}
							<a
								href={resolve('/[namespace]', { namespace: run.triggerUser })}
								class="hover:text-primary">{run.triggerUser}</a
							>
						{/if}
						{#if run.pullRequestNumber}
							<a
								href={resolve('/[namespace]/[name]/pulls/[number]', {
									namespace,
									name,
									number: String(run.pullRequestNumber)
								})}
								class="hover:text-primary">PR !{run.pullRequestNumber}</a
							>
						{/if}
					</div>
				</div>
				<div class="flex shrink-0 items-center gap-3">
					<div class="hidden text-right text-xs text-muted-foreground tabular-nums sm:block">
						{#if dur}
							<div class="inline-flex items-center gap-1"><Timer class="size-3" />{dur}</div>
						{/if}
						{#if at}
							<div title={at.toLocaleString()}>{relativeTime(at)}</div>
						{/if}
					</div>
					<PermissionGate allowed={canWrite}>
						<div class="flex items-center gap-0.5">
							{#if isActive(run.status)}
								<Button
									variant="ghost-muted"
									size="icon-sm"
									class="hover:text-destructive"
									title="Cancel run"
									aria-label="Cancel run"
									onclick={() => openCancel(run)}
									disabled={!!busy[run.id]}
								>
									{#if busy[run.id]}
										<Loader class="animate-spin" />
									{:else}
										<Ban />
									{/if}
								</Button>
							{:else if run.status === ActionsStatus.COMPLETED && !run.agentSessionId}
								<Button
									variant="ghost-muted"
									size="icon-sm"
									title="Re-run all jobs"
									aria-label="Re-run all jobs"
									onclick={() => rerun(run)}
									disabled={!!busy[run.id]}
								>
									{#if busy[run.id]}
										<Loader class="animate-spin" />
									{:else}
										<RotateCcw />
									{/if}
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
	title="Cancel run"
	confirmLabel="Cancel run"
	cancelLabel="Keep running"
	onConfirm={() => cancelTarget && cancel(cancelTarget)}
	loading={!!cancelTarget && !!busy[cancelTarget.id]}
	icon={Ban}
>
	{#snippet description()}
		Running jobs in run #{cancelTarget?.number} are stopped and queued jobs are dropped. Finished jobs
		keep their results.
	{/snippet}
</ConfirmDialog>
