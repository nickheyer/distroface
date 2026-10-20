<script lang="ts">
	import { page } from '$app/state';
	import { replaceState } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import {
		Play,
		Workflow as WorkflowIcon,
		Power,
		PowerOff,
		AlertCircle,
		FileCode,
		Search,
		Plus
	} from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription, AlertTitle } from '$lib/components/ui/alert';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import WorkflowStarter from '$lib/components/git/workflow-starter.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import RunList from '$lib/components/git/run-list.svelte';
	import { rpcClient, silentCallOptions, backgroundCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import {
		ActionsConclusion,
		ActionsStatus,
		type Workflow,
		type WorkflowRun
	} from '$lib/proto/distroface/v1/actions_pb';
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
	const canEdit = $derived(gitRepoStore.canWrite(GitUnit.CODE) && !gitRepoStore.readOnly);
	const unitEnabled = $derived(repo?.units?.actions !== false);
	const defaultBranch = $derived(repo?.defaultBranch ?? '');
	const actionsHref = $derived(resolve('/[namespace]/[name]/actions', { namespace, name }));
	const newWorkflowHref = $derived(resolve('/[namespace]/[name]/actions/new', { namespace, name }));

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
		!!selectedWorkflow &&
			selectedWorkflow.enabled &&
			!selectedWorkflow.error &&
			selectedWorkflow.triggers.includes('workflow_dispatch')
	);
	const filtersActive = $derived(
		event !== ALL || status !== ALL || branch.trim() !== '' || actor.trim() !== ''
	);

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
		const opts = silent ? backgroundCallOptions : silentCallOptions;
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
				opts
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
		replaceState(
			resolve(
				`${actionsHref}${path ? `?workflow=${encodeURIComponent(path)}` : ''}` as `/${string}`
			),
			{}
		);
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
			const resp = w.enabled
				? await rpcClient.actions.disableWorkflow(req)
				: await rpcClient.actions.enableWorkflow(req);
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

	const showEmpty = $derived(
		!workflowsLoading &&
			!runsLoading &&
			workflows.length === 0 &&
			runs.length === 0 &&
			!filtersActive
	);

	const dispatchHref = $derived(
		resolve(
			`${resolve('/[namespace]/[name]/actions/dispatch', { namespace, name })}?workflow=${encodeURIComponent(selected)}` as `/${string}`
		)
	);

	function lastRun(w: Workflow): string {
		return w.lastRunAt ? relativeTime(timestampDate(w.lastRunAt)) : 'never';
	}
</script>

