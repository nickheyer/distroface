<script lang="ts">
	import { page } from '$app/state';
	import { replaceState } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Play, Workflow as WorkflowIcon, Power, PowerOff, AlertCircle, FileCode2, Search } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import RunList from '$lib/components/git/run-list.svelte';
	import WorkflowDispatchDialog from '$lib/components/git/workflow-dispatch-dialog.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import { ActionsConclusion, ActionsStatus, type Workflow, type WorkflowRun } from '$lib/proto/distroface/v1/actions_pb';
	import { eventLabel, isActive } from '$lib/components/git/actions-state';
	import { cn, relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	const ALL = '__all';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);

	$effect(() => {
		if (namespace && name) gitRepoStore.load(namespace, name);
	});

	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.ACTIONS) && !repo?.isArchived);
	const unitEnabled = $derived(repo?.units?.actions !== false);
	const defaultBranch = $derived(repo?.defaultBranch ?? '');
	const actionsHref = $derived(resolve('/[namespace]/[name]/actions', { namespace, name }));

	let workflows = $state<Workflow[]>([]);
	let workflowsLoading = $state(true);
	let selected = $state(page.url.searchParams.get('workflow') ?? '');
	let runs = $state<WorkflowRun[]>([]);
	let runsLoading = $state(true);
	const pager = new Pager(25);

	let event = $state(ALL);
	let status = $state(ALL);
	let branch = $state('');
	let actor = $state('');
	let debounce: ReturnType<typeof setTimeout> | undefined;
	let seq = 0;

	const statusOptions = [
		{ value: ALL, label: 'Any status' },
		{ value: 'queued', label: 'Queued' },
		{ value: 'waiting', label: 'Waiting' },
		{ value: 'in_progress', label: 'In progress' },
		{ value: 'success', label: 'Success' },
		{ value: 'failure', label: 'Failed' },
		{ value: 'cancelled', label: 'Cancelled' },
		{ value: 'skipped', label: 'Skipped' },
		{ value: 'timed_out', label: 'Timed out' }
	];

	function statusFilter(v: string): { status: ActionsStatus; conclusion: ActionsConclusion } {
		const done = (c: ActionsConclusion) => ({ status: ActionsStatus.COMPLETED, conclusion: c });
		switch (v) {
			case 'queued':
				return { status: ActionsStatus.QUEUED, conclusion: ActionsConclusion.UNSPECIFIED };
			case 'waiting':
				return { status: ActionsStatus.WAITING, conclusion: ActionsConclusion.UNSPECIFIED };
			case 'in_progress':
				return { status: ActionsStatus.RUNNING, conclusion: ActionsConclusion.UNSPECIFIED };
			case 'success':
				return done(ActionsConclusion.SUCCESS);
			case 'failure':
				return done(ActionsConclusion.FAILURE);
			case 'cancelled':
				return done(ActionsConclusion.CANCELLED);
			case 'skipped':
				return done(ActionsConclusion.SKIPPED);
			case 'timed_out':
				return done(ActionsConclusion.TIMED_OUT);
			default:
				return { status: ActionsStatus.UNSPECIFIED, conclusion: ActionsConclusion.UNSPECIFIED };
		}
	}

	const events = $derived.by(() => {
		const out = ['push', 'pull_request', 'workflow_dispatch', 'schedule', 'release'];
		for (const w of workflows) for (const t of w.triggers) if (!out.includes(t)) out.push(t);
		return out;
	});

	const selectedWorkflow = $derived(workflows.find((w) => w.path === selected));
	const canDispatch = $derived(
		!!selectedWorkflow && selectedWorkflow.enabled && !selectedWorkflow.error && selectedWorkflow.triggers.includes('workflow_dispatch')
	);
	const filtersActive = $derived(event !== ALL || status !== ALL || branch.trim() !== '' || actor.trim() !== '');

	async function loadWorkflows() {
		workflowsLoading = true;
		try {
			const resp = await rpcClient.actions.listWorkflows({ namespace, name }, silentCallOptions);
			workflows = resp.workflows;
		} catch {
			workflows = [];
		} finally {
			workflowsLoading = false;
		}
	}

	async function loadRuns(silent = false) {
		const mine = ++seq;
		if (!silent) runsLoading = true;
		try {
			const f = statusFilter(status);
			const resp = await rpcClient.actions.listWorkflowRuns(
				{
					namespace,
					name,
					page: pager.request(undefined, ''),
					workflowPath: selected,
					status: f.status,
					conclusion: f.conclusion,
					event: event === ALL ? '' : event,
					ref: branch.trim(),
					actor: actor.trim()
				},
				silentCallOptions
			);
			if (mine !== seq) return;
			runs = resp.runs;
			pager.apply(resp.page);
		} catch {
			if (mine === seq) {
				runs = [];
				pager.apply();
			}
		} finally {
			if (mine === seq) runsLoading = false;
		}
	}

	function selectWorkflow(path: string) {
		selected = path;
		pager.reset();
		replaceState(resolve(`${actionsHref}${path ? `?workflow=${encodeURIComponent(path)}` : ''}` as `/${string}`), {});
		loadRuns();
	}

	function onFilter() {
		pager.reset();
		loadRuns();
	}

	function onText() {
		clearTimeout(debounce);
		debounce = setTimeout(onFilter, 300);
	}

	let toggling = $state<Record<string, boolean>>({});

	async function toggleWorkflow(w: Workflow) {
		if (toggling[w.path]) return;
		toggling[w.path] = true;
		try {
			const req = { namespace, name, path: w.path };
			const resp = w.enabled ? await rpcClient.actions.disableWorkflow(req) : await rpcClient.actions.enableWorkflow(req);
			const next = resp.workflow;
			if (next) workflows = workflows.map((x) => (x.path === w.path ? next : x));
			toast.success(w.enabled ? 'Workflow disabled' : 'Workflow enabled');
		} catch {
			// Error interceptor already toasted
		} finally {
			delete toggling[w.path];
		}
	}

	onMount(() => {
		loadWorkflows();
		loadRuns();
	});

	const anyActive = $derived(runs.some((r) => isActive(r.status)));

	$effect(() => {
		if (!anyActive) return;
		const t = setInterval(() => loadRuns(true), 10000);
		return () => clearInterval(t);
	});

	const showEmpty = $derived(!workflowsLoading && !runsLoading && workflows.length === 0 && runs.length === 0 && !filtersActive);

	let dispatchOpen = $state(false);

	const example = `name: CI
on:
  push:
    branches: [main]
  pull_request:
  workflow_dispatch:
jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4
      - run: make test`;

	function lastRun(w: Workflow): string {
		return w.lastRunAt ? relativeTime(timestampDate(w.lastRunAt)) : 'never';
	}
