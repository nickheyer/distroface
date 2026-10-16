<script lang="ts">
	import { resolve } from '$app/paths';
	import { goto } from '$app/navigation';
	import {
		Settings2, Check, Search, Loader2, SquareKanban, CalendarDays, Blocks, X, Clock, Hourglass, Plus, Trash2,
		Bell, BellOff, Lock, Unlock, Pin, PinOff, CircleDot, CircleCheck, GitPullRequest
	} from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Popover, PopoverContent, PopoverTrigger } from '$lib/components/ui/popover';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import LabelPicker from '$lib/components/git/label-picker.svelte';
	import AssigneePicker from '$lib/components/git/assignee-picker.svelte';
	import MilestonePicker from '$lib/components/git/milestone-picker.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { IssueState, type Issue, type IssueRef, type TrackedTime } from '$lib/proto/distroface/v1/issue_pb';
	import type { Project } from '$lib/proto/distroface/v1/project_pb';
	import { relativeTime, cn } from '$lib/utils';
	import { timestampDate, timestampFromDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	let {
		namespace,
		name,
		issue = $bindable(),
		canWrite,
		canModerate,
		onChanged
	}: {
		namespace: string;
		name: string;
		issue: Issue;
		canWrite: boolean;
		canModerate: boolean;
		onChanged: () => void;
	} = $props();

	const signedIn = $derived(authStore.isAuthenticated);
	const repoArchived = $derived(gitRepoStore.repo?.isArchived === true);
	const req = $derived({ namespace, name, number: issue.number });
	const num = $derived(Number(issue.number));

	let labelIds = $state<string[]>([]);
	let assigneeIds = $state<string[]>([]);
	let milestoneId = $state('');

	$effect(() => {
		labelIds = issue.labels.map((l) => l.id);
		assigneeIds = issue.assignees.map((a) => a.id);
		milestoneId = issue.milestone?.id ?? '';
	});

	async function update(fields: Record<string, unknown>) {
		try {
			const resp = await rpcClient.issue.updateIssue({ ...req, ...fields });
			if (resp.issue) issue = resp.issue;
			onChanged();
		} catch {
			// Error interceptor already toasted
		}
	}

	const saveLabels = (ids: string[]) => update({ setLabels: true, labelIds: ids });
	const saveAssignees = (ids: string[]) => update({ setAssignees: true, assigneeIds: ids });
	const saveMilestone = (id: string) => update({ milestoneId: id });

	// Projects
	let projectOpen = $state(false);
	let projects = $state<Project[]>([]);
	let projectsLoaded = $state(false);
	let projectBusy = $state(false);

	async function loadProjects() {
		try {
			const resp = await rpcClient.project.listProjects({ namespace, name, state: 'open', page: { pageSize: 100, pageToken: '', orderBy: '' } }, silentCallOptions);
			projects = resp.projects;
		} catch {
			projects = [];
		} finally {
			projectsLoaded = true;
		}
	}

	$effect(() => {
		if ((projectOpen || issue.projectId) && !projectsLoaded) loadProjects();
	});

	const currentProject = $derived(projects.find((p) => p.id === issue.projectId));
	const currentColumn = $derived(currentProject?.columns.find((c) => c.id === issue.projectColumnId));

	async function setProject(id: string) {
		if (projectBusy) return;
		projectBusy = true;
		projectOpen = false;
		try {
			if (issue.projectId && issue.projectId !== id) {
				await rpcClient.project.removeIssueFromProject({ projectId: issue.projectId, issueId: issue.id });
			}
			if (id && id !== issue.projectId) {
				await rpcClient.project.addIssueToProject({ projectId: id, issueId: issue.id, columnId: '' });
			}
			onChanged();
		} catch {
			// Error interceptor already toasted
		} finally {
			projectBusy = false;
		}
	}

	// Due date
	const dueValue = $derived(issue.dueDate ? timestampDate(issue.dueDate).toISOString().slice(0, 10) : '');

	function setDue(value: string) {
		if (!value) {
			if (issue.dueDate) update({ dueDate: { seconds: 0n, nanos: 0 } });
			return;
		}
		const d = new Date(`${value}T12:00:00`);
		if (Number.isNaN(d.getTime())) return;
		update({ dueDate: timestampFromDate(d) });
	}

	const overdue = $derived(!!issue.dueDate && issue.state === IssueState.OPEN && timestampDate(issue.dueDate).getTime() < Date.now());

	// Dependencies
	let blockedBy = $state<IssueRef[]>([]);
	let blocking = $state<IssueRef[]>([]);
	let depOpen = $state(false);
	let depQuery = $state('');
	let depResults = $state<Issue[]>([]);
	let depLoading = $state(false);
	let depDebounce: ReturnType<typeof setTimeout> | undefined;

	async function loadDependencies() {
		try {
			const resp = await rpcClient.issue.listDependencies(req, silentCallOptions);
			blockedBy = resp.blockedBy;
			blocking = resp.blocking;
		} catch {
			blockedBy = [];
			blocking = [];
		}
	}

	async function searchDeps() {
		depLoading = true;
		try {
			const resp = await rpcClient.issue.listIssues(
				{ namespace, name, state: IssueState.OPEN, page: { pageSize: 10, pageToken: '', orderBy: '', query: { text: depQuery.trim(), filters: [] } } },
				silentCallOptions
			);
			depResults = resp.issues.filter((i) => i.number !== issue.number);
		} catch {
			depResults = [];
		} finally {
			depLoading = false;
		}
	}

	function onDepQuery() {
		clearTimeout(depDebounce);
		depDebounce = setTimeout(searchDeps, 200);
	}

	$effect(() => {
		if (depOpen) searchDeps();
	});

	async function addDependency(target: string) {
		depOpen = false;
		try {
			const resp = await rpcClient.issue.addDependency({ ...req, dependsOn: target });
			blockedBy = resp.blockedBy;
			onChanged();
		} catch {
			// Error interceptor already toasted
		}
	}

	async function removeDependency(ref: IssueRef) {
		const target = ref.repoFullName && ref.repoFullName !== `${namespace}/${name}` ? `${ref.repoFullName}#${ref.number}` : String(ref.number);
		try {
			const resp = await rpcClient.issue.removeDependency({ ...req, dependsOn: target });
			blockedBy = resp.blockedBy;
			onChanged();
		} catch {
			// Error interceptor already toasted
		}
	}

	function refParams(ref: IssueRef): { namespace: string; name: string; number: string } {
		const [ns, nm] = (ref.repoFullName || `${namespace}/${name}`).split('/');
		return { namespace: ns, name: nm, number: String(ref.number) };
	}

	const openBlockers = $derived(blockedBy.filter((r) => r.state !== IssueState.CLOSED));
	const resolvedBlockers = $derived(blockedBy.filter((r) => r.state === IssueState.CLOSED));

	// Time, opt in per repository
	const timeTracking = $derived(gitRepoStore.timeTracking);
	const isOpen = $derived(issue.state !== IssueState.CLOSED);
	let times = $state<TrackedTime[]>([]);
	let totalSeconds = $state(0);
	let timeBusy = $state(false);
	let timeOpen = $state(false);
	let timeHours = $state('');
	let timeMinutes = $state('');
	let timeNote = $state('');

	async function loadTime() {
		if (!timeTracking) {
			times = [];
			totalSeconds = 0;
			return;
		}
		try {
			const resp = await rpcClient.issue.listTrackedTime({ namespace, name, number: issue.number, page: { pageSize: 50, pageToken: '', orderBy: '' } }, silentCallOptions);
			times = resp.times;
			totalSeconds = Number(resp.totalSeconds);
		} catch {
			times = [];
		}
	}

	// Server sums the open intervals, the client only advances the current one
	const openBase = $derived({ seconds: Number(issue.openSeconds), at: Date.now() });
	let tick = $state(Date.now());
	$effect(() => {
		if (!timeTracking || !isOpen) return;
		const id = setInterval(() => (tick = Date.now()), 60_000);
		return () => clearInterval(id);
	});
	const openFor = $derived(openBase.seconds + (isOpen ? Math.max(0, Math.floor((tick - openBase.at) / 1000)) : 0));

	function age(seconds: number): string {
		const d = Math.floor(seconds / 86400);
		const h = Math.floor((seconds % 86400) / 3600);
		const m = Math.floor((seconds % 3600) / 60);
		if (d >= 1) return h ? `${d}d ${h}h` : `${d}d`;
		if (h >= 1) return m ? `${h}h ${m}m` : `${h}h`;
		if (m >= 1) return `${m}m`;
		return 'under a minute';
	}

	function duration(seconds: number): string {
		const h = Math.floor(seconds / 3600);
		const m = Math.floor((seconds % 3600) / 60);
		if (h && m) return `${h}h ${m}m`;
		if (h) return `${h}h`;
		return `${m}m`;
	}

	async function addTime() {
		const seconds = Math.round((Number(timeHours) || 0) * 3600 + (Number(timeMinutes) || 0) * 60);
		if (seconds <= 0) {
			toast.error('Enter a duration');
			return;
		}
		timeBusy = true;
		try {
			await rpcClient.issue.addTrackedTime({ ...req, seconds: BigInt(seconds), note: timeNote.trim() });
			timeOpen = false;
			timeHours = '';
			timeMinutes = '';
			timeNote = '';
			await loadTime();
			onChanged();
		} catch {
			// Error interceptor already toasted
		} finally {
			timeBusy = false;
		}
	}

	async function deleteTime(t: TrackedTime) {
		try {
			await rpcClient.issue.deleteTrackedTime({ ...req, id: t.id });
			await loadTime();
			onChanged();
		} catch {
			// Error interceptor already toasted
		}
	}

	// Subscription and moderation
	let subBusy = $state(false);
	async function toggleSubscribe() {
		subBusy = true;
		try {
			if (issue.subscribed) await rpcClient.issue.unsubscribeIssue(req);
			else await rpcClient.issue.subscribeIssue(req);
			issue = { ...issue, subscribed: !issue.subscribed } as Issue;
		} catch {
			// Error interceptor already toasted
		} finally {
			subBusy = false;
		}
	}

	let lockOpen = $state(false);
	let lockReason = $state('');
	let modBusy = $state(false);
	const lockReasons = ['off-topic', 'too heated', 'resolved', 'spam'];

	async function lock() {
		modBusy = true;
		try {
			const resp = await rpcClient.issue.lockIssue({ ...req, reason: lockReason });
			if (resp.issue) issue = resp.issue;
			lockOpen = false;
			onChanged();
		} catch {
			// Error interceptor already toasted
		} finally {
			modBusy = false;
		}
	}

	async function unlock() {
		modBusy = true;
		try {
			const resp = await rpcClient.issue.unlockIssue(req);
			if (resp.issue) issue = resp.issue;
			onChanged();
		} catch {
			// Error interceptor already toasted
		} finally {
			modBusy = false;
		}
	}

	async function togglePin() {
		modBusy = true;
		try {
			const resp = issue.pinned ? await rpcClient.issue.unpinIssue(req) : await rpcClient.issue.pinIssue(req);
			if (resp.issue) issue = resp.issue;
			onChanged();
		} catch {
			// Error interceptor already toasted
		} finally {
			modBusy = false;
		}
	}

	let deleteOpen = $state(false);
	let deleting = $state(false);
	async function deleteIssue() {
		deleting = true;
		try {
			await rpcClient.issue.deleteIssue(req);
			toast.success('Issue deleted');
			await goto(resolve('/[namespace]/[name]/issues', { namespace, name }));
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}

	$effect(() => {
		void issue.id;
		void timeTracking;
		loadDependencies();
		loadTime();
	});

	const canDeleteTime = (t: TrackedTime) => canModerate || t.user?.id === authStore.user?.id;
</script>

{#snippet heading(label: string)}
	<span class="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{label}</span>
{/snippet}

{#snippet refRow(ref: IssueRef, removable: boolean, resolved = false)}
	<div class="flex items-center gap-2 text-[13px] min-w-0 {resolved ? 'text-muted-foreground' : ''}">
		{#if ref.isPull}
			<GitPullRequest class="h-3.5 w-3.5 shrink-0 {ref.state === IssueState.CLOSED ? 'text-purple-500' : 'text-success'}" />
		{:else if ref.state === IssueState.CLOSED}
			<CircleCheck class="h-3.5 w-3.5 shrink-0 text-purple-500" />
		{:else}
			<CircleDot class="h-3.5 w-3.5 shrink-0 text-success" />
		{/if}
		{#if ref.isPull}
			<a href={resolve('/[namespace]/[name]/pulls/[number]', refParams(ref))} class="truncate hover:text-primary {resolved ? 'line-through decoration-muted-foreground/60' : ''}" title={ref.title}>
				<span class="text-muted-foreground">#{ref.number}</span> {ref.title}
			</a>
		{:else}
			<a href={resolve('/[namespace]/[name]/issues/[number]', refParams(ref))} class="truncate hover:text-primary {resolved ? 'line-through decoration-muted-foreground/60' : ''}" title={ref.title}>
				<span class="text-muted-foreground">#{ref.number}</span> {ref.title}
			</a>
		{/if}
		{#if removable}
			<button type="button" class="ml-auto h-5 w-5 inline-flex items-center justify-center rounded text-muted-foreground hover:text-destructive" title="Remove" onclick={() => removeDependency(ref)}>
				<X class="h-3 w-3" />
			</button>
		{/if}
	</div>
{/snippet}

<div class="grid gap-5 sm:grid-cols-2 lg:block lg:space-y-5 text-sm">
	<section class="space-y-2">
		<AssigneePicker {namespace} {name} bind:selected={assigneeIds} canEdit={canWrite} onChange={saveAssignees} known={issue.assignees} />
	</section>
	<section class="space-y-2 lg:pt-5 lg:border-t lg:border-border/50">
		<LabelPicker {namespace} {name} bind:selected={labelIds} canEdit={canWrite} onChange={saveLabels} />
	</section>
	<section class="space-y-2 lg:pt-5 lg:border-t lg:border-border/50">
		<MilestonePicker {namespace} {name} bind:selected={milestoneId} canEdit={canWrite} onChange={saveMilestone} />
	</section>

	<section class="space-y-2 lg:pt-5 lg:border-t lg:border-border/50">
		<div class="flex items-center justify-between">
			{@render heading('Project')}
			{#if canWrite}
				<Popover bind:open={projectOpen}>
					<PopoverTrigger>
						{#snippet child({ props })}
							<button {...props} type="button" class="h-6 w-6 inline-flex items-center justify-center rounded-md text-muted-foreground hover:text-foreground hover:bg-accent transition-colors" title="Edit project" aria-label="Edit project" disabled={projectBusy}>
								<Settings2 class="h-3.5 w-3.5" />
							</button>
						{/snippet}
					</PopoverTrigger>
					<PopoverContent class="w-64 p-0" align="end">
						<div class="max-h-64 overflow-y-auto py-1">
							{#if issue.projectId}
								<button type="button" class="w-full px-2.5 py-1.5 text-left text-[12.5px] text-muted-foreground hover:bg-accent" onclick={() => setProject('')}>Remove from project</button>
							{/if}
							{#if projectsLoaded && projects.length === 0}
								<p class="px-3 py-4 text-[12.5px] text-muted-foreground text-center">No open projects</p>
							{/if}
							{#each projects as p (p.id)}
								<button type="button" class={cn('w-full flex items-center gap-2 px-2.5 py-1.5 text-left text-[13px] hover:bg-accent', p.id === issue.projectId && 'bg-accent/50')} onclick={() => setProject(p.id)}>
									<span class="h-4 w-4 inline-flex items-center justify-center">{#if p.id === issue.projectId}<Check class="h-3.5 w-3.5 text-primary" />{/if}</span>
									<span class="truncate">{p.title}</span>
								</button>
							{/each}
						</div>
					</PopoverContent>
				</Popover>
			{/if}
		</div>
		{#if issue.projectId}
			<a href={resolve('/[namespace]/[name]/projects/[id]', { namespace, name, id: issue.projectId })} class="flex items-center gap-1.5 text-[13px] hover:text-primary">
				<SquareKanban class="h-3.5 w-3.5 text-muted-foreground" />
				<span class="truncate font-medium">{currentProject?.title ?? 'Project board'}</span>
				{#if currentColumn}<span class="text-muted-foreground text-[12px]">/ {currentColumn.title}</span>{/if}
			</a>
		{:else}
			<span class="text-[12.5px] text-muted-foreground">None</span>
		{/if}
	</section>

	<section class="space-y-2 lg:pt-5 lg:border-t lg:border-border/50">
		{#if canWrite}
			<FormField label="Due date" id="issue-due" bordered={false} error={overdue ? 'Overdue' : ''}>
				<div class="flex items-center gap-1.5">
					<Input id="issue-due" type="date" value={dueValue} onchange={(e) => setDue((e.currentTarget as HTMLInputElement).value)} />
					{#if issue.dueDate}
						<Button variant="ghost" size="icon" class="h-9 w-9 shrink-0" title="Clear due date" onclick={() => setDue('')}><X class="h-3.5 w-3.5" /></Button>
					{/if}
				</div>
			</FormField>
		{:else}
			{@render heading('Due date')}
			{#if issue.dueDate}
				<span class="flex items-center gap-1.5 text-[13px] {overdue ? 'text-destructive' : ''}"><CalendarDays class="h-3.5 w-3.5" />{timestampDate(issue.dueDate).toLocaleDateString()}</span>
			{:else}
				<span class="text-[12.5px] text-muted-foreground">No due date</span>
			{/if}
		{/if}
	</section>

	<section class="space-y-2 lg:pt-5 lg:border-t lg:border-border/50">
		<div class="flex items-center justify-between">
			{@render heading('Dependencies')}
			{#if canWrite}
				<Popover bind:open={depOpen}>
					<PopoverTrigger>
						{#snippet child({ props })}
							<button {...props} type="button" class="h-6 w-6 inline-flex items-center justify-center rounded-md text-muted-foreground hover:text-foreground hover:bg-accent transition-colors" title="Add a blocker" aria-label="Add a blocker">
								<Plus class="h-3.5 w-3.5" />
							</button>
						{/snippet}
					</PopoverTrigger>
					<PopoverContent class="w-[min(20rem,calc(100vw-2rem))] p-0" align="end">
						<div class="border-b border-border/50 px-2 py-2 flex items-center gap-2">
							<Search class="h-3.5 w-3.5 text-muted-foreground shrink-0" />
							<input class="flex-1 bg-transparent text-[13px] outline-none placeholder:text-muted-foreground" placeholder="Search issues or type ns/name#12" bind:value={depQuery} oninput={onDepQuery} onkeydown={(e) => { if (e.key === 'Enter' && /^(\S+#)?\d+$/.test(depQuery.trim())) addDependency(depQuery.trim()); }} />
							{#if depLoading}<Loader2 class="h-3.5 w-3.5 animate-spin text-muted-foreground" />{/if}
						</div>
						<div class="max-h-64 overflow-y-auto py-1">
							{#if !depLoading && depResults.length === 0}
								<p class="px-3 py-4 text-[12.5px] text-muted-foreground text-center">No open issues match</p>
							{/if}
							{#each depResults as r (r.id)}
								<button type="button" class="w-full flex items-center gap-2 px-2.5 py-1.5 text-left text-[13px] hover:bg-accent" onclick={() => addDependency(String(r.number))}>
									<span class="text-muted-foreground shrink-0">#{r.number}</span>
									<span class="truncate">{r.title}</span>
								</button>
							{/each}
						</div>
					</PopoverContent>
				</Popover>
			{/if}
		</div>
		{#if blockedBy.length === 0 && blocking.length === 0}
			<span class="text-[12.5px] text-muted-foreground">No dependencies</span>
		{:else}
			{#if openBlockers.length}
				<p class="text-[11.5px] text-muted-foreground flex items-center gap-1"><Blocks class="h-3 w-3" />Blocked by</p>
				<div class="space-y-1">
					{#each openBlockers as ref (`${ref.repoFullName}#${ref.number}`)}
						{@render refRow(ref, canWrite)}
					{/each}
				</div>
			{/if}
			{#if resolvedBlockers.length}
				<p class="text-[11.5px] text-muted-foreground flex items-center gap-1 {openBlockers.length ? 'mt-1' : ''}">
					<CircleCheck class="h-3 w-3 text-purple-500" />{openBlockers.length ? 'Resolved blockers' : 'No longer blocked'}
					<span class="ml-auto tabular-nums">{resolvedBlockers.length} of {blockedBy.length} resolved</span>
				</p>
				<div class="space-y-1">
					{#each resolvedBlockers as ref (`${ref.repoFullName}#${ref.number}`)}
						{@render refRow(ref, canWrite, true)}
					{/each}
				</div>
			{/if}
			{#if blocking.length}
				<p class="text-[11.5px] text-muted-foreground flex items-center gap-1 mt-1"><Blocks class="h-3 w-3" />Blocking</p>
				<div class="space-y-1">
					{#each blocking as ref (`${ref.repoFullName}#${ref.number}`)}
						{@render refRow(ref, false)}
					{/each}
				</div>
			{/if}
		{/if}
	</section>

	{#if timeTracking}
		<section class="space-y-2 lg:pt-5 lg:border-t lg:border-border/50">
			<div class="flex items-center justify-between">
				{@render heading('Time')}
				{#if signedIn && !repoArchived}
					<button type="button" class="h-6 w-6 inline-flex items-center justify-center rounded-md text-muted-foreground hover:text-foreground hover:bg-accent transition-colors" title="Log time spent" aria-label="Log time spent" onclick={() => (timeOpen = true)}>
						<Plus class="h-3.5 w-3.5" />
					</button>
				{/if}
			</div>
			<div class="flex items-center gap-2 text-[13px]" title={isOpen ? 'Counted from when the issue was opened, paused while closed' : 'Time spent open before it was closed'}>
				<Clock class="h-3.5 w-3.5 text-muted-foreground" />
				<span>{isOpen ? 'Open for' : 'Resolved in'} <span class="tabular-nums font-medium">{age(openFor)}</span></span>
			</div>
			{#if totalSeconds > 0}
				<div class="flex items-center gap-2 text-[13px]">
					<Hourglass class="h-3.5 w-3.5 text-muted-foreground" />
					<span><span class="tabular-nums font-medium">{duration(totalSeconds)}</span> logged</span>
				</div>
			{/if}
			{#if times.length}
				<div class="space-y-1 pt-1">
					{#each times as t (t.id)}
						<div class="flex items-center gap-2 text-[12px] text-muted-foreground min-w-0">
							<span class="font-medium text-foreground/80 truncate">{t.user?.username ?? 'someone'}</span>
							<span class="tabular-nums shrink-0">{duration(Number(t.seconds))}</span>
							{#if t.note}<span class="truncate" title={t.note}>{t.note}</span>{/if}
							{#if t.createdAt}<span class="ml-auto shrink-0">{relativeTime(timestampDate(t.createdAt))}</span>{/if}
							{#if canDeleteTime(t)}
								<button type="button" class="h-5 w-5 inline-flex items-center justify-center rounded hover:text-destructive shrink-0" title="Delete entry" onclick={() => deleteTime(t)}><Trash2 class="h-3 w-3" /></button>
							{/if}
						</div>
					{/each}
				</div>
			{/if}
		</section>
	{/if}

	{#if signedIn}
		<section class="space-y-2 lg:pt-5 lg:border-t lg:border-border/50">
			{@render heading('Notifications')}
			<Button variant="outline" size="sm" class="h-8 w-full justify-center" onclick={toggleSubscribe} disabled={subBusy}>
				{#if issue.subscribed}<BellOff class="h-3.5 w-3.5 mr-1.5" />Unsubscribe{:else}<Bell class="h-3.5 w-3.5 mr-1.5" />Subscribe{/if}
			</Button>
			<p class="text-[11.5px] text-muted-foreground">{issue.subscribed ? 'You receive notifications for this issue.' : 'Get notified about activity here.'}</p>
		</section>
	{/if}

	{#if canModerate}
		<section class="space-y-1 lg:pt-5 lg:border-t lg:border-border/50">
			{@render heading('Moderation')}
			{#if issue.locked}
				<button type="button" class="flex items-center gap-2 text-[13px] text-muted-foreground hover:text-foreground w-full py-1" onclick={unlock} disabled={modBusy}><Unlock class="h-3.5 w-3.5" />Unlock conversation</button>
			{:else}
				<button type="button" class="flex items-center gap-2 text-[13px] text-muted-foreground hover:text-foreground w-full py-1" onclick={() => { lockReason = ''; lockOpen = true; }} disabled={modBusy}><Lock class="h-3.5 w-3.5" />Lock conversation</button>
			{/if}
			<button type="button" class="flex items-center gap-2 text-[13px] text-muted-foreground hover:text-foreground w-full py-1" onclick={togglePin} disabled={modBusy}>
				{#if issue.pinned}<PinOff class="h-3.5 w-3.5" />Unpin issue{:else}<Pin class="h-3.5 w-3.5" />Pin issue{/if}
			</button>
			{#if gitRepoStore.canAdmin}
				<button type="button" class="flex items-center gap-2 text-[13px] text-destructive/80 hover:text-destructive w-full py-1" onclick={() => (deleteOpen = true)}><Trash2 class="h-3.5 w-3.5" />Delete issue</button>
			{/if}
		</section>
	{/if}
</div>

<FormPanel bind:open={timeOpen} title="Log time" description="Record time you spent on #{num}, kept separate from how long it has been open" icon={Hourglass}>
	<div class="space-y-3">
		<div class="grid grid-cols-2 gap-3">
			<FormField label="Hours" id="time-hours">
				<Input id="time-hours" type="number" min="0" bind:value={timeHours} placeholder="0" />
			</FormField>
			<FormField label="Minutes" id="time-minutes">
				<Input id="time-minutes" type="number" min="0" max="59" bind:value={timeMinutes} placeholder="30" />
			</FormField>
		</div>
		<FormField label="Note" id="time-note">
			<Input id="time-note" bind:value={timeNote} placeholder="What was done" />
		</FormField>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (timeOpen = false)}>Cancel</Button>
		<Button onclick={addTime} disabled={timeBusy}>{timeBusy ? 'Saving...' : 'Log Time'}</Button>
	{/snippet}
</FormPanel>

<FormPanel bind:open={lockOpen} title="Lock conversation" description="Only collaborators with write access can comment while locked." icon={Lock}>
	<FormField label="Reason" id="lock-reason" help="Optional, shown on the issue">
		<Select type="single" value={lockReason} onValueChange={(v) => (lockReason = v ?? '')}>
			<SelectTrigger id="lock-reason" class="w-full">{lockReason || 'No reason'}</SelectTrigger>
			<SelectContent>
				{#each lockReasons as r (r)}
					<SelectItem value={r}>{r}</SelectItem>
				{/each}
			</SelectContent>
		</Select>
	</FormField>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (lockOpen = false)}>Cancel</Button>
		<Button onclick={lock} disabled={modBusy}>{modBusy ? 'Locking...' : 'Lock Conversation'}</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteOpen} title="Delete issue" confirmLabel="Delete" onConfirm={deleteIssue} loading={deleting} icon={Trash2}>
	{#snippet description()}
		This permanently deletes issue <strong>#{num}</strong> with every comment and reaction.
	{/snippet}
</ConfirmDialog>
