<script lang="ts">
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import {
		Milestone as MilestoneIcon,
		Plus,
		Pencil,
		Trash2,
		CircleCheck,
		RotateCw,
		CalendarDays,
		CircleDot
	} from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import Markdown from '$lib/components/git/markdown.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import { IssueState, type Milestone } from '$lib/proto/distroface/v1/issue_pb';
	import { relativeTime, cn } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const base = $derived(`/${namespace}/${name}`);
	const canManage = $derived(gitRepoStore.canWrite(GitUnit.ISSUES) && !repo.isArchived);
	const newHref = $derived(resolve('/[namespace]/[name]/milestones/new', { namespace, name }));

	function editHref(id: string): string {
		return resolve('/[namespace]/[name]/milestones/[id]/edit', { namespace, name, id });
	}

	let stateTab = $state<'open' | 'closed'>('open');
	let milestones = $state<Milestone[]>([]);
	let loading = $state(true);
	const pager = new Pager(20);

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.issue.listMilestones(
				{
					namespace,
					name,
					state: stateTab === 'closed' ? IssueState.CLOSED : IssueState.OPEN,
					page: pager.request()
				},
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
		return {
			text: `${overdue ? 'Past due by' : 'Due'} ${overdue ? relativeTime(d).replace(' ago', '') : d.toLocaleDateString()}`,
			overdue
		};
	}

	let toggling = $state('');
	async function toggleState(m: Milestone) {
		toggling = m.id;
		try {
			await rpcClient.issue.updateMilestone({
				namespace,
				name,
				id: m.id,
				state: m.state === IssueState.CLOSED ? IssueState.OPEN : IssueState.CLOSED
			});
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
		<div class="inline-flex items-center rounded-md border p-0.5">
			<button
				type="button"
				class={cn(
					'inline-flex items-center gap-1.5 rounded-sm px-2.5 py-1 text-xs font-medium transition-colors',
					stateTab === 'open'
						? 'bg-muted text-foreground'
						: 'text-muted-foreground hover:text-foreground'
				)}
				aria-pressed={stateTab === 'open'}
				onclick={() => setTab('open')}><CircleDot class="size-3.5" />Open</button
			>
			<button
				type="button"
				class={cn(
					'inline-flex items-center gap-1.5 rounded-sm px-2.5 py-1 text-xs font-medium transition-colors',
					stateTab === 'closed'
						? 'bg-muted text-foreground'
						: 'text-muted-foreground hover:text-foreground'
				)}
				aria-pressed={stateTab === 'closed'}
				onclick={() => setTab('closed')}><CircleCheck class="size-3.5" />Closed</button
			>
		</div>
		<span class="text-xs text-muted-foreground tabular-nums"
			>{pager.totalCount || milestones.length} milestone{(pager.totalCount || milestones.length) ===
			1
				? ''
				: 's'}</span
		>
		<div class="ml-auto flex items-center gap-2">
			<Button variant="outline" href="{base}/labels">Labels</Button>
			<PermissionGate allowed={canManage}>
				<Button href={newHref}><Plus />New milestone</Button>
			</PermissionGate>
		</div>
	</div>

	{#if loading && milestones.length === 0}
		<Skeleton class="h-48 w-full rounded-lg" />
	{:else if milestones.length === 0}
		<EmptyState
			icon={MilestoneIcon}
			message={stateTab === 'open' ? 'No open milestones' : 'No closed milestones'}
			description="Milestones group issues toward a date and show progress."
		>
			{#snippet actions()}
				{#if canManage && stateTab === 'open'}<Button variant="outline" href={newHref}
						><Plus />New milestone</Button
					>{/if}
			{/snippet}
		</EmptyState>
	{:else}
		<div class="box divide-y transition-opacity {loading ? 'opacity-60' : ''}">
			{#each milestones as m (m.id)}
				{@const p = progress(m)}
				{@const due = dueInfo(m)}
				<div class="flex flex-col gap-3 px-4 py-3 md:flex-row md:items-start">
					<div class="min-w-0 flex-1 space-y-1.5">
						<a
							href="{resolve('/[namespace]/[name]/issues', {
								namespace,
								name
							})}?milestone={encodeURIComponent(m.title)}"
							class="text-sm font-semibold hover:text-primary">{m.title}</a
						>
						<div class="flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
							<span class={cn('inline-flex items-center gap-1', due.overdue && 'text-destructive')}
								><CalendarDays class="size-3.5" />{due.text}</span
							>
							{#if m.state === IssueState.CLOSED && m.closedAt}
								<span>Closed {relativeTime(timestampDate(m.closedAt))}</span>
							{:else if m.updatedAt}
								<span>Updated {relativeTime(timestampDate(m.updatedAt))}</span>
							{/if}
						</div>
						{#if m.description}
							<div class="text-sm text-muted-foreground"><Markdown source={m.description} /></div>
						{/if}
					</div>
					<div class="shrink-0 space-y-1.5 md:w-64">
						<div class="h-2 overflow-hidden rounded-full bg-muted">
							<div
								class="h-full rounded-full bg-success transition-all"
								style="width: {p.pct}%"
							></div>
						</div>
						<div class="flex items-center gap-2 text-xs text-muted-foreground tabular-nums">
							<span class="font-medium text-foreground">{p.pct}%</span>
							<a
								href="{resolve('/[namespace]/[name]/issues', {
									namespace,
									name
								})}?milestone={encodeURIComponent(m.title)}"
								class="hover:text-primary">{p.open} open</a
							>
							<a
								href="{resolve('/[namespace]/[name]/issues', {
									namespace,
									name
								})}?milestone={encodeURIComponent(m.title)}&state=closed"
								class="hover:text-primary">{p.closed} closed</a
							>
						</div>
						{#if canManage}
							<div class="flex flex-wrap items-center gap-1 pt-1">
								<Button variant="ghost" size="sm" href={editHref(m.id)}><Pencil />Edit</Button>
								<Button
									variant="ghost"
									size="sm"
									disabled={toggling === m.id}
									onclick={() => toggleState(m)}
								>
									{#if m.state === IssueState.CLOSED}<RotateCw />Reopen{:else}<CircleCheck
										/>Close{/if}
								</Button>
								<Button
									variant="ghost-muted"
									size="icon-sm"
									class="text-destructive hover:text-destructive"
									onclick={() => {
										deleteTarget = m;
										deleteOpen = true;
									}}
									aria-label="Delete milestone"><Trash2 /></Button
								>
							</div>
						{/if}
					</div>
				</div>
			{/each}
			<DataPagination attached {pager} onChange={load} />
		</div>
	{/if}
</div>

<ConfirmDialog
	bind:open={deleteOpen}
	title="Delete milestone"
	confirmLabel="Delete"
	onConfirm={confirmDelete}
	loading={deleting}
	icon={Trash2}
>
	{#snippet description()}
		Issues in <strong>{deleteTarget?.title}</strong> keep no milestone. This cannot be undone.
	{/snippet}
</ConfirmDialog>