</script>

<div class="space-y-4">
	{#if !configStore.actionsEnabled}
		<div class="rounded-xl border border-amber-500/30 bg-amber-500/5 px-4 py-3 text-[13px] flex items-start gap-2">
			<AlertCircle class="h-4 w-4 text-amber-500 mt-0.5 shrink-0" />
			<div>
				<span class="font-medium">Actions are disabled on this server.</span>
				<span class="text-muted-foreground">Workflows will not run until an administrator enables actions in the git settings.</span>
			</div>
		</div>
	{:else if !unitEnabled}
		<div class="rounded-xl border border-amber-500/30 bg-amber-500/5 px-4 py-3 text-[13px] flex items-start gap-2">
			<AlertCircle class="h-4 w-4 text-amber-500 mt-0.5 shrink-0" />
			<div>
				<span class="font-medium">Actions are turned off for this repository.</span>
				{#if gitRepoStore.canAdmin}
					<a href={resolve('/[namespace]/[name]/settings', { namespace, name })} class="text-primary hover:underline">Enable the unit in settings</a>
				{/if}
			</div>
		</div>
	{/if}

	{#if workflowsLoading && workflows.length === 0 && runs.length === 0}
		<div class="grid gap-6 lg:grid-cols-[16rem_minmax(0,1fr)]">
			<Skeleton class="h-48 w-full rounded-xl" />
			<Skeleton class="h-64 w-full rounded-xl" />
		</div>
	{:else if showEmpty}
		<EmptyState
			icon={WorkflowIcon}
			message="No workflows yet"
			description="Add a workflow file under .distroface/workflows to run jobs on push, pull requests, tags, schedules, or by hand."
		>
			{#snippet actions()}
				<div class="text-left w-full max-w-lg mx-auto min-w-0">
					<p class="text-[12px] text-muted-foreground mb-1.5 font-mono inline-flex items-center gap-1.5"><FileCode2 class="h-3.5 w-3.5" />.distroface/workflows/ci.yaml</p>
					<pre class="rounded-lg border border-border/60 bg-muted/40 p-3 text-[12px] font-mono overflow-x-auto leading-5">{example}</pre>
					<p class="text-[12px] text-muted-foreground mt-2">Files in .gitea/workflows and .github/workflows are picked up too.</p>
				</div>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="grid gap-6 lg:grid-cols-[16rem_minmax(0,1fr)]">
			<aside class="min-w-0">
				<div class="lg:hidden">
					<Select type="single" value={selected || ALL} onValueChange={(v) => selectWorkflow(!v || v === ALL ? '' : v)}>
						<SelectTrigger class="w-full" aria-label="Workflow">
							<span class="inline-flex items-center gap-2 truncate">
								<WorkflowIcon class="h-3.5 w-3.5 text-muted-foreground shrink-0" />
								<span class="truncate">{selectedWorkflow?.name ?? 'All workflows'}</span>
							</span>
						</SelectTrigger>
						<SelectContent>
							<SelectItem value={ALL}>All workflows</SelectItem>
							{#each workflows as w (w.path)}
								<SelectItem value={w.path}>{w.name}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
				</div>

				<div class="hidden lg:block space-y-0.5 lg:border-r lg:border-border/60 lg:pr-4">
					<div class="text-[11px] font-semibold tracking-wider uppercase text-muted-foreground px-2.5 pb-1.5">Workflows</div>
					<button
						type="button"
						class={cn('w-full text-left rounded-lg px-2.5 py-1.5 text-[13px] hover:bg-muted/60 transition-colors', selected === '' && 'bg-muted font-medium')}
						onclick={() => selectWorkflow('')}
					>
						All workflows
					</button>
					{#each workflows as w (w.path)}
						<div class={cn('group flex items-center gap-1 rounded-lg px-2.5 py-1.5 hover:bg-muted/60 transition-colors', selected === w.path && 'bg-muted')}>
							<button type="button" class="flex-1 min-w-0 text-left" onclick={() => selectWorkflow(w.path)} title={w.path}>
								<div class="flex items-center gap-1.5 min-w-0">
									<span class={cn('text-[13px] truncate', selected === w.path && 'font-medium', !w.enabled && 'text-muted-foreground')}>{w.name}</span>
									{#if w.error}
										<span title={w.error} class="shrink-0 inline-flex"><AlertCircle class="h-3.5 w-3.5 text-destructive" /></span>
									{/if}
									{#if !w.enabled}
										<Badge variant="outline" class="text-[10px] h-4 py-0 shrink-0">Disabled</Badge>
									{/if}
								</div>
								<div class="text-[11px] text-muted-foreground font-mono truncate">{w.path}</div>
							</button>
							<PermissionGate allowed={canWrite}>
								<Button
									variant="ghost"
									size="sm"
									class="h-6 w-6 p-0 shrink-0 opacity-0 group-hover:opacity-100 focus-visible:opacity-100 text-muted-foreground"
									title={w.enabled ? 'Disable workflow' : 'Enable workflow'}
									onclick={() => toggleWorkflow(w)}
									disabled={!!toggling[w.path]}
								>
									{#if w.enabled}
										<PowerOff class="h-3 w-3" />
									{:else}
										<Power class="h-3 w-3" />
									{/if}
								</Button>
							</PermissionGate>
						</div>
					{/each}
					{#if workflows.length === 0}
						<p class="text-[12px] text-muted-foreground px-2.5 py-1.5">No workflow files on {defaultBranch || 'the default branch'}.</p>
					{/if}
				</div>
			</aside>

			<section class="space-y-3 min-w-0">
				{#if selectedWorkflow}
					<div class="flex items-start gap-3 flex-wrap">
						<div class="min-w-0 flex-1">
							<h2 class="text-base font-semibold flex items-center gap-2 flex-wrap">
								<span class="truncate">{selectedWorkflow.name}</span>
								{#if !selectedWorkflow.enabled}
									<Badge variant="outline" class="text-[10px] h-4.5 py-0">Disabled</Badge>
								{/if}
							</h2>
							<div class="text-[12px] text-muted-foreground flex items-center gap-2 flex-wrap mt-0.5">
								<a href={resolve('/[namespace]/[name]/blob/[ref]/[...path]', { namespace, name, ref: encodeURIComponent(defaultBranch), path: selectedWorkflow.path })} class="font-mono hover:text-primary inline-flex items-center gap-1 min-w-0">
									<FileCode2 class="h-3 w-3 shrink-0" /><span class="truncate">{selectedWorkflow.path}</span>
								</a>
								<span>{selectedWorkflow.runCount} run{selectedWorkflow.runCount === 1 ? '' : 's'}</span>
								<span>last run {lastRun(selectedWorkflow)}</span>
								{#each selectedWorkflow.triggers as t (t)}
									<Badge variant="secondary" class="text-[10px] h-4.5 py-0 font-normal">{eventLabel(t)}</Badge>
								{/each}
							</div>
						</div>
						<div class="flex items-center gap-2 flex-wrap">
							<PermissionGate allowed={canWrite}>
								{#if selectedWorkflow.triggers.includes('workflow_dispatch')}
									<Button size="sm" class="h-8" onclick={() => (dispatchOpen = true)} disabled={!canDispatch || !configStore.actionsEnabled}>
										<Play class="h-3.5 w-3.5 mr-1.5" />Run Workflow
									</Button>
								{/if}
								<Button variant="outline" size="sm" class="h-8" onclick={() => toggleWorkflow(selectedWorkflow)} disabled={!!toggling[selectedWorkflow.path]}>
									{#if selectedWorkflow.enabled}
										<PowerOff class="h-3.5 w-3.5 mr-1.5" />Disable
									{:else}
										<Power class="h-3.5 w-3.5 mr-1.5" />Enable
									{/if}
								</Button>
							</PermissionGate>
						</div>
					</div>
					{#if selectedWorkflow.error}
						<div class="rounded-lg border border-destructive/30 bg-destructive/5 px-3 py-2 text-[12.5px] flex items-start gap-2">
							<AlertCircle class="h-4 w-4 text-destructive mt-0.5 shrink-0" />
							<div class="min-w-0 break-words"><span class="font-medium">This workflow file does not parse.</span> <span class="font-mono text-muted-foreground">{selectedWorkflow.error}</span></div>
						</div>
					{/if}
				{/if}

				<div class="flex items-center gap-2 flex-wrap">
					<Select type="single" value={event} onValueChange={(v) => { event = v || ALL; onFilter(); }}>
						<SelectTrigger class="h-8 w-full sm:w-40 text-[12.5px]" aria-label="Event">
							{event === ALL ? 'Any event' : eventLabel(event)}
						</SelectTrigger>
						<SelectContent>
							<SelectItem value={ALL}>Any event</SelectItem>
							{#each events as e (e)}
								<SelectItem value={e}>{eventLabel(e)}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
					<Select type="single" value={status} onValueChange={(v) => { status = v || ALL; onFilter(); }}>
						<SelectTrigger class="h-8 w-full sm:w-40 text-[12.5px]" aria-label="Status">
							{statusOptions.find((o) => o.value === status)?.label ?? 'Any status'}
						</SelectTrigger>
						<SelectContent>
							{#each statusOptions as o (o.value)}
								<SelectItem value={o.value}>{o.label}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
					<div class="relative w-full sm:w-44">
						<Search class="h-3.5 w-3.5 text-muted-foreground absolute left-2.5 top-1/2 -translate-y-1/2" />
						<Input bind:value={branch} oninput={onText} placeholder="Branch" class="h-8 pl-8 text-[12.5px] font-mono" aria-label="Branch" />
					</div>
					<Input bind:value={actor} oninput={onText} placeholder="Actor" class="h-8 w-full sm:w-36 text-[12.5px]" aria-label="Actor" />
					<span class="text-[12.5px] text-muted-foreground tabular-nums sm:ml-auto">{pager.totalCount || runs.length} run{(pager.totalCount || runs.length) === 1 ? '' : 's'}</span>
				</div>

				<RunList
					{namespace}
					{name}
					{runs}
					loading={runsLoading}
					{canWrite}
					emptyMessage={filtersActive ? 'No runs match these filters' : 'No workflow runs yet'}
					onChanged={() => loadRuns(true)}
				/>
				{#if runs.length > 0}
					<DataPagination {pager} onChange={() => loadRuns()} />
				{/if}
			</section>
		</div>
	{/if}
</div>

<WorkflowDispatchDialog
	bind:open={dispatchOpen}
	{namespace}
	{name}
	workflow={selectedWorkflow}
	defaultRef={defaultBranch}
	onDispatched={() => loadRuns(true)}
/>
