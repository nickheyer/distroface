<script lang="ts">
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Milestone as MilestoneIcon, Plus, Pencil, Trash2, CircleCheck, RotateCw, CalendarDays, CircleDot } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import Markdown from '$lib/components/git/markdown.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import { IssueState, type Milestone } from '$lib/proto/distroface/v1/issue_pb';
	import { relativeTime, cn } from '$lib/utils';
	import { timestampDate, timestampFromDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const base = $derived(`/${namespace}/${name}`);
	const canManage = $derived(gitRepoStore.canWrite(GitUnit.ISSUES) && !repo.isArchived);

	let stateTab = $state<'open' | 'closed'>('open');
	let milestones = $state<Milestone[]>([]);
	let loading = $state(true);
	const pager = new Pager(20);

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.issue.listMilestones(
				{ namespace, name, state: stateTab === 'closed' ? IssueState.CLOSED : IssueState.OPEN, page: pager.request() },
				silentCallOptions
			);
			milestones = resp.milestones;
			pager.apply(resp.page);
		} catch {
			milestones = [];
			pager.apply();
		} finally {
			loading = false;
		}
	}

	function setTab(tab: 'open' | 'closed') {
		if (stateTab === tab) return;
		stateTab = tab;
		pager.reset();
		load();
	}

	onMount(() => {
		gitRepoStore.load(namespace, name);
		load();
	});

	function progress(m: Milestone): { pct: number; open: number; closed: number } {
		const open = Number(m.openIssues);
		const closed = Number(m.closedIssues);
		const total = open + closed;
		return { pct: total === 0 ? 0 : Math.round((closed / total) * 100), open, closed };
	}

	function dueInfo(m: Milestone): { text: string; overdue: boolean } {
		if (!m.dueDate) return { text: 'No due date', overdue: false };
		const d = timestampDate(m.dueDate);
		const overdue = m.state !== IssueState.CLOSED && d.getTime() < Date.now();
		return { text: `${overdue ? 'Past due by' : 'Due'} ${overdue ? relativeTime(d).replace(' ago', '') : d.toLocaleDateString()}`, overdue };
	}

	let panelOpen = $state(false);
	let editing = $state<Milestone | null>(null);
	let fTitle = $state('');
	let fDescription = $state('');
	let fDue = $state('');
	let saving = $state(false);

	function openCreate() {
		editing = null;
		fTitle = '';
		fDescription = '';
		fDue = '';
		panelOpen = true;
	}

	function openEdit(m: Milestone) {
		editing = m;
		fTitle = m.title;
		fDescription = m.description;
		fDue = m.dueDate ? timestampDate(m.dueDate).toISOString().slice(0, 10) : '';
		panelOpen = true;
	}

	function dueTimestamp() {
		if (!fDue) return editing ? { seconds: 0n, nanos: 0 } : undefined;
		const d = new Date(`${fDue}T12:00:00`);
		return Number.isNaN(d.getTime()) ? undefined : timestampFromDate(d);
	}

	async function save() {
		if (!fTitle.trim() || saving) return;
		saving = true;
		try {
			if (editing) {
				await rpcClient.issue.updateMilestone({ namespace, name, id: editing.id, title: fTitle.trim(), description: fDescription.trim(), dueDate: dueTimestamp() });
				toast.success('Milestone updated');
			} else {
				await rpcClient.issue.createMilestone({ namespace, name, title: fTitle.trim(), description: fDescription.trim(), dueDate: dueTimestamp() });
				toast.success('Milestone created');
			}
			panelOpen = false;
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			saving = false;
		}
	}

	let toggling = $state('');
	async function toggleState(m: Milestone) {
		toggling = m.id;
		try {
			await rpcClient.issue.updateMilestone({ namespace, name, id: m.id, state: m.state === IssueState.CLOSED ? IssueState.OPEN : IssueState.CLOSED });
			toast.success(m.state === IssueState.CLOSED ? 'Milestone reopened' : 'Milestone closed');
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			toggling = '';
		}
	}

	let deleteOpen = $state(false);
	let deleteTarget = $state<Milestone | null>(null);
	let deleting = $state(false);

	async function confirmDelete() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await rpcClient.issue.deleteMilestone({ namespace, name, id: deleteTarget.id });
			toast.success('Milestone deleted');
			deleteOpen = false;
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}
</script>

