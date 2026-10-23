<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import {
		ArrowLeft,
		Ban,
		RotateCcw,
		ShieldAlert,
		GitBranch,
		Tag,
		Timer,
		Package,
		Download,
		AlertCircle,
		AlertTriangle,
		Info,
		ChevronDown,
		Loader2,
		Play,
		Layers,
		Container,
		Pin
	} from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import {
		DropdownMenu,
		DropdownMenuContent,
		DropdownMenuItem,
		DropdownMenuTrigger
	} from '$lib/components/ui/dropdown-menu';
	import EmptyState from '$lib/components/empty-state.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import CommitBadge from '$lib/components/git/commit-badge.svelte';
	import RunStatusIcon from '$lib/components/git/run-status-icon.svelte';
	import { rpcClient, silentCallOptions, backgroundCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import {
		ActionsConclusion,
		type Job,
		type RunArtifact,
		type RunImage,
		type WorkflowRun
	} from '$lib/proto/distroface/v1/actions_pb';
	import {
		durationOf,
		eventLabel,
		formatDuration,
		isActive,
		parseAnnotations,
		refInfo,
		runState,
		runTitle,
		stateLabels,
		type Annotation
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
	let images = $state<RunImage[]>([]);
	let loading = $state(true);
	let missing = $state(false);
	let now = $state(Date.now());
	let seq = 0;

	async function load(silent = false) {
		const opts = silent ? backgroundCallOptions : silentCallOptions;
		const mine = ++seq;
		if (!silent) loading = true;
		try {
			const [r, a, im] = await Promise.all([
				rpcClient.actions.getWorkflowRun({ namespace, name, id }, opts),
				rpcClient.actions
					.listRunArtifacts({ namespace, name, runId: id }, opts)
					.catch(() => undefined),
				rpcClient.actions.listRunImages({ namespace, name, runId: id }, opts).catch(() => undefined)
			]);
			if (mine !== seq) return;
			run = r.run;
			artifacts = a?.artifacts ?? [];
			images = im?.images ?? [];
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
		!!run &&
			run.jobs.some(
				(j) =>
					j.conclusion === ActionsConclusion.FAILURE || j.conclusion === ActionsConclusion.TIMED_OUT
			)
	);

	function jobDuration(j: Job): string {
		const ms = durationOf(j.startedAt, j.finishedAt, now);
		return ms === undefined ? '' : formatDuration(ms);
	}

	// Jobs bucketed by longest needs chain
	const stages = $derived.by((): Job[][] => {
		const jobs = run?.jobs ?? [];
		const depth: Record<string, number> = {};
		const deps = (key: string) =>
			jobs.filter((j) => j.name === key || j.name.startsWith(key + ' ('));
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
		for (const j of run?.jobs ?? [])
			for (const a of parseAnnotations(j.annotations)) out.push({ ...a, job: j });
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
				toast.success(
					kind === 'rerunFailed' ? 'Failed jobs queued again' : 'All jobs queued again'
				);
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

	function shortDigest(d: string): string {
		const hex = d.startsWith('sha256:') ? d.slice(7) : d;
		return hex.slice(0, 12);
	}

	function platformList(img: RunImage): string {
		// eslint-disable-next-line svelte/prefer-svelte-reactivity
		const seen = new Set<string>();
		for (const p of img.platforms) {
			const v = p.variant ? `/${p.variant}` : '';
			seen.add(`${p.os}/${p.architecture}${v}`);
		}
		return [...seen].join(', ');
	}
</script>

{#snippet annotationRow(a: Annotation & { job?: Job })}
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
				{#if a.job}
					<a
						href={resolve('/[namespace]/[name]/actions/runs/[id]/jobs/[jobId]', {
							namespace,
							name,
							id,
							jobId: a.job.id
						})}
						class="font-medium text-foreground hover:text-primary">{a.job.name}</a
					>
				{:else}
					<span class="font-medium text-foreground">Workflow</span>
				{/if}
				{#if a.title}<span class="font-medium text-foreground">{a.title}</span>{/if}
				{#if location(a)}<span class="font-mono break-all">{location(a)}</span>{/if}
			</div>
			<div class="wrap-break-word whitespace-pre-wrap">{a.message}</div>
		</div>
	</div>
{/snippet}

{#snippet jobRow(job: Job)}
	{@const dur = jobDuration(job)}
	<a
		href={resolve('/[namespace]/[name]/actions/runs/[id]/jobs/[jobId]', {
			namespace,
			name,
			id,
			jobId: job.id
		})}
		class="flex items-start gap-2.5 px-4 py-2.5 transition-colors hover:bg-muted/30"
	>
		<div class="pt-0.5"><RunStatusIcon status={job.status} conclusion={job.conclusion} /></div>
		<div class="min-w-0 flex-1">
			<div class="truncate text-sm font-medium">{job.name}</div>
			{#if job.needs.length > 0}
				<div class="truncate text-2xs text-muted-foreground">after {job.needs.join(', ')}</div>
			{/if}
		</div>
		{#if dur}
			<span class="shrink-0 text-xs text-muted-foreground tabular-nums">{dur}</span>
		{/if}
	</a>
{/snippet}

<div class="space-y-5">
	<a
		href={resolve('/[namespace]/[name]/actions', { namespace, name })}
		class="inline-flex items-center gap-1 text-sm text-muted-foreground hover:text-primary"
	>
		<ArrowLeft class="size-3.5" />All runs
	</a>

	{#if loading && !run}
		<div class="space-y-4">
			<Skeleton class="h-16 w-full rounded-lg" />
			<div class="grid gap-6 lg:grid-cols-[18rem_minmax(0,1fr)]">
				<Skeleton class="h-48 w-full rounded-lg" />
				<Skeleton class="h-64 w-full rounded-lg" />
			</div>
		</div>
	{:else if missing || !run}
		<EmptyState
			icon={Play}
			message="Run not found"
			description="This run does not exist or was pruned."
		/>
	{:else}
		<div class="rounded-lg border bg-card px-5 py-4">
			<div class="flex flex-wrap items-start gap-3">
				<div class="pt-0.5">
					<RunStatusIcon
						status={run.status}
						conclusion={run.conclusion}
						needsApproval={run.needsApproval}
						class="size-6"
					/>
				</div>
				<div class="min-w-0 flex-1">
					<h1 class="flex flex-wrap items-center gap-2 text-lg font-semibold tracking-tight">
						<span class="truncate">{runTitle(run)}</span>
						<span class="font-normal text-muted-foreground tabular-nums">#{run.number}</span>
						<Badge variant="outline"
							>{stateLabels[runState(run.status, run.conclusion, run.needsApproval)]}</Badge
						>
					</h1>
					<div
						class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground"
					>
						<a
							href={resolve(
								`${actionsHref}?workflow=${encodeURIComponent(run.workflowPath)}` as `/${string}`
							)}
							class="font-medium text-foreground hover:text-primary"
							>{run.workflowName || run.workflowPath}</a
						>
						<Badge variant="secondary">{eventLabel(run.event)}</Badge>
						{#if run.sha}
							<CommitBadge sha={run.sha} {namespace} {name} />
						{/if}
						{#if ref.name}
							<a
								href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', {
									namespace,
									name,
									ref: encodeURIComponent(ref.name),
									path: ''
								})}
								class="inline-flex items-center gap-1 font-mono hover:text-primary"
								title={run.ref}
							>
								{#if ref.kind === 'tag'}<Tag class="size-3" />{:else}<GitBranch
										class="size-3"
									/>{/if}{ref.name}
							</a>
						{/if}
						{#if run.pullRequestNumber}
							<a
								href={resolve('/[namespace]/[name]/pulls/[number]', {
									namespace,
									name,
									number: String(run.pullRequestNumber)
								})}
								class="hover:text-primary">PR #{run.pullRequestNumber}</a
							>
						{/if}
						{#if run.triggerUser}
							<span
								>by <a
									href={resolve('/[namespace]', { namespace: run.triggerUser })}
									class="text-foreground hover:text-primary">{run.triggerUser}</a
								></span
							>
						{/if}
						{#if created}
							<span title={created.toLocaleString()}>{relativeTime(created)}</span>
						{/if}
						{#if runDuration !== undefined}
							<span class="inline-flex items-center gap-1 tabular-nums"
								><Timer class="size-3" />{formatDuration(runDuration)}</span
							>
						{/if}
						<span>attempt {attempt}</span>
					</div>
				</div>
				<PermissionGate allowed={canWrite}>
					<div class="flex flex-wrap items-center gap-2">
						{#if run.needsApproval}
							<Button onclick={() => act('approve')} disabled={!!busy}>
								{#if busy === 'approve'}<Loader2 class="animate-spin" />{:else}<ShieldAlert
									/>{/if}Approve and run
							</Button>
						{/if}
						{#if active}
							<Button
								variant="outline-destructive"
								onclick={() => (cancelOpen = true)}
								disabled={!!busy}
							>
								<Ban />Cancel run
							</Button>
						{:else}
							<DropdownMenu>
								<DropdownMenuTrigger>
									{#snippet child({ props })}
										<Button {...props} variant="outline" disabled={!!busy}>
											{#if busy.startsWith('rerun')}<Loader2
													class="animate-spin"
												/>{:else}<RotateCcw />{/if}Re-run<ChevronDown class="opacity-70" />
										</Button>
									{/snippet}
								</DropdownMenuTrigger>
								<DropdownMenuContent align="end">
									<DropdownMenuItem onSelect={() => act('rerun')}>Re-run all jobs</DropdownMenuItem>
									<DropdownMenuItem onSelect={() => act('rerunFailed')} disabled={!anyFailed}
										>Re-run failed jobs</DropdownMenuItem
									>
								</DropdownMenuContent>
							</DropdownMenu>
						{/if}
					</div>
				</PermissionGate>
			</div>
			{#if run.needsApproval}
				<Alert variant="warning" class="mt-3">
					<ShieldAlert />
					<AlertDescription
						>This run comes from a fork pull request and waits for a maintainer with write access on
						actions to approve it.</AlertDescription
					>
				</Alert>
			{/if}
		</div>

		<div class="grid gap-6 lg:grid-cols-[18rem_minmax(0,1fr)]">
			<aside class="min-w-0 space-y-4">
				<div class="-mx-1 overflow-x-auto px-1 lg:hidden">
					<div class="flex w-max items-center gap-2">
						{#each run.jobs as job (job.id)}
							<a
								href={resolve('/[namespace]/[name]/actions/runs/[id]/jobs/[jobId]', {
									namespace,
									name,
									id,
									jobId: job.id
								})}
								class="inline-flex items-center gap-1.5 rounded-md border px-2.5 py-1.5 text-xs whitespace-nowrap transition-colors hover:border-border-strong hover:bg-muted/30"
							>
								<RunStatusIcon status={job.status} conclusion={job.conclusion} class="size-3.5" />
								<span class="max-w-40 truncate">{job.name}</span>
							</a>
						{/each}
						{#if run.jobs.length === 0}
							<span class="text-xs text-muted-foreground">No jobs yet</span>
						{/if}
					</div>
				</div>

				<div class="hidden space-y-4 lg:block">
					<div>
						<div class="eyebrow px-1 pb-1.5">Jobs</div>
						<div class="box divide-y">
							{#each run.jobs as job (job.id)}
								{@render jobRow(job)}
							{/each}
							{#if run.jobs.length === 0}
								<div class="px-4 py-3 text-xs text-muted-foreground">No jobs yet</div>
							{/if}
						</div>
					</div>

					{#if stages.length > 1}
						<div>
							<div class="eyebrow inline-flex items-center gap-1 px-1 pb-1.5">
								<Layers class="size-3" />Dependency order
							</div>
							<div class="box space-y-2 px-3 py-2">
								{#each stages as stage, i (i)}
									<div class="flex items-start gap-2 text-xs">
										<span class="w-12 shrink-0 text-muted-foreground tabular-nums"
											>Stage {i + 1}</span
										>
										<div class="flex flex-wrap gap-1">
											{#each stage as job (job.id)}
												<a
													href={resolve('/[namespace]/[name]/actions/runs/[id]/jobs/[jobId]', {
														namespace,
														name,
														id,
														jobId: job.id
													})}
													class="inline-flex items-center gap-1 rounded-md border px-1.5 py-0.5 transition-colors hover:border-border-strong hover:bg-muted/30"
												>
													<RunStatusIcon
														status={job.status}
														conclusion={job.conclusion}
														class="size-3"
													/>
													<span class="max-w-40 truncate">{job.name}</span>
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

			<section class="min-w-0 space-y-5">
				{#if annotations.length > 0}
					<div>
						<h2 class="mb-2 flex flex-wrap items-center gap-2 text-sm font-semibold">
							Annotations
							{#if errors.length}<Badge variant="destructive"
									>{errors.length} error{errors.length === 1 ? '' : 's'}</Badge
								>{/if}
							{#if warnings.length}<Badge variant="warning"
									>{warnings.length} warning{warnings.length === 1 ? '' : 's'}</Badge
								>{/if}
							{#if notices.length}<Badge variant="outline"
									>{notices.length} notice{notices.length === 1 ? '' : 's'}</Badge
								>{/if}
						</h2>
						<div class="box divide-y">
							{#each errors as a, i (`e${i}`)}{@render annotationRow(a)}{/each}
							{#each warnings as a, i (`w${i}`)}{@render annotationRow(a)}{/each}
							{#each notices as a, i (`n${i}`)}{@render annotationRow(a)}{/each}
						</div>
					</div>
				{/if}

				<div>
					<h2 class="mb-2 flex items-center gap-2 text-sm font-semibold">
						<Container class="size-3.5 text-muted-foreground" />Images
						<span class="font-normal text-muted-foreground tabular-nums">{images.length}</span>
					</h2>
					{#if images.length === 0}
						<div class="box">
							<div class="box-empty">
								No images. Tags pushed to this instance with the job token, for example by
								docker/build-push-action, are listed here.
							</div>
						</div>
					{:else}
						<div class="box divide-y">
							{#each images as img (img.id)}
								<div class="flex flex-wrap items-center gap-3 px-4 py-3">
									<Container class="size-4 shrink-0 text-muted-foreground" />
									<div class="min-w-0 flex-1">
										<div class="flex flex-wrap items-center gap-2">
											<a
												href={resolve('/images/[namespace]/[...name]', {
													namespace: img.namespace,
													name: img.name
												})}
												class="truncate font-mono text-sm font-medium hover:text-primary"
												>{img.fullName}:{img.tag}</a
											>
											{#if img.pinned}
												<Badge variant="secondary" class="gap-1"
													><Pin class="size-3" />release</Badge
												>
											{/if}
										</div>
										<div class="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
											{#if img.digest}
												<span class="font-mono" title={img.digest}>{shortDigest(img.digest)}</span>
											{/if}
											{#if img.sizeBytes}<span>{formatBytes(Number(img.sizeBytes))}</span>{/if}
											{#if platformList(img)}<span>{platformList(img)}</span>{/if}
											{#if img.jobName}
												<a
													href={resolve('/[namespace]/[name]/actions/runs/[id]/jobs/[jobId]', {
														namespace,
														name,
														id,
														jobId: img.jobId
													})}
													class="hover:text-primary">from {img.jobName}</a
												>
											{/if}
										</div>
									</div>
									<div class="flex items-center gap-1">
										<code class="code-inline hidden max-w-80 truncate lg:inline"
											>{img.pullCommand}</code
										>
										<CopyButton text={img.pullCommand} label="Pull command copied" />
									</div>
								</div>
							{/each}
						</div>
					{/if}
				</div>

				<div>
					<h2 class="mb-2 flex items-center gap-2 text-sm font-semibold">
						<Package class="size-3.5 text-muted-foreground" />Artifacts
						<span class="font-normal text-muted-foreground tabular-nums">{artifacts.length}</span>
					</h2>
					{#if artifacts.length === 0}
						<div class="box">
							<div class="box-empty">
								No artifacts. Steps that use actions/upload-artifact publish files here.
							</div>
						</div>
					{:else}
						<div class="box divide-y">
							{#each artifacts as a (a.id)}
								<div class="flex flex-wrap items-center gap-3 px-4 py-3">
									<Package class="size-4 shrink-0 text-muted-foreground" />
									<div class="min-w-0 flex-1">
										<div class="truncate text-sm font-medium">{a.name}</div>
										<div class="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
											<span>{formatBytes(Number(a.size))}</span>
											{#if expires(a)}<span>{expires(a)}</span>{/if}
										</div>
									</div>
									{#if a.downloadUrl}
										<Button variant="outline" size="sm" href={a.downloadUrl} download>
											<Download />Download
										</Button>
									{:else}
										<span class="text-xs text-muted-foreground">Expired</span>
									{/if}
								</div>
							{/each}
						</div>
					{/if}
				</div>

				<div>
					<h2 class="mb-2 text-sm font-semibold">Job details</h2>
					<div class="box divide-y">
						{#each run.jobs as job (job.id)}
							{@const done = job.steps.filter(
								(s) => s.conclusion === ActionsConclusion.SUCCESS
							).length}
							{@const failed = job.steps.filter(
								(s) =>
									s.conclusion === ActionsConclusion.FAILURE ||
									s.conclusion === ActionsConclusion.TIMED_OUT
							).length}
							<a
								href={resolve('/[namespace]/[name]/actions/runs/[id]/jobs/[jobId]', {
									namespace,
									name,
									id,
									jobId: job.id
								})}
								class="flex items-center gap-3 px-4 py-3 transition-colors hover:bg-muted/30"
							>
								<RunStatusIcon status={job.status} conclusion={job.conclusion} />
								<div class="min-w-0 flex-1">
									<div class="truncate text-sm font-medium">{job.name}</div>
									<div class="flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
										<span>{stateLabels[runState(job.status, job.conclusion)]}</span>
										{#if job.steps.length}<span
												>{done}/{job.steps.length} steps{failed ? `, ${failed} failed` : ''}</span
											>{/if}
										{#if job.runnerName}<span>on {job.runnerName}</span>{/if}
										{#each job.runsOn as l (l)}<Badge variant="outline" class="font-mono">{l}</Badge
											>{/each}
										{#if job.attempt > 1}<span>attempt {job.attempt}</span>{/if}
									</div>
								</div>
								{#if jobDuration(job)}
									<span class="shrink-0 text-xs text-muted-foreground tabular-nums"
										>{jobDuration(job)}</span
									>
								{/if}
							</a>
						{/each}
					</div>
				</div>
			</section>
		</div>
	{/if}
</div>

<ConfirmDialog
	bind:open={cancelOpen}
	title="Cancel run"
	confirmLabel="Cancel run"
	cancelLabel="Keep running"
	onConfirm={() => act('cancel')}
	loading={busy === 'cancel'}
	icon={Ban}
>
	{#snippet description()}
		Running jobs are stopped and queued jobs are dropped. Finished jobs keep their results.
	{/snippet}
</ConfirmDialog>
