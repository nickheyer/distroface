<script lang="ts">
	import { page } from '$app/state';
	import { replaceState } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Bot, Plus, AlertCircle, Plug, FileCode2 } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription, AlertTitle } from '$lib/components/ui/alert';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import { Input } from '$lib/components/ui/input';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import AgentSessionList from '$lib/components/git/agent-session-list.svelte';
	import { rpcClient, silentCallOptions, backgroundCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import {
		AgentSessionConclusion,
		AgentSessionStatus,
		AgentSource,
		type AgentSession,
		type GetRepoAgentsResponse
	} from '$lib/proto/distroface/v1/agents_pb';
	import { isSessionActive, kindLabels, triggerLabels } from '$lib/components/git/agents-state';
	import { cn } from '$lib/utils';

	const ALL = '__all';

	const namespace = $derived(page.params.namespace ?? '');
	const name = $derived(page.params.name ?? '');
	const repo = $derived(gitRepoStore.repo);

	$effect(() => {
		if (namespace && name) gitRepoStore.load(namespace, name);
	});

	const canWrite = $derived(gitRepoStore.canWrite(GitUnit.AGENTS) && !repo?.isArchived);
	const unitEnabled = $derived(repo?.units?.agents === true);
	const agentsHref = $derived(resolve('/[namespace]/[name]/agents', { namespace, name }));
	const newHref = $derived(resolve('/[namespace]/[name]/agents/new', { namespace, name }));

	let overview = $state<GetRepoAgentsResponse | undefined>();
	let overviewLoading = $state(true);
	let selected = $state(page.url.searchParams.get('agent') ?? '');
	let sessions = $state<AgentSession[]>([]);
	let sessionsLoading = $state(true);
	let activeCount = $state(0n);
	const pager = new Pager(25);

	let status = $state(ALL);
	let trigger = $state(ALL);
	let actor = $state('');
	let debounce: ReturnType<typeof setTimeout> | undefined;
	let seq = 0;

	const statusOptions = [
		{ value: ALL, label: 'Any state' },
		{ value: 'queued', label: 'Queued' },
		{ value: 'running', label: 'Running' },
		{ value: 'approval', label: 'Waiting for approval' },
		{ value: 'success', label: 'Completed' },
		{ value: 'failure', label: 'Failed' },
		{ value: 'cancelled', label: 'Cancelled' },
		{ value: 'timed_out', label: 'Timed out' },
		{ value: 'budget', label: 'Budget exhausted' }
	];

	function statusFilter(v: string): {
		status: AgentSessionStatus;
		conclusion: AgentSessionConclusion;
	} {
		const done = (c: AgentSessionConclusion) => ({
			status: AgentSessionStatus.COMPLETED,
			conclusion: c
		});
		switch (v) {
			case 'queued':
				return {
					status: AgentSessionStatus.QUEUED,
					conclusion: AgentSessionConclusion.UNSPECIFIED
				};
			case 'running':
				return {
					status: AgentSessionStatus.RUNNING,
					conclusion: AgentSessionConclusion.UNSPECIFIED
				};
			case 'approval':
				return {
					status: AgentSessionStatus.WAITING_APPROVAL,
					conclusion: AgentSessionConclusion.UNSPECIFIED
				};
			case 'success':
				return done(AgentSessionConclusion.SUCCESS);
			case 'failure':
				return done(AgentSessionConclusion.FAILURE);
			case 'cancelled':
				return done(AgentSessionConclusion.CANCELLED);
			case 'timed_out':
				return done(AgentSessionConclusion.TIMED_OUT);
			case 'budget':
				return done(AgentSessionConclusion.BUDGET_EXCEEDED);
			default:
				return {
					status: AgentSessionStatus.UNSPECIFIED,
					conclusion: AgentSessionConclusion.UNSPECIFIED
				};
		}
	}

	const agents = $derived(overview?.agents ?? []);
	const selectedAgent = $derived(agents.find((a) => a.slug === selected || a.id === selected));
	const filtersActive = $derived(status !== ALL || trigger !== ALL || actor.trim() !== '');

	async function loadOverview() {
		overviewLoading = true;
		try {
			overview = await rpcClient.agents.getRepoAgents({ namespace, name }, silentCallOptions);
		} catch {
			overview = undefined;
		} finally {
			overviewLoading = false;
		}
	}

	async function loadSessions(silent = false) {
		const opts = silent ? backgroundCallOptions : silentCallOptions;
		const mine = ++seq;
		if (!silent) sessionsLoading = true;
		try {
			const f = statusFilter(status);
			const resp = await rpcClient.agents.listAgentSessions(
				{
					namespace,
					name,
					page: pager.request(undefined, ''),
					agent: selected,
					status: f.status,
					conclusion: f.conclusion,
					trigger: trigger === ALL ? '' : trigger,
					actor: actor.trim()
				},
				opts
			);
			if (mine !== seq) return;
			sessions = resp.sessions;
			activeCount = resp.activeCount;
			pager.apply(resp.page);
		} catch {
			if (mine === seq) {
				sessions = [];
				pager.apply();
			}
		} finally {
			if (mine === seq) sessionsLoading = false;
		}
	}

	function selectAgent(slug: string) {
		selected = slug;
		pager.reset();
		replaceState(
			resolve(`${agentsHref}${slug ? `?agent=${encodeURIComponent(slug)}` : ''}` as `/${string}`),
			{}
		);
		loadSessions();
	}

	function onFilter() {
		pager.reset();
		loadSessions();
	}

	function onText() {
		clearTimeout(debounce);
		debounce = setTimeout(onFilter, 300);
	}

	onMount(() => {
		loadOverview();
		loadSessions();
	});

	const anyActive = $derived(sessions.some((s) => isSessionActive(s.status)));
	$effect(() => {
		if (!anyActive) return;
		const t = setInterval(() => loadSessions(true), 5000);
		return () => clearInterval(t);
	});

	const newTaskHref = $derived(
		selectedAgent
			? resolve(`${newHref}?agent=${encodeURIComponent(selectedAgent.slug)}` as `/${string}`)
			: newHref
	);
</script>

<div class="space-y-4">
	{#if !configStore.agentsEnabled}
		<Alert variant="warning">
			<AlertCircle />
			<AlertTitle>Agents are disabled on this server</AlertTitle>
			<AlertDescription
				>Sessions will not run until an administrator enables agents in the git settings.</AlertDescription
			>
		</Alert>
	{:else if overview && !overview.enabled}
		<Alert variant="warning">
			<AlertCircle />
			<AlertTitle>Agents are disabled for this organization</AlertTitle>
			<AlertDescription
				>An organization owner can enable them under the organization agents page.</AlertDescription
			>
		</Alert>
	{:else if !unitEnabled}
		<Alert variant="warning">
			<AlertCircle />
			<AlertTitle>Agents are turned off for this repository</AlertTitle>
			{#if gitRepoStore.canAdmin}
				<AlertDescription>
					<a href={resolve('/[namespace]/[name]/settings', { namespace, name })} class="link"
						>Enable the unit in settings</a
					>
				</AlertDescription>
			{/if}
		</Alert>
	{:else if overview && !overview.hasProvider}
		<Alert variant="warning">
			<Plug />
			<AlertTitle>No inference connector</AlertTitle>
			<AlertDescription>
				{#if gitRepoStore.canAdmin}
					<a
						href={resolve('/[namespace]/[name]/settings/agents/connectors', { namespace, name })}
						class="link">Add a connector</a
					> here, or ask an organization or instance admin to register one.
				{:else}
					Ask a repository, organization, or instance admin to register a connector.
				{/if}
			</AlertDescription>
		</Alert>
	{/if}

	{#if overviewLoading && !overview}
		<div class="grid gap-6 lg:grid-cols-[16rem_minmax(0,1fr)]">
			<Skeleton class="h-48 w-full rounded-lg" />
			<Skeleton class="h-64 w-full rounded-lg" />
		</div>
	{:else}
		<div class="grid gap-6 lg:grid-cols-[16rem_minmax(0,1fr)]">
			<aside class="min-w-0">
				<div class="flex items-center gap-2 lg:hidden">
					<Select
						type="single"
						value={selected || ALL}
						onValueChange={(v) => selectAgent(!v || v === ALL ? '' : v)}
					>
						<SelectTrigger class="min-w-0 flex-1" aria-label="Agent">
							<span class="inline-flex items-center gap-2 truncate">
								<Bot class="size-3.5 shrink-0 text-muted-foreground" />
								<span class="truncate">{selectedAgent?.name ?? 'All agents'}</span>
							</span>
						</SelectTrigger>
						<SelectContent>
							<SelectItem value={ALL}>All agents</SelectItem>
							{#each agents as a (a.id)}
								<SelectItem value={a.slug}>{a.name}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
					<PermissionGate allowed={canWrite && !!overview?.canStart}>
						<Button class="shrink-0" href={newTaskHref}><Plus />New task</Button>
					</PermissionGate>
				</div>

				<div class="hidden space-y-0.5 lg:block lg:border-r lg:pr-4">
					<PermissionGate allowed={canWrite && !!overview?.canStart}>
						<Button size="sm" class="mb-2 w-full" href={newTaskHref} disabled={agents.length === 0}>
							<Plus />New task
						</Button>
					</PermissionGate>
					<div class="eyebrow px-2.5 pb-1.5">Agents</div>
					<button
						type="button"
						class={cn(
							'w-full rounded-md px-2.5 py-1.5 text-left text-sm transition-colors hover:bg-muted/60',
							selected === '' && 'bg-muted font-medium'
						)}
						onclick={() => selectAgent('')}
					>
						All agents
					</button>
					{#each agents as a (a.id)}
						<button
							type="button"
							class={cn(
								'w-full rounded-md px-2.5 py-1.5 text-left transition-colors hover:bg-muted/60',
								selected === a.slug && 'bg-muted'
							)}
							onclick={() => selectAgent(a.slug)}
							title={a.description}
						>
							<div class="flex min-w-0 items-center gap-1.5">
								<span
									class={cn(
										'truncate text-sm',
										selected === a.slug && 'font-medium',
										!a.enabled && 'text-muted-foreground'
									)}>{a.name}</span
								>
								{#if a.source === AgentSource.FILE}
									<FileCode2 class="size-3 shrink-0 text-muted-foreground" />
								{/if}
								{#if !a.enabled}
									<Badge variant="outline" class="shrink-0">Disabled</Badge>
								{/if}
							</div>
							<div class="truncate font-mono text-2xs text-muted-foreground">
								@{a.botUsername || a.slug}, {kindLabels[a.kind]?.toLowerCase() ?? 'agent'}
							</div>
						</button>
					{/each}
					{#if agents.length === 0}
						<p class="px-2.5 py-1.5 text-xs text-muted-foreground">
							No agents are defined for this repository.
							{#if gitRepoStore.canAdmin}
								<a
									href={resolve('/[namespace]/[name]/settings/agents/definitions', {
										namespace,
										name
									})}
									class="link">Define one</a
								>.
							{/if}
						</p>
					{/if}
					{#if overview && overview.instructionFiles.length > 0}
						<div class="mt-4 px-2.5">
							<div class="eyebrow pb-1">Instructions</div>
							{#each overview.instructionFiles as f (f)}
								<a
									href={resolve('/[namespace]/[name]/blob/[ref]/[...path]', {
										namespace,
										name,
										ref: encodeURIComponent(repo?.defaultBranch ?? ''),
										path: f
									})}
									class="block truncate font-mono text-2xs text-muted-foreground hover:text-primary"
									>{f}</a
								>
							{/each}
						</div>
					{/if}
				</div>
			</aside>

			<section class="min-w-0 space-y-3">
				{#if selectedAgent}
					<div class="flex flex-wrap items-start gap-3">
						<div class="min-w-0 flex-1">
							<h2 class="section-title flex flex-wrap items-center gap-2">
								<span class="truncate">{selectedAgent.name}</span>
								<Badge variant="secondary">{kindLabels[selectedAgent.kind] ?? 'Agent'}</Badge>
								{#if !selectedAgent.enabled}<Badge variant="outline">Disabled</Badge>{/if}
							</h2>
							<div class="mt-0.5 flex flex-wrap items-center gap-2 text-xs text-muted-foreground">
								<span class="font-mono">@{selectedAgent.botUsername || selectedAgent.slug}</span>
								{#if selectedAgent.description}<span>{selectedAgent.description}</span>{/if}
								{#if selectedAgent.model}<span class="font-mono">{selectedAgent.model}</span>{/if}
								<span
									>{selectedAgent.sessionCount} session{selectedAgent.sessionCount === 1n
										? ''
										: 's'}</span
								>
							</div>
						</div>
					</div>
				{/if}

				<div class="flex flex-wrap items-center gap-2">
					<Select
						type="single"
						value={status}
						onValueChange={(v) => {
							status = v || ALL;
							onFilter();
						}}
					>
						<SelectTrigger class="w-full sm:w-44" aria-label="State">
							{statusOptions.find((o) => o.value === status)?.label ?? 'Any state'}
						</SelectTrigger>
						<SelectContent>
							{#each statusOptions as o (o.value)}
								<SelectItem value={o.value}>{o.label}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
					<Select
						type="single"
						value={trigger}
						onValueChange={(v) => {
							trigger = v || ALL;
							onFilter();
						}}
					>
						<SelectTrigger class="w-full sm:w-40" aria-label="Trigger">
							{trigger === ALL ? 'Any trigger' : (triggerLabels[trigger] ?? trigger)}
						</SelectTrigger>
						<SelectContent>
							<SelectItem value={ALL}>Any trigger</SelectItem>
							{#each Object.entries(triggerLabels) as [k, label] (k)}
								<SelectItem value={k}>{label}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
					<Input
						bind:value={actor}
						oninput={onText}
						placeholder="Started by"
						class="w-full sm:w-36"
						aria-label="Started by"
					/>
					<span class="text-xs text-muted-foreground tabular-nums sm:ml-auto">
						{pager.totalCount || sessions.length} session{(pager.totalCount || sessions.length) ===
						1
							? ''
							: 's'}{#if activeCount > 0n},
							{activeCount} active{/if}
					</span>
				</div>

				<AgentSessionList
					{namespace}
					{name}
					{sessions}
					loading={sessionsLoading}
					{canWrite}
					emptyMessage={filtersActive ? 'No sessions match these filters' : 'No agent sessions yet'}
					emptyDescription={filtersActive
						? 'Try a different state, trigger, or user.'
						: 'Start a task, assign an issue to an agent, request a review from one, or mention it in a comment.'}
					onChanged={() => loadSessions(true)}
				/>
				{#if sessions.length > 0}
					<DataPagination {pager} onChange={() => loadSessions()} />
				{/if}
			</section>
		</div>
	{/if}
</div>
