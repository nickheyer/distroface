<script lang="ts">
	import { resolve } from '$app/paths';
	import { GitBranch, Tag, RotateCcw, Ban, Loader2, Timer, Play } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import CommitBadge from '$lib/components/git/commit-badge.svelte';
	import RunStatusIcon from '$lib/components/git/run-status-icon.svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { ActionsStatus, type WorkflowRun } from '$lib/proto/distroface/v1/actions_pb';
	import { durationOf, eventLabel, formatDuration, isActive, refInfo, runTitle } from './actions-state';
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

	async function cancel(run: WorkflowRun) {
		if (busy[run.id]) return;
		busy[run.id] = true;
		try {
			await rpcClient.actions.cancelRun({ namespace, name, id: run.id });
			toast.success(`Run #${run.number} cancelled`);
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
	<div class="rounded-xl border border-border/60 divide-y divide-border/60">
		{#each { length: 5 }, i (i)}
			<div class="flex items-center gap-3 px-4 py-3">
				<div class="h-4 w-4 rounded-full bg-muted animate-pulse"></div>
				<div class="flex-1 space-y-1.5">
					<div class="h-3.5 w-64 bg-muted rounded animate-pulse"></div>
					<div class="h-3 w-40 bg-muted rounded animate-pulse"></div>
				</div>
				<div class="h-3 w-16 bg-muted rounded animate-pulse"></div>
			</div>
		{/each}
	</div>
{:else if runs.length === 0}
	<EmptyState icon={Play} message={emptyMessage} description="Runs appear here when a workflow is triggered." />
{:else}
	<div class="rounded-xl border border-border/60 divide-y divide-border/60 transition-opacity {loading ? 'opacity-60' : ''}">
		{#each runs as run (run.id)}
			{@const ref = refInfo(run.ref)}
			{@const at = created(run)}
			{@const dur = duration(run)}
			<div class="flex items-start gap-3 px-4 py-3 hover:bg-muted/30 transition-colors">
				<div class="pt-0.5">
					<RunStatusIcon status={run.status} conclusion={run.conclusion} needsApproval={run.needsApproval} />
				</div>
				<div class="flex-1 min-w-0">
					<div class="flex items-center gap-2 min-w-0 flex-wrap">
						<a href={resolve('/[namespace]/[name]/actions/runs/[id]', { namespace, name, id: run.id })} class="text-[13.5px] font-medium hover:text-primary truncate">
							{runTitle(run)}
						</a>
						<span class="text-[12px] text-muted-foreground tabular-nums shrink-0">#{run.number}</span>
						{#if run.needsApproval}
							<Badge variant="outline" class="text-[10px] h-4.5 py-0 border-amber-500/40 text-amber-600 dark:text-amber-400">Needs approval</Badge>
						{/if}
					</div>
					<div class="flex items-center gap-x-2 gap-y-0.5 flex-wrap text-[12px] text-muted-foreground mt-0.5 min-w-0">
						<span class="truncate text-foreground/80">{run.workflowName || run.workflowPath}</span>
						<Badge variant="secondary" class="text-[10px] h-4.5 py-0 font-normal">{eventLabel(run.event)}</Badge>
						{#if ref.name}
							<a href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', { namespace, name, ref: encodeURIComponent(ref.name), path: '' })} class="inline-flex items-center gap-1 font-mono hover:text-primary truncate max-w-48" title={run.ref}>
								{#if ref.kind === 'tag'}
									<Tag class="h-3 w-3 shrink-0" />
								{:else}
									<GitBranch class="h-3 w-3 shrink-0" />
								{/if}
								<span class="truncate">{ref.name}</span>
							</a>
						{/if}
						{#if run.sha}
							<CommitBadge sha={run.sha} {namespace} {name} copy={false} />
						{/if}
						{#if run.triggerUser}
							<a href={resolve('/[namespace]', { namespace: run.triggerUser })} class="hover:text-primary">{run.triggerUser}</a>
						{/if}
						{#if run.pullRequestNumber}
							<a href={resolve('/[namespace]/[name]/pulls/[number]', { namespace, name, number: String(run.pullRequestNumber) })} class="hover:text-primary">PR #{run.pullRequestNumber}</a>
						{/if}
					</div>
				</div>
				<div class="flex items-center gap-3 shrink-0">
					<div class="text-right text-[12px] text-muted-foreground tabular-nums hidden sm:block">
						{#if dur}
							<div class="inline-flex items-center gap-1"><Timer class="h-3 w-3" />{dur}</div>
						{/if}
						{#if at}
							<div title={at.toLocaleString()}>{relativeTime(at)}</div>
						{/if}
					</div>
					<PermissionGate allowed={canWrite}>
						<div class="flex items-center gap-0.5">
							{#if isActive(run.status)}
								<Button variant="ghost" size="sm" class="h-7 px-2 text-muted-foreground hover:text-destructive" title="Cancel run" onclick={() => cancel(run)} disabled={!!busy[run.id]}>
									{#if busy[run.id]}
										<Loader2 class="h-3.5 w-3.5 animate-spin" />
									{:else}
										<Ban class="h-3.5 w-3.5" />
									{/if}
								</Button>
							{:else if run.status === ActionsStatus.COMPLETED}
								<Button variant="ghost" size="sm" class="h-7 px-2 text-muted-foreground hover:text-foreground" title="Re-run all jobs" onclick={() => rerun(run)} disabled={!!busy[run.id]}>
									{#if busy[run.id]}
										<Loader2 class="h-3.5 w-3.5 animate-spin" />
									{:else}
										<RotateCcw class="h-3.5 w-3.5" />
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