<div class="space-y-4">
	<div class="flex flex-wrap items-center gap-2">
		<div class="flex items-center rounded-lg border border-border/60 p-0.5">
			<button type="button" class={cn('px-3 py-1 text-[12.5px] rounded-md inline-flex items-center gap-1.5', stateTab === 'open' ? 'bg-muted font-semibold' : 'text-muted-foreground hover:text-foreground')} onclick={() => setTab('open')}><CircleDot class="h-3.5 w-3.5" />Open</button>
			<button type="button" class={cn('px-3 py-1 text-[12.5px] rounded-md inline-flex items-center gap-1.5', stateTab === 'closed' ? 'bg-muted font-semibold' : 'text-muted-foreground hover:text-foreground')} onclick={() => setTab('closed')}><CircleCheck class="h-3.5 w-3.5" />Closed</button>
		</div>
		<span class="text-[12.5px] text-muted-foreground tabular-nums">{pager.totalCount || milestones.length} milestone{(pager.totalCount || milestones.length) === 1 ? '' : 's'}</span>
		<div class="ml-auto flex items-center gap-2">
			<Button variant="outline" size="sm" class="h-8" href="{base}/labels">Labels</Button>
			<PermissionGate allowed={canManage}>
				<Button size="sm" class="h-8" onclick={openCreate}><Plus class="h-3.5 w-3.5 mr-1.5" />New Milestone</Button>
			</PermissionGate>
		</div>
	</div>

	{#if loading && milestones.length === 0}
		<Skeleton class="h-48 w-full rounded-xl" />
	{:else if milestones.length === 0}
		<EmptyState icon={MilestoneIcon} message={stateTab === 'open' ? 'No open milestones' : 'No closed milestones'} description="Milestones group issues toward a date and show progress.">
			{#snippet actions()}
				{#if canManage && stateTab === 'open'}<Button size="sm" onclick={openCreate}><Plus class="h-3.5 w-3.5 mr-1.5" />New Milestone</Button>{/if}
			{/snippet}
		</EmptyState>
	{:else}
		<div class="rounded-xl border border-border/60 divide-y divide-border/40 transition-opacity {loading ? 'opacity-60' : ''}">
			{#each milestones as m (m.id)}
				{@const p = progress(m)}
				{@const due = dueInfo(m)}
				<div class="px-4 py-3 flex flex-col md:flex-row md:items-start gap-3">
					<div class="min-w-0 flex-1 space-y-1.5">
						<a href="{resolve('/[namespace]/[name]/issues', { namespace, name })}?milestone={encodeURIComponent(m.title)}" class="text-[15px] font-semibold hover:text-primary">{m.title}</a>
						<div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-[12.5px] text-muted-foreground">
							<span class={cn('inline-flex items-center gap-1', due.overdue && 'text-destructive')}><CalendarDays class="h-3.5 w-3.5" />{due.text}</span>
							{#if m.state === IssueState.CLOSED && m.closedAt}
								<span>Closed {relativeTime(timestampDate(m.closedAt))}</span>
							{:else if m.updatedAt}
								<span>Updated {relativeTime(timestampDate(m.updatedAt))}</span>
							{/if}
						</div>
						{#if m.description}
							<div class="text-[13px] text-muted-foreground"><Markdown source={m.description} /></div>
						{/if}
					</div>
					<div class="md:w-64 space-y-1.5 shrink-0">
						<div class="h-2 rounded-full bg-muted overflow-hidden">
							<div class="h-full bg-success rounded-full transition-all" style="width: {p.pct}%"></div>
						</div>
						<div class="flex items-center gap-2 text-[12px] text-muted-foreground tabular-nums">
							<span class="font-medium text-foreground">{p.pct}%</span>
							<a href="{resolve('/[namespace]/[name]/issues', { namespace, name })}?milestone={encodeURIComponent(m.title)}" class="hover:text-primary">{p.open} open</a>
							<a href="{resolve('/[namespace]/[name]/issues', { namespace, name })}?milestone={encodeURIComponent(m.title)}&state=closed" class="hover:text-primary">{p.closed} closed</a>
						</div>
						{#if canManage}
							<div class="flex flex-wrap items-center gap-1 pt-1">
								<Button variant="ghost" size="sm" class="h-8 px-2" onclick={() => openEdit(m)}><Pencil class="h-3.5 w-3.5 mr-1" />Edit</Button>
								<Button variant="ghost" size="sm" class="h-8 px-2" disabled={toggling === m.id} onclick={() => toggleState(m)}>
									{#if m.state === IssueState.CLOSED}<RotateCw class="h-3.5 w-3.5 mr-1" />Reopen{:else}<CircleCheck class="h-3.5 w-3.5 mr-1" />Close{/if}
								</Button>
								<Button variant="ghost" size="sm" class="h-8 px-2 text-destructive hover:text-destructive" onclick={() => { deleteTarget = m; deleteOpen = true; }} aria-label="Delete milestone"><Trash2 class="h-3.5 w-3.5" /></Button>
							</div>
						{/if}
					</div>
				</div>
			{/each}
			<DataPagination attached {pager} onChange={load} />
		</div>
	{/if}
</div>

<FormPanel bind:open={panelOpen} title={editing ? 'Edit milestone' : 'New milestone'} description="Milestones group issues toward a date and show progress." icon={MilestoneIcon}>
	<div class="space-y-3">
		<FormField label="Title" id="ms-title" required>
			<Input id="ms-title" bind:value={fTitle} placeholder="v1.0" />
		</FormField>
		<FormField label="Due date" id="ms-due" help="Optional, overdue milestones are flagged in the list">
			<Input id="ms-due" type="date" bind:value={fDue} class="w-full sm:w-48" />
		</FormField>
		<FormField label="Description" id="ms-desc">
			<Textarea id="ms-desc" bind:value={fDescription} rows={4} placeholder="What this milestone delivers" />
		</FormField>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (panelOpen = false)}>Cancel</Button>
		<Button onclick={save} disabled={saving || !fTitle.trim()}>{saving ? 'Saving...' : editing ? 'Save Changes' : 'Create Milestone'}</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteOpen} title="Delete milestone" confirmLabel="Delete" onConfirm={confirmDelete} loading={deleting} icon={Trash2}>
	{#snippet description()}
		Issues in <strong>{deleteTarget?.title}</strong> keep no milestone. This cannot be undone.
	{/snippet}
</ConfirmDialog>
