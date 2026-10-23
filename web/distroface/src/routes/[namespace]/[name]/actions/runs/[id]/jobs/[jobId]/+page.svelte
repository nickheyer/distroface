<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import {
		ArrowLeft,
		Timer,
		Cpu,
		AlertCircle,
		AlertTriangle,
		Info,
		Play,
		ListChecks
	} from '@lucide/svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import {
		Table,
		TableBody,
		TableCell,
		TableHead,
		TableHeader,
		TableRow
	} from '$lib/components/ui/table';
	import EmptyState from '$lib/components/empty-state.svelte';
	import RunStatusIcon from '$lib/components/git/run-status-icon.svelte';
	import JobLog from '$lib/components/git/job-log.svelte';
	import { rpcClient, silentCallOptions, backgroundCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import type { Job, JobStep, WorkflowRun } from '$lib/proto/distroface/v1/actions_pb';
	import {
		durationOf,
		formatDuration,
		isActive,
		parseAnnotations,
		parseOutputs,
		runState,
		runTitle,
		stateLabels,
		type Annotation
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
		const opts = silent ? backgroundCallOptions : silentCallOptions;
		const mine = ++seq;
		if (!silent) loading = true;
		try {
			const [j, r] = await Promise.all([
				rpcClient.actions.getJob({ namespace, name, id: jobId }, opts),
				rpcClient.actions
					.getWorkflowRun({ namespace, name, id: runId }, opts)
					.catch(() => undefined)
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
	<div class="flex flex-wrap items-center gap-2 text-sm text-muted-foreground">
		<a href={resolve('/[namespace]/[name]/actions', { namespace, name })} class="hover:text-primary"
			>Actions</a
		>
		<span class="text-muted-foreground/50">/</span>
		<a
			href={resolve('/[namespace]/[name]/actions/runs/[id]', { namespace, name, id: runId })}
			class="inline-flex min-w-0 items-center gap-1 hover:text-primary"
		>
			<ArrowLeft class="size-3.5 shrink-0" /><span class="truncate"
				>{run ? `${runTitle(run)} #${run.number}` : 'Run'}</span
			>
		</a>
	</div>

	{#if loading && !job}
		<div class="space-y-4">
			<Skeleton class="h-16 w-full rounded-lg" />
			<div class="grid gap-6 lg:grid-cols-[18rem_minmax(0,1fr)]">
				<Skeleton class="h-64 w-full rounded-lg" />
				<Skeleton class="h-96 w-full rounded-lg" />
			</div>
		</div>
	{:else if missing || !job}
		<EmptyState
			icon={Play}
			message="Job not found"
			description="This job does not exist or its run was pruned."
		/>
	{:else}
		<div class="rounded-lg border bg-card px-5 py-4">
			<div class="flex flex-wrap items-start gap-3">
				<div class="pt-0.5">
					<RunStatusIcon status={job.status} conclusion={job.conclusion} class="size-6" />
				</div>
				<div class="min-w-0 flex-1">
					<h1 class="flex flex-wrap items-center gap-2 text-lg font-semibold tracking-tight">
						<span class="truncate">{job.name}</span>
						<Badge variant="outline">{stateLabels[runState(job.status, job.conclusion)]}</Badge>
					</h1>
					<div
						class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground"
					>
						{#if run}
							<span class="font-medium text-foreground">{run.workflowName || run.workflowPath}</span
							>
						{/if}
						{#if job.runnerName}
							<span class="inline-flex items-center gap-1"
								><Cpu class="size-3" />{job.runnerName}</span
							>
						{/if}
						{#each job.runsOn as l (l)}
							<Badge variant="outline" class="font-mono">{l}</Badge>
						{/each}
						{#if started}
							<span title={started.toLocaleString()}>started {relativeTime(started)}</span>
						{/if}
						{#if jobDuration !== undefined}
							<span class="inline-flex items-center gap-1 tabular-nums"
								><Timer class="size-3" />{formatDuration(jobDuration)}</span
							>
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
				<div class="space-y-3 lg:hidden">
					{#if run && run.jobs.length > 1}
						<div class="-mx-1 overflow-x-auto px-1">
							<div class="flex w-max items-center gap-2">
								{#each run.jobs as j (j.id)}
									<a
										href={resolve('/[namespace]/[name]/actions/runs/[id]/jobs/[jobId]', {
											namespace,
											name,
											id: runId,
											jobId: j.id
										})}
										class={cn(
											'inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-xs whitespace-nowrap transition-colors',
											j.id === job.id
												? 'border-border-strong bg-muted font-medium'
												: 'hover:border-border-strong hover:bg-muted/30'
										)}
									>
										<RunStatusIcon status={j.status} conclusion={j.conclusion} class="size-3.5" />
										<span class="max-w-40 truncate">{j.name}</span>
									</a>
								{/each}
							</div>
						</div>
					{/if}
					{#if job.steps.length > 0}
						<Select type="single" onValueChange={jumpToStep}>
							<SelectTrigger class="w-full" aria-label="Jump to step">
								<span class="inline-flex items-center gap-2"
									><ListChecks class="size-3.5 text-muted-foreground" />Jump to step</span
								>
							</SelectTrigger>
							<SelectContent>
								{#each job.steps as s (s.number)}
									<SelectItem value={String(s.number)}>{s.number}. {s.name}</SelectItem>
								{/each}
							</SelectContent>
						</Select>
					{/if}
				</div>

				<div class="hidden space-y-4 lg:block">
					{#if run && run.jobs.length > 1}
						<div>
							<div class="eyebrow px-1 pb-1.5">Jobs</div>
							<div class="box divide-y">
								{#each run.jobs as j (j.id)}
									<a
										href={resolve('/[namespace]/[name]/actions/runs/[id]/jobs/[jobId]', {
											namespace,
											name,
											id: runId,
											jobId: j.id
										})}
										class={cn(
											'flex items-center gap-2 px-4 py-2 text-sm transition-colors hover:bg-muted/30',
											j.id === job.id && 'bg-muted font-medium'
										)}
									>
										<RunStatusIcon status={j.status} conclusion={j.conclusion} class="size-3.5" />
										<span class="truncate">{j.name}</span>
									</a>
								{/each}
							</div>
						</div>
					{/if}

					<div>
						<div class="eyebrow inline-flex items-center gap-1 px-1 pb-1.5">
							<ListChecks class="size-3" />Steps
						</div>
						<div class="box divide-y">
							{#each job.steps as s (s.number)}
								{@const dur = stepDuration(s)}
								<button
									type="button"
									class="flex w-full items-center gap-2.5 px-4 py-2 text-left transition-colors hover:bg-muted/30"
									onclick={() => log?.scrollToStep(s.number)}
									title="Jump to step output"
								>
									<RunStatusIcon status={s.status} conclusion={s.conclusion} class="size-3.5" />
									<span class="w-5 shrink-0 text-2xs text-muted-foreground tabular-nums"
										>{s.number}</span
									>
									<span class="min-w-0 flex-1 truncate text-sm">{s.name}</span>
									{#if dur}
										<span class="shrink-0 text-2xs text-muted-foreground tabular-nums">{dur}</span>
									{/if}
								</button>
							{/each}
							{#if job.steps.length === 0}
								<div class="px-4 py-3 text-xs text-muted-foreground">
									{active ? 'Waiting for a runner' : 'No steps recorded'}
								</div>
							{/if}
						</div>
					</div>
				</div>
			</aside>

			<section class="min-w-0 space-y-5">
				<JobLog bind:this={log} {namespace} {name} {runId} jobId={job.id} {job} follow={active} />

				{#if outputs.length > 0}
					<div>
						<h2 class="mb-2 text-sm font-semibold">Outputs</h2>
						<div class="data-table">
							<Table>
								<TableHeader>
									<TableRow>
										<TableHead class="w-56">Name</TableHead>
										<TableHead>Value</TableHead>
									</TableRow>
								</TableHeader>
								<TableBody>
									{#each outputs as [k, v] (k)}
										<TableRow>
											<TableCell class="align-top font-mono font-medium">{k}</TableCell>
											<TableCell class="font-mono break-all whitespace-pre-wrap">{v}</TableCell>
										</TableRow>
									{/each}
								</TableBody>
							</Table>
						</div>
					</div>
				{/if}

				{#if annotations.length > 0}
					<div>
						<h2 class="mb-2 text-sm font-semibold">
							Annotations <span class="font-normal text-muted-foreground tabular-nums"
								>{annotations.length}</span
							>
						</h2>
						<div class="box divide-y">
							{#each annotations as a, i (i)}
								<div class="flex items-start gap-2 px-4 py-2.5 text-sm">
									{#if a.level === 'error'}
										<AlertCircle class="mt-0.5 size-4 shrink-0 text-destructive" />
									{:else if a.level === 'warning'}
										<AlertTriangle class="mt-0.5 size-4 shrink-0 text-warning" />
									{:else}
										<Info class="mt-0.5 size-4 shrink-0 text-info" />
									{/if}
									<div class="min-w-0 flex-1">
										<div class="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
											{#if a.title}<span class="font-medium text-foreground">{a.title}</span>{/if}
											{#if a.step}
												<button
													type="button"
													class="hover:text-primary"
													onclick={() => log?.scrollToStep(a.step ?? 0)}>step {a.step}</button
												>
											{/if}
											{#if location(a)}<span class="font-mono break-all">{location(a)}</span>{/if}
										</div>
										<div class="wrap-break-word whitespace-pre-wrap">{a.message}</div>
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