<div class="space-y-4">
	{#if !configStore.actionsEnabled}
		<Alert variant="warning">
			<AlertCircle />
			<AlertTitle>Actions are disabled on this server</AlertTitle>
			<AlertDescription
				>Workflows will not run until an administrator enables actions in the git settings.</AlertDescription
			>
		</Alert>
	{:else if !unitEnabled}
		<Alert variant="warning">
			<AlertCircle />
			<AlertTitle>Actions are turned off for this repository</AlertTitle>
			{#if gitRepoStore.canAdmin}
				<AlertDescription>
					<a href={resolve('/[namespace]/[name]/settings', { namespace, name })} class="link"
						>Enable the unit in settings</a
					>
				</AlertDescription>
			{/if}
		</Alert>
	{/if}

	{#if workflowsLoading && workflows.length === 0 && runs.length === 0}
		<div class="grid gap-6 lg:grid-cols-[16rem_minmax(0,1fr)]">
			<Skeleton class="h-48 w-full rounded-lg" />
			<Skeleton class="h-64 w-full rounded-lg" />
		</div>
	{:else if showEmpty}
		<WorkflowStarter {namespace} {name} {defaultBranch} canWrite={canEdit} />
	{:else}
		<div class="grid gap-6 lg:grid-cols-[16rem_minmax(0,1fr)]">
			<aside class="min-w-0">
				<div class="flex items-center gap-2 lg:hidden">
					<Select
						type="single"
						value={selected || ALL}
						onValueChange={(v) => selectWorkflow(!v || v === ALL ? '' : v)}
					>
						<SelectTrigger class="min-w-0 flex-1" aria-label="Workflow">
							<span class="inline-flex items-center gap-2 truncate">
								<WorkflowIcon class="size-3.5 shrink-0 text-muted-foreground" />
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
					{#if canEdit}
						<Button variant="outline" class="shrink-0" href={newWorkflowHref}>
							<Plus />New
						</Button>
					{/if}
				</div>

				<div class="hidden space-y-0.5 lg:block lg:border-r lg:pr-4">
					{#if canEdit}
						<Button variant="outline" size="sm" class="mb-2 w-full" href={newWorkflowHref}>
							<Plus />New workflow
						</Button>
					{/if}
					<div class="eyebrow px-2.5 pb-1.5">Workflows</div>
					<button
						type="button"
						class={cn(
							'w-full rounded-md px-2.5 py-1.5 text-left text-sm transition-colors hover:bg-muted/60',
							selected === '' && 'bg-muted font-medium'
						)}
						onclick={() => selectWorkflow('')}
					>
						All workflows
					</button>
					{#each workflows as w (w.path)}
						<div
							class={cn(
								'group flex items-center gap-1 rounded-md px-2.5 py-1.5 transition-colors hover:bg-muted/60',
								selected === w.path && 'bg-muted'
							)}
						>
							<button
								type="button"
								class="min-w-0 flex-1 text-left"
								onclick={() => selectWorkflow(w.path)}
								title={w.path}
							>
								<div class="flex min-w-0 items-center gap-1.5">
									<span
										class={cn(
											'truncate text-sm',
											selected === w.path && 'font-medium',
											!w.enabled && 'text-muted-foreground'
										)}>{w.name}</span
									>
									{#if w.error}
										<span title={w.error} class="inline-flex shrink-0"
											><AlertCircle class="size-3.5 text-destructive" /></span
										>
									{/if}
									{#if !w.enabled}
										<Badge variant="outline" class="shrink-0">Disabled</Badge>
									{/if}
								</div>
								<div class="truncate font-mono text-2xs text-muted-foreground">{w.path}</div>
							</button>
							<PermissionGate allowed={canWrite}>
								<Button
									variant="ghost-muted"
									size="icon-xs"
									class="shrink-0 opacity-0 group-hover:opacity-100 focus-visible:opacity-100"
									title={w.enabled ? 'Disable workflow' : 'Enable workflow'}
									onclick={() => toggleWorkflow(w)}
									disabled={!!toggling[w.path]}
								>
									{#if w.enabled}
										<PowerOff />
									{:else}
										<Power />
									{/if}
								</Button>
							</PermissionGate>
						</div>
					{/each}
					{#if workflows.length === 0}
						<p class="px-2.5 py-1.5 text-xs text-muted-foreground">
							No workflow files on {defaultBranch || 'the default branch'}
						</p>
					{/if}
				</div>
			</aside>

			<section class="min-w-0 space-y-3">
				{#if selectedWorkflow}
					<div class="flex flex-wrap items-start gap-3">
						<div class="min-w-0 flex-1">
							<h2 class="section-title flex flex-wrap items-center gap-2">
								<span class="truncate">{selectedWorkflow.name}</span>
								{#if !selectedWorkflow.enabled}
									<Badge variant="outline">Disabled</Badge>
								{/if}
							</h2>
							<div class="mt-0.5 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
								<a
									href={resolve('/[namespace]/[name]/blob/[ref]/[...path]', {
										namespace,
										name,
										ref: encodeURIComponent(defaultBranch),
										path: selectedWorkflow.path
									})}
									class="inline-flex min-w-0 items-center gap-1 font-mono hover:text-primary"
								>
									<FileCode class="size-3 shrink-0" /><span class="truncate"
										>{selectedWorkflow.path}</span
									>
								</a>
								<span
									>{selectedWorkflow.runCount} run{selectedWorkflow.runCount === 1 ? '' : 's'}</span
								>
								<span>last run {lastRun(selectedWorkflow)}</span>
								{#each selectedWorkflow.triggers as t (t)}
									<Badge variant="secondary">{eventLabel(t)}</Badge>
								{/each}
							</div>
						</div>
						<div class="flex flex-wrap items-center gap-2">
							<PermissionGate allowed={canWrite}>
								{#if selectedWorkflow.triggers.includes('workflow_dispatch')}
									<Button
										href={dispatchHref}
										disabled={!canDispatch || !configStore.actionsEnabled}
									>
										<Play />Run workflow
									</Button>
								{/if}
								<Button
									variant="outline"
									onclick={() => toggleWorkflow(selectedWorkflow)}
									disabled={!!toggling[selectedWorkflow.path]}
								>
									{#if selectedWorkflow.enabled}
										<PowerOff />Disable
									{:else}
										<Power />Enable
									{/if}
								</Button>
							</PermissionGate>
						</div>
					</div>
					{#if selectedWorkflow.error}
						<Alert variant="destructive">
							<AlertCircle />
							<AlertTitle>This workflow file does not parse</AlertTitle>
							<AlertDescription class="min-w-0 font-mono wrap-break-word"
								>{selectedWorkflow.error}</AlertDescription
							>
						</Alert>
					{/if}
				{/if}

				<div class="flex flex-wrap items-center gap-2">
					<Select
						type="single"
						value={event}
						onValueChange={(v) => {
							event = v || ALL;
							onFilter();
						}}
					>
						<SelectTrigger class="w-full sm:w-40" aria-label="Event">
							{event === ALL ? 'Any event' : eventLabel(event)}
						</SelectTrigger>
						<SelectContent>
							<SelectItem value={ALL}>Any event</SelectItem>
							{#each events as e (e)}
								<SelectItem value={e}>{eventLabel(e)}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
					<Select
						type="single"
						value={status}
						onValueChange={(v) => {
							status = v || ALL;
							onFilter();
						}}
					>
						<SelectTrigger class="w-full sm:w-40" aria-label="Status">
							{statusOptions.find((o) => o.value === status)?.label ?? 'Any status'}
						</SelectTrigger>
						<SelectContent>
							{#each statusOptions as o (o.value)}
								<SelectItem value={o.value}>{o.label}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
					<div class="relative w-full sm:w-44">
						<Search
							class="absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground"
						/>
						<Input
							bind:value={branch}
							oninput={onText}
							placeholder="Branch"
							class="pl-8 font-mono"
							aria-label="Branch"
						/>
					</div>
					<Input
						bind:value={actor}
						oninput={onText}
						placeholder="Actor"
						class="w-full sm:w-36"
						aria-label="Actor"
					/>
					<span class="text-xs text-muted-foreground tabular-nums sm:ml-auto"
						>{pager.totalCount || runs.length} run{(pager.totalCount || runs.length) === 1
							? ''
							: 's'}</span
					>
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
