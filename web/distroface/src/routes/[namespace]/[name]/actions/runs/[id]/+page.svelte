<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import {
		ArrowLeft, Ban, RotateCcw, ShieldAlert, GitBranch, Tag, Timer, Package, Download, AlertCircle, AlertTriangle, Info, ChevronDown, Loader2, Play, Layers
	} from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger
	} from '$lib/components/ui/dropdown-menu';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import CommitBadge from '$lib/components/git/commit-badge.svelte';
	import RunStatusIcon from '$lib/components/git/run-status-icon.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import { ActionsConclusion, type Job, type RunArtifact, type WorkflowRun } from '$lib/proto/distroface/v1/actions_pb';
	import {
		durationOf, eventLabel, formatDuration, isActive, parseAnnotations, refInfo, runState, runTitle, stateLabels, type Annotation
	} from '$lib/components/git/actions-state';
	import { formatBytes, relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const id = $derived(page.params.id ?? '');
	const repo = $derived(gitRepoStore.repo);
	const actionsHref = $derived(resolve('/[namespace]/[name]/actions', { namespace, name }));

	$effect(() => {
		if (namespace && name) gitRepoStore.load(namespace, name);
	});

	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.ACTIONS) && !repo?.isArchived);

	let run = $state<WorkflowRun | undefined>();
	let artifacts = $state<RunArtifact[]>([]);
	let loading = $state(true);
	let missing = $state(false);
	let now = $state(Date.now());
	let seq = 0;

	async function load(silent = false) {
		const mine = ++seq;
		if (!silent) loading = true;
		try {
			const [r, a] = await Promise.all([
				rpcClient.actions.getWorkflowRun({ namespace, name, id }, silentCallOptions),
				rpcClient.actions.listRunArtifacts({ namespace, name, runId: id }, silentCallOptions).catch(() => undefined)
			]);
			if (mine !== seq) return;
			run = r.run;
			artifacts = a?.artifacts ?? [];
			missing = !r.run;
		} catch {
			if (mine === seq && !silent) {
				run = undefined;
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

	const active = $derived(!!run && isActive(run.status));

	$effect(() => {
		if (!active) return;
		const poll = setInterval(() => load(true), 5000);
		const tick = setInterval(() => (now = Date.now()), 1000);
		return () => {
			clearInterval(poll);
			clearInterval(tick);
		};
	});

	onMount(() => {
		now = Date.now();
	});

	const ref = $derived(run ? refInfo(run.ref) : { kind: 'ref' as const, name: '' });
	const attempt = $derived(run ? Math.max(1, ...run.jobs.map((j) => j.attempt)) : 1);
	const created = $derived(run?.createdAt ? timestampDate(run.createdAt) : undefined);
	const runDuration = $derived(run ? durationOf(run.startedAt, run.finishedAt, now) : undefined);
	const anyFailed = $derived(
		!!run && run.jobs.some((j) => j.conclusion === ActionsConclusion.FAILURE || j.conclusion === ActionsConclusion.TIMED_OUT)
	);

	function jobDuration(j: Job): string {
		const ms = durationOf(j.startedAt, j.finishedAt, now);
		return ms === undefined ? '' : formatDuration(ms);
	}

	// Jobs bucketed by longest needs chain
	const stages = $derived.by((): Job[][] => {
		const jobs = run?.jobs ?? [];
		const depth: Record<string, number> = {};
		const deps = (key: string) => jobs.filter((j) => j.name === key || j.name.startsWith(key + ' ('));
		const calc = (j: Job, trail: string[]): number => {
			const cached = depth[j.id];
			if (cached !== undefined) return cached;
			if (trail.includes(j.id)) return 0;
			const next = [...trail, j.id];
			let d = 0;
			for (const n of j.needs) for (const dep of deps(n)) d = Math.max(d, calc(dep, next) + 1);
			depth[j.id] = d;
			return d;
		};
		const out: Job[][] = [];
		for (const j of jobs) {
			const d = calc(j, []);
			(out[d] ??= []).push(j);
		}
		return out.filter((s) => s && s.length > 0);
	});

	const annotations = $derived.by(() => {
		const out: Array<Annotation & { job?: Job }> = [];
		for (const note of run?.notes ?? []) out.push({ level: 'warning', message: note });
		for (const j of run?.jobs ?? []) for (const a of parseAnnotations(j.annotations)) out.push({ ...a, job: j });
		return out;
	});
	const errors = $derived(annotations.filter((a) => a.level === 'error'));
	const warnings = $derived(annotations.filter((a) => a.level === 'warning'));
	const notices = $derived(annotations.filter((a) => a.level !== 'error' && a.level !== 'warning'));

	let busy = $state('');
	let cancelOpen = $state(false);

	async function act(kind: 'approve' | 'cancel' | 'rerun' | 'rerunFailed') {
		if (!run || busy) return;
		busy = kind;
		try {
			const req = { namespace, name, id: run.id };
			if (kind === 'approve') {
				await rpcClient.actions.approveRun(req);
				toast.success('Run approved');
			} else if (kind === 'cancel') {
				await rpcClient.actions.cancelRun(req);
				toast.success('Run cancelled');
				cancelOpen = false;
			} else {
				await rpcClient.actions.rerunRun({ ...req, failedOnly: kind === 'rerunFailed' });
				toast.success(kind === 'rerunFailed' ? 'Failed jobs queued again' : 'All jobs queued again');
			}
			await load(true);
		} catch {
			// Error interceptor already toasted
		} finally {
			busy = '';
		}
	}

	function location(a: Annotation): string {
		if (!a.file) return '';
		return a.line ? `${a.file}:${a.line}${a.col ? ':' + a.col : ''}` : a.file;
	}

	function expires(a: RunArtifact): string {
		return a.expiresAt ? `expires ${relativeTime(timestampDate(a.expiresAt))}` : '';
	}
</script>

{#snippet annotationRow(a: Annotation & { job?: Job })}
	<div class="flex items-start gap-2 px-3 py-2 text-[12.5px]">
		{#if a.level === 'error'}
			<AlertCircle class="h-4 w-4 text-destructive shrink-0 mt-0.5" />
		{:else if a.level === 'warning'}
			<AlertTriangle class="h-4 w-4 text-amber-500 shrink-0 mt-0.5" />
		{:else}
			<Info class="h-4 w-4 text-sky-500 shrink-0 mt-0.5" />
		{/if}
		<div class="min-w-0 flex-1">
			<div class="flex items-center gap-2 flex-wrap text-[11.5px] text-muted-foreground">
				{#if a.job}
					<a href={resolve('/[namespace]/[name]/actions/runs/[id]/jobs/[jobId]', { namespace, name, id, jobId: a.job.id })} class="font-medium text-foreground/80 hover:text-primary">{a.job.name}</a>
				{:else}
					<span class="font-medium text-foreground/80">Workflow</span>
				{/if}
				{#if a.title}<span class="font-medium text-foreground/80">{a.title}</span>{/if}
				{#if location(a)}<span class="font-mono break-all">{location(a)}</span>{/if}
			</div>
			<div class="whitespace-pre-wrap wrap-break-word">{a.message}</div>
		</div>
	</div>
{/snippet}

{#snippet jobRow(job: Job)}
	{@const dur = jobDuration(job)}
	<a href={resolve('/[namespace]/[name]/actions/runs/[id]/jobs/[jobId]', { namespace, name, id, jobId: job.id })} class="flex items-start gap-2.5 px-3 py-2.5 hover:bg-muted/40 transition-colors">
		<div class="pt-0.5"><RunStatusIcon status={job.status} conclusion={job.conclusion} /></div>
		<div class="flex-1 min-w-0">
			<div class="text-[13px] font-medium truncate">{job.name}</div>
			{#if job.needs.length > 0}
				<div class="text-[11px] text-muted-foreground truncate">after {job.needs.join(', ')}</div>
			{/if}
		</div>
		{#if dur}
			<span class="text-[11.5px] text-muted-foreground tabular-nums shrink-0">{dur}</span>
		{/if}
	</a>
{/snippet}

<div class="space-y-5">
	<a href={resolve('/[namespace]/[name]/actions', { namespace, name })} class="inline-flex items-center gap-1 text-[12.5px] text-muted-foreground hover:text-primary">
		<ArrowLeft class="h-3.5 w-3.5" />All runs
	</a>

	{#if loading && !run}
		<div class="space-y-4">
			<Skeleton class="h-16 w-full rounded-xl" />
			<div class="grid gap-6 lg:grid-cols-[18rem_minmax(0,1fr)]">
				<Skeleton class="h-48 w-full rounded-xl" />
				<Skeleton class="h-64 w-full rounded-xl" />
			</div>
		</div>
	{:else if missing || !run}
		<EmptyState icon={Play} message="Run not found" description="This run does not exist or was pruned." />
	{:else}
		<div class="rounded-xl border border-border/60 bg-card px-5 py-4">
			<div class="flex items-start gap-3 flex-wrap">
				<div class="pt-0.5">
					<RunStatusIcon status={run.status} conclusion={run.conclusion} needsApproval={run.needsApproval} class="h-6 w-6" />
				</div>
				<div class="flex-1 min-w-0">
					<h1 class="text-lg font-semibold flex items-center gap-2 flex-wrap">
						<span class="truncate">{runTitle(run)}</span>
						<span class="text-muted-foreground font-normal tabular-nums">#{run.number}</span>
						<Badge variant="outline" class="text-[10.5px] h-5 py-0">{stateLabels[runState(run.status, run.conclusion, run.needsApproval)]}</Badge>
					</h1>
					<div class="flex items-center gap-x-3 gap-y-1 flex-wrap text-[12.5px] text-muted-foreground mt-1">
						<a href={resolve(`${actionsHref}?workflow=${encodeURIComponent(run.workflowPath)}` as `/${string}`)} class="text-foreground/80 font-medium hover:text-primary">{run.workflowName || run.workflowPath}</a>
						<Badge variant="secondary" class="text-[10px] h-4.5 py-0 font-normal">{eventLabel(run.event)}</Badge>
						{#if run.sha}
							<CommitBadge sha={run.sha} {namespace} {name} />
						{/if}
						{#if ref.name}
							<a href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', { namespace, name, ref: encodeURIComponent(ref.name), path: '' })} class="inline-flex items-center gap-1 font-mono hover:text-primary" title={run.ref}>
								{#if ref.kind === 'tag'}<Tag class="h-3 w-3" />{:else}<GitBranch class="h-3 w-3" />{/if}{ref.name}
							</a>
						{/if}
						{#if run.pullRequestNumber}
							<a href={resolve('/[namespace]/[name]/pulls/[number]', { namespace, name, number: String(run.pullRequestNumber) })} class="hover:text-primary">PR #{run.pullRequestNumber}</a>
						{/if}
						{#if run.triggerUser}
							<span>by <a href={resolve('/[namespace]', { namespace: run.triggerUser })} class="text-foreground/80 hover:text-primary">{run.triggerUser}</a></span>
						{/if}
						{#if created}
							<span title={created.toLocaleString()}>{relativeTime(created)}</span>
						{/if}
						{#if runDuration !== undefined}
							<span class="inline-flex items-center gap-1 tabular-nums"><Timer class="h-3 w-3" />{formatDuration(runDuration)}</span>
						{/if}
						<span>attempt {attempt}</span>
					</div>
				</div>
				<PermissionGate allowed={canWrite}>
					<div class="flex items-center gap-2 flex-wrap">
						{#if run.needsApproval}
							<Button size="sm" class="h-8" onclick={() => act('approve')} disabled={!!busy}>
								{#if busy === 'approve'}<Loader2 class="h-3.5 w-3.5 mr-1.5 animate-spin" />{:else}<ShieldAlert class="h-3.5 w-3.5 mr-1.5" />{/if}Approve and Run
							</Button>
						{/if}
						{#if active}
							<Button variant="outline" size="sm" class="h-8 text-destructive hover:text-destructive" onclick={() => (cancelOpen = true)} disabled={!!busy}>
								<Ban class="h-3.5 w-3.5 mr-1.5" />Cancel Run
							</Button>
						{:else}
							<DropdownMenu>
								<DropdownMenuTrigger>
									{#snippet child({ props })}
										<Button {...props} variant="outline" size="sm" class="h-8" disabled={!!busy}>
											{#if busy.startsWith('rerun')}<Loader2 class="h-3.5 w-3.5 mr-1.5 animate-spin" />{:else}<RotateCcw class="h-3.5 w-3.5 mr-1.5" />{/if}Re-run<ChevronDown class="h-3.5 w-3.5 ml-1.5" />
										</Button>
									{/snippet}
								</DropdownMenuTrigger>
								<DropdownMenuContent align="end">
									<DropdownMenuItem onSelect={() => act('rerun')}>Re-run all jobs</DropdownMenuItem>
									<DropdownMenuItem onSelect={() => act('rerunFailed')} disabled={!anyFailed}>Re-run failed jobs</DropdownMenuItem>
								</DropdownMenuContent>
							</DropdownMenu>
						{/if}
					</div>
				</PermissionGate>
			</div>
			{#if run.needsApproval}
				<div class="mt-3 rounded-lg border border-amber-500/30 bg-amber-500/5 px-3 py-2 text-[12.5px] flex items-start gap-2">
					<ShieldAlert class="h-4 w-4 text-amber-500 shrink-0 mt-0.5" />
					<span>This run comes from a fork pull request and waits for a maintainer with write access on actions to approve it.</span>
				</div>
			{/if}
		</div>

		<div class="grid gap-6 lg:grid-cols-[18rem_minmax(0,1fr)]">
			<aside class="min-w-0 space-y-4">
				<div class="lg:hidden -mx-1 px-1 overflow-x-auto">
					<div class="flex items-center gap-2 w-max">
						{#each run.jobs as job (job.id)}
							<a href={resolve('/[namespace]/[name]/actions/runs/[id]/jobs/[jobId]', { namespace, name, id, jobId: job.id })} class="inline-flex items-center gap-1.5 rounded-lg border border-border/60 px-2.5 py-1.5 text-[12.5px] hover:border-primary/40 whitespace-nowrap">
								<RunStatusIcon status={job.status} conclusion={job.conclusion} class="h-3.5 w-3.5" />
								<span class="truncate max-w-40">{job.name}</span>
							</a>
						{/each}
						{#if run.jobs.length === 0}
							<span class="text-[12.5px] text-muted-foreground">No jobs yet.</span>
						{/if}
					</div>
				</div>

				<div class="hidden lg:block space-y-4">
					<div>
						<div class="text-[11px] font-semibold tracking-wider uppercase text-muted-foreground px-1 pb-1.5">Jobs</div>
						<div class="rounded-xl border border-border/60 divide-y divide-border/60">
							{#each run.jobs as job (job.id)}
								{@render jobRow(job)}
							{/each}
							{#if run.jobs.length === 0}
								<div class="px-3 py-3 text-[12.5px] text-muted-foreground">No jobs yet.</div>
							{/if}
						</div>
					</div>

					{#if stages.length > 1}
						<div>
							<div class="text-[11px] font-semibold tracking-wider uppercase text-muted-foreground px-1 pb-1.5 inline-flex items-center gap-1"><Layers class="h-3 w-3" />Dependency order</div>
							<div class="rounded-xl border border-border/60 px-3 py-2 space-y-2">
								{#each stages as stage, i (i)}
									<div class="flex items-start gap-2 text-[12px]">
										<span class="text-muted-foreground tabular-nums shrink-0 w-12">Stage {i + 1}</span>
										<div class="flex flex-wrap gap-1">
											{#each stage as job (job.id)}
												<a href={resolve('/[namespace]/[name]/actions/runs/[id]/jobs/[jobId]', { namespace, name, id, jobId: job.id })} class="inline-flex items-center gap-1 rounded-md border border-border/60 px-1.5 py-0.5 hover:border-primary/40">
													<RunStatusIcon status={job.status} conclusion={job.conclusion} class="h-3 w-3" />
													<span class="truncate max-w-40">{job.name}</span>
												</a>
											{/each}
										</div>
									</div>
								{/each}
							</div>
						</div>
					{/if}
				</div>
			</aside>

			<section class="space-y-5 min-w-0">
				{#if annotations.length > 0}
					<div>
						<div class="section-header mb-2">
							<h2 class="text-[13px] font-semibold flex items-center gap-2 flex-wrap">
								Annotations
								{#if errors.length}<Badge variant="outline" class="text-[10px] h-4.5 py-0 border-destructive/40 text-destructive">{errors.length} error{errors.length === 1 ? '' : 's'}</Badge>{/if}
								{#if warnings.length}<Badge variant="outline" class="text-[10px] h-4.5 py-0 border-amber-500/40 text-amber-600 dark:text-amber-400">{warnings.length} warning{warnings.length === 1 ? '' : 's'}</Badge>{/if}
								{#if notices.length}<Badge variant="outline" class="text-[10px] h-4.5 py-0">{notices.length} notice{notices.length === 1 ? '' : 's'}</Badge>{/if}
							</h2>
						</div>
						<div class="rounded-xl border border-border/60 divide-y divide-border/60">
							{#each errors as a, i (`e${i}`)}{@render annotationRow(a)}{/each}
							{#each warnings as a, i (`w${i}`)}{@render annotationRow(a)}{/each}
							{#each notices as a, i (`n${i}`)}{@render annotationRow(a)}{/each}
						</div>
					</div>
				{/if}

				<div>
					<h2 class="text-[13px] font-semibold mb-2 flex items-center gap-2"><Package class="h-3.5 w-3.5 text-muted-foreground" />Artifacts <span class="text-muted-foreground font-normal tabular-nums">{artifacts.length}</span></h2>
					{#if artifacts.length === 0}
						<div class="rounded-xl border border-dashed border-border/60 px-4 py-5 text-[12.5px] text-muted-foreground">
							No artifacts. Steps that use actions/upload-artifact publish files here.
						</div>
					{:else}
						<div class="rounded-xl border border-border/60 divide-y divide-border/60">
							{#each artifacts as a (a.id)}
								<div class="flex items-center gap-3 px-3 py-2.5 flex-wrap">
									<Package class="h-4 w-4 text-muted-foreground shrink-0" />
									<div class="flex-1 min-w-0">
										<div class="text-[13px] font-medium truncate">{a.name}</div>
										<div class="text-[11.5px] text-muted-foreground flex items-center gap-2 flex-wrap">
											<span>{formatBytes(Number(a.size))}</span>
											{#if expires(a)}<span>{expires(a)}</span>{/if}
										</div>
									</div>
									{#if a.downloadUrl}
										<Button variant="outline" size="sm" class="h-7 text-[12px]" href={a.downloadUrl} download>
											<Download class="h-3.5 w-3.5 mr-1" />Download
										</Button>
									{:else}
										<span class="text-[11.5px] text-muted-foreground">Expired</span>
									{/if}
								</div>
							{/each}
						</div>
					{/if}
				</div>

				<div>
					<h2 class="text-[13px] font-semibold mb-2">Job details</h2>
					<div class="rounded-xl border border-border/60 divide-y divide-border/60">
						{#each run.jobs as job (job.id)}
							{@const done = job.steps.filter((s) => s.conclusion === ActionsConclusion.SUCCESS).length}
							{@const failed = job.steps.filter((s) => s.conclusion === ActionsConclusion.FAILURE || s.conclusion === ActionsConclusion.TIMED_OUT).length}
							<a href={resolve('/[namespace]/[name]/actions/runs/[id]/jobs/[jobId]', { namespace, name, id, jobId: job.id })} class="flex items-center gap-3 px-3 py-2.5 hover:bg-muted/40 transition-colors">
								<RunStatusIcon status={job.status} conclusion={job.conclusion} />
								<div class="flex-1 min-w-0">
									<div class="text-[13px] font-medium truncate">{job.name}</div>
									<div class="text-[11.5px] text-muted-foreground flex items-center gap-2 flex-wrap">
										<span>{stateLabels[runState(job.status, job.conclusion)]}</span>
										{#if job.steps.length}<span>{done}/{job.steps.length} steps{failed ? `, ${failed} failed` : ''}</span>{/if}
										{#if job.runnerName}<span>on {job.runnerName}</span>{/if}
										{#each job.runsOn as l (l)}<Badge variant="outline" class="text-[10px] h-4 py-0 font-mono font-normal">{l}</Badge>{/each}
										{#if job.attempt > 1}<span>attempt {job.attempt}</span>{/if}
									</div>
								</div>
								{#if jobDuration(job)}
									<span class="text-[11.5px] text-muted-foreground tabular-nums shrink-0">{jobDuration(job)}</span>
								{/if}
							</a>
						{/each}
					</div>
				</div>
			</section>
		</div>
	{/if}
</div>

<ConfirmDialog bind:open={cancelOpen} title="Cancel run" confirmLabel="Cancel Run" cancelLabel="Keep running" onConfirm={() => act('cancel')} loading={busy === 'cancel'} icon={Ban}>
	{#snippet description()}
		Running jobs are stopped and queued jobs are dropped. Finished jobs keep their results.
	{/snippet}
</ConfirmDialog>
