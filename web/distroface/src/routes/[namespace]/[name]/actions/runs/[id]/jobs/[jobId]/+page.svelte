<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { ArrowLeft, Timer, Cpu, AlertCircle, AlertTriangle, Info, Play, ListChecks } from '@lucide/svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import EmptyState from '$lib/components/empty-state.svelte';
	import RunStatusIcon from '$lib/components/git/run-status-icon.svelte';
	import JobLog from '$lib/components/git/job-log.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import type { Job, JobStep, WorkflowRun } from '$lib/proto/distroface/v1/actions_pb';
	import {
		durationOf, formatDuration, isActive, parseAnnotations, parseOutputs, runState, runTitle, stateLabels, type Annotation
	} from '$lib/components/git/actions-state';
	import { cn, relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const runId = $derived(page.params.id ?? '');
	const jobId = $derived(page.params.jobId ?? '');

	$effect(() => {
		if (namespace && name) gitRepoStore.load(namespace, name);
	});

	let run = $state<WorkflowRun | undefined>();
	let job = $state<Job | undefined>();
	let loading = $state(true);
	let missing = $state(false);
	let now = $state(Date.now());
	let seq = 0;
	let log = $state<{ scrollToStep: (n: number) => void } | undefined>();

	async function load(silent = false) {
		const mine = ++seq;
		if (!silent) loading = true;
		try {
			const [j, r] = await Promise.all([
				rpcClient.actions.getJob({ namespace, name, id: jobId }, silentCallOptions),
				rpcClient.actions.getWorkflowRun({ namespace, name, id: runId }, silentCallOptions).catch(() => undefined)
			]);
			if (mine !== seq) return;
			job = j.job;
			run = r?.run;
			missing = !j.job;
		} catch {
			if (mine === seq && !silent) {
				job = undefined;
				missing = true;
			}
		} finally {
			if (mine === seq) loading = false;
		}
	}

	$effect(() => {
		void jobId;
		void runId;
		load();
	});

	const active = $derived(!!job && isActive(job.status));

	$effect(() => {
		if (!active) return;
		const poll = setInterval(() => load(true), 5000);
		const tick = setInterval(() => (now = Date.now()), 1000);
		return () => {
			clearInterval(poll);
			clearInterval(tick);
		};
	});

	const started = $derived(job?.startedAt ? timestampDate(job.startedAt) : undefined);
	const jobDuration = $derived(job ? durationOf(job.startedAt, job.finishedAt, now) : undefined);
	const outputs = $derived(job ? parseOutputs(job.outputs) : []);
	const annotations = $derived(job ? parseAnnotations(job.annotations) : []);

	function stepDuration(s: JobStep): string {
		const ms = durationOf(s.startedAt, s.finishedAt, now);
		return ms === undefined ? '' : formatDuration(ms);
	}

	function location(a: Annotation): string {
		if (!a.file) return '';
		return a.line ? `${a.file}:${a.line}${a.col ? ':' + a.col : ''}` : a.file;
	}

	function jumpToStep(v: string | undefined) {
		const n = Number(v);
		if (n > 0) log?.scrollToStep(n);
	}
</script>

<div class="space-y-5">
	<div class="flex items-center gap-2 text-[12.5px] text-muted-foreground flex-wrap">
		<a href={resolve('/[namespace]/[name]/actions', { namespace, name })} class="hover:text-primary">Actions</a>
		<span>/</span>
		<a href={resolve('/[namespace]/[name]/actions/runs/[id]', { namespace, name, id: runId })} class="inline-flex items-center gap-1 hover:text-primary min-w-0">
			<ArrowLeft class="h-3.5 w-3.5 shrink-0" /><span class="truncate">{run ? `${runTitle(run)} #${run.number}` : 'Run'}</span>
		</a>
	</div>

	{#if loading && !job}
		<div class="space-y-4">
			<Skeleton class="h-16 w-full rounded-xl" />
			<div class="grid gap-6 lg:grid-cols-[18rem_minmax(0,1fr)]">
				<Skeleton class="h-64 w-full rounded-xl" />
				<Skeleton class="h-96 w-full rounded-xl" />
			</div>
		</div>
	{:else if missing || !job}
		<EmptyState icon={Play} message="Job not found" description="This job does not exist or its run was pruned." />
	{:else}
		<div class="rounded-xl border border-border/60 bg-card px-5 py-4">
			<div class="flex items-start gap-3 flex-wrap">
				<div class="pt-0.5">
					<RunStatusIcon status={job.status} conclusion={job.conclusion} class="h-6 w-6" />
				</div>
				<div class="flex-1 min-w-0">
					<h1 class="text-lg font-semibold flex items-center gap-2 flex-wrap">
						<span class="truncate">{job.name}</span>
						<Badge variant="outline" class="text-[10.5px] h-5 py-0">{stateLabels[runState(job.status, job.conclusion)]}</Badge>
					</h1>
					<div class="flex items-center gap-x-3 gap-y-1 flex-wrap text-[12.5px] text-muted-foreground mt-1">
						{#if run}
							<span class="text-foreground/80 font-medium">{run.workflowName || run.workflowPath}</span>
						{/if}
						{#if job.runnerName}
							<span class="inline-flex items-center gap-1"><Cpu class="h-3 w-3" />{job.runnerName}</span>
						{/if}
						{#each job.runsOn as l (l)}
							<Badge variant="outline" class="text-[10px] h-4.5 py-0 font-mono font-normal">{l}</Badge>
						{/each}
						{#if started}
							<span title={started.toLocaleString()}>started {relativeTime(started)}</span>
						{/if}
						{#if jobDuration !== undefined}
							<span class="inline-flex items-center gap-1 tabular-nums"><Timer class="h-3 w-3" />{formatDuration(jobDuration)}</span>
						{/if}
						<span>attempt {job.attempt || 1}</span>
						{#if job.needs.length}
							<span>after {job.needs.join(', ')}</span>
						{/if}
					</div>
				</div>
			</div>
		</div>

		<div class="grid gap-6 lg:grid-cols-[18rem_minmax(0,1fr)]">
			<aside class="min-w-0 space-y-3">
				<div class="lg:hidden space-y-3">
					{#if run && run.jobs.length > 1}
						<div class="-mx-1 px-1 overflow-x-auto">
							<div class="flex items-center gap-2 w-max">
								{#each run.jobs as j (j.id)}
									<a
										href={resolve('/[namespace]/[name]/actions/runs/[id]/jobs/[jobId]', { namespace, name, id: runId, jobId: j.id })}
										class={cn('inline-flex items-center gap-1.5 rounded-lg border px-2.5 py-1.5 text-[12.5px] whitespace-nowrap hover:border-primary/40', j.id === job.id ? 'border-primary/40 bg-primary/5 font-medium' : 'border-border/60')}
									>
										<RunStatusIcon status={j.status} conclusion={j.conclusion} class="h-3.5 w-3.5" />
										<span class="truncate max-w-40">{j.name}</span>
									</a>
								{/each}
							</div>
						</div>
					{/if}
					{#if job.steps.length > 0}
						<Select type="single" onValueChange={jumpToStep}>
							<SelectTrigger class="w-full" aria-label="Jump to step">
								<span class="inline-flex items-center gap-2"><ListChecks class="h-3.5 w-3.5 text-muted-foreground" />Jump to step</span>
							</SelectTrigger>
							<SelectContent>
								{#each job.steps as s (s.number)}
									<SelectItem value={String(s.number)}>{s.number}. {s.name}</SelectItem>
								{/each}
							</SelectContent>
						</Select>
					{/if}
				</div>

				<div class="hidden lg:block space-y-4">
					{#if run && run.jobs.length > 1}
						<div>
							<div class="text-[11px] font-semibold tracking-wider uppercase text-muted-foreground px-1 pb-1.5">Jobs</div>
							<div class="rounded-xl border border-border/60 divide-y divide-border/60">
								{#each run.jobs as j (j.id)}
									<a
										href={resolve('/[namespace]/[name]/actions/runs/[id]/jobs/[jobId]', { namespace, name, id: runId, jobId: j.id })}
										class={cn('flex items-center gap-2 px-3 py-2 text-[12.5px] hover:bg-muted/40 transition-colors', j.id === job.id && 'bg-muted/60 font-medium')}
									>
										<RunStatusIcon status={j.status} conclusion={j.conclusion} class="h-3.5 w-3.5" />
										<span class="truncate">{j.name}</span>
									</a>
								{/each}
							</div>
						</div>
					{/if}

					<div>
						<div class="text-[11px] font-semibold tracking-wider uppercase text-muted-foreground px-1 pb-1.5 inline-flex items-center gap-1"><ListChecks class="h-3 w-3" />Steps</div>
						<div class="rounded-xl border border-border/60 divide-y divide-border/60">
							{#each job.steps as s (s.number)}
								{@const dur = stepDuration(s)}
								<button type="button" class="w-full flex items-center gap-2.5 px-3 py-2 text-left hover:bg-muted/40 transition-colors" onclick={() => log?.scrollToStep(s.number)} title="Jump to step output">
									<RunStatusIcon status={s.status} conclusion={s.conclusion} class="h-3.5 w-3.5" />
									<span class="text-[11px] text-muted-foreground tabular-nums w-5 shrink-0">{s.number}</span>
									<span class="flex-1 min-w-0 text-[12.5px] truncate">{s.name}</span>
									{#if dur}
										<span class="text-[11px] text-muted-foreground tabular-nums shrink-0">{dur}</span>
									{/if}
								</button>
							{/each}
							{#if job.steps.length === 0}
								<div class="px-3 py-3 text-[12.5px] text-muted-foreground">{active ? 'Waiting for a runner...' : 'No steps recorded.'}</div>
							{/if}
						</div>
					</div>
				</div>
			</aside>

			<section class="space-y-5 min-w-0">
				<JobLog bind:this={log} {namespace} {name} {runId} jobId={job.id} {job} follow={active} />

				{#if outputs.length > 0}
					<div>
						<h2 class="text-[13px] font-semibold mb-2">Outputs</h2>
						<div class="data-table overflow-x-auto">
							<table class="w-full text-[12.5px]">
								<thead>
									<tr class="bg-muted/30">
										<th class="th text-left w-56">Name</th>
										<th class="th text-left">Value</th>
									</tr>
								</thead>
								<tbody class="divide-y divide-border/60">
									{#each outputs as [k, v] (k)}
										<tr>
											<td class="px-3 py-2 font-mono font-medium align-top">{k}</td>
											<td class="px-3 py-2 font-mono whitespace-pre-wrap break-all">{v}</td>
										</tr>
									{/each}
								</tbody>
							</table>
						</div>
					</div>
				{/if}

				{#if annotations.length > 0}
					<div>
						<h2 class="text-[13px] font-semibold mb-2">Annotations <span class="text-muted-foreground font-normal tabular-nums">{annotations.length}</span></h2>
						<div class="rounded-xl border border-border/60 divide-y divide-border/60">
							{#each annotations as a, i (i)}
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
											{#if a.title}<span class="font-medium text-foreground/80">{a.title}</span>{/if}
											{#if a.step}
												<button type="button" class="hover:text-primary" onclick={() => log?.scrollToStep(a.step ?? 0)}>step {a.step}</button>
											{/if}
											{#if location(a)}<span class="font-mono break-all">{location(a)}</span>{/if}
										</div>
										<div class="whitespace-pre-wrap break-words">{a.message}</div>
									</div>
								</div>
							{/each}
						</div>
					</div>
				{/if}
			</section>
		</div>
	{/if}
</div>
