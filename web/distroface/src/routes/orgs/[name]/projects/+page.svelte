<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { onMount, getContext } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import * as Tabs from '$lib/components/ui/tabs';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import { SquareKanban, Plus, Trash2, CircleCheck, RotateCw, Lock } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import type { Project } from '$lib/proto/distroface/v1/project_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgId = $derived(ctx.org?.id ?? '');
	const orgName = $derived(page.params.name ?? '');
	const newHref = $derived(resolve('/orgs/[name]/projects/new', { name: orgName }));

	let projects = $state<Project[]>([]);
	const pager = new Pager(20);
	let loading = $state(true);
	let loaded = $state(false);
	let stateFilter = $state('open');

	let deleteOpen = $state(false);
	let deleteTarget = $state<Project | null>(null);
	let deleting = $state(false);
	let toggling = $state<Record<string, boolean>>({});

	async function loadProjects() {
		loading = true;
		try {
			const resp = await rpcClient.project.listProjects({
				page: pager.request(),
				orgId,
				state: stateFilter
			});
			projects = resp.projects;
			pager.apply(resp.page);
		} catch {
			projects = [];
		} finally {
			loading = false;
			loaded = true;
		}
	}

	function changeState(v: string) {
		stateFilter = v;
		pager.reset();
		loadProjects();
	}

	async function toggleClosed(project: Project) {
		toggling[project.id] = true;
		try {
			await rpcClient.project.closeProject({ id: project.id, closed: project.state !== 'closed' });
			toast.success(project.state === 'closed' ? 'Project reopened' : 'Project closed');
			await loadProjects();
		} catch {
			// error interceptor
		} finally {
			delete toggling[project.id];
		}
	}

	async function confirmDelete() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await rpcClient.project.deleteProject({ id: deleteTarget.id });
			toast.success('Project deleted');
			deleteOpen = false;
			await loadProjects();
		} catch {
			// error interceptor
		} finally {
			deleting = false;
		}
	}

	onMount(loadProjects);
</script>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Projects</h2>
			<p class="section-subtitle">
				Boards that track issues across the organization's repositories.
			</p>
		</div>
		{#if ctx.canAdmin}
			<Button class="shrink-0" href={newHref}>
				<Plus />
				New project
			</Button>
		{/if}
	</div>

	<Tabs.Root value={stateFilter} onValueChange={changeState}>
		<Tabs.List>
			<Tabs.Trigger value="open">Open</Tabs.Trigger>
			<Tabs.Trigger value="closed">Closed</Tabs.Trigger>
		</Tabs.List>
	</Tabs.Root>

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 3 }, i (i)}
				<div class="space-y-2 px-4 py-3">
					<Skeleton class="h-4 w-48" />
					<Skeleton class="h-3.5 w-72" />
					<Skeleton class="h-3 w-40" />
				</div>
			{/each}
		</div>
	{:else if projects.length === 0}
		<EmptyState
			icon={SquareKanban}
			message={stateFilter === 'closed' ? 'No closed projects' : 'No open projects'}
			description="Projects collect issues from any repository onto one board."
		>
			{#snippet actions()}
				{#if ctx.canAdmin && stateFilter === 'open'}
					<Button variant="outline" href={newHref}>
						<Plus />
						New project
					</Button>
				{/if}
			{/snippet}
		</EmptyState>
	{:else}
		<div class="box divide-y transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			{#each projects as project (project.id)}
				<div class="flex items-center gap-3 px-4 py-3">
					<div class="min-w-0 flex-1">
						<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
							<a
								href={resolve('/orgs/[name]/projects/[id]', { name: orgName, id: project.id })}
								class="truncate text-sm font-semibold underline-offset-2 hover:text-primary hover:underline"
							>
								{project.title}
							</a>
							{#if !project.public}
								<Badge variant="outline"><Lock />Private</Badge>
							{/if}
							{#if project.state === 'closed'}
								<Badge variant="outline">Closed</Badge>
							{/if}
						</div>
						<p class="mt-0.5 line-clamp-1 text-sm text-muted-foreground">
							{project.description || 'No description'}
						</p>
						<div
							class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground tabular-nums"
						>
							<span>{project.openCount} open, {project.closedCount} closed</span>
							{#if project.updatedAt}
								<span>Updated {relativeTime(timestampDate(project.updatedAt))}</span>
							{/if}
							{#if project.creator}
								<span>By {project.creator}</span>
							{/if}
						</div>
					</div>
					{#if ctx.canAdmin}
						<div class="flex shrink-0 items-center gap-1">
							<Button
								variant="ghost"
								disabled={!!toggling[project.id]}
								onclick={() => toggleClosed(project)}
							>
								{#if project.state === 'closed'}
									<RotateCw />
									Reopen
								{:else}
									<CircleCheck />
									Close
								{/if}
							</Button>
							<Button
								variant="ghost"
								size="icon"
								class="text-destructive hover:text-destructive"
								onclick={() => {
									deleteTarget = project;
									deleteOpen = true;
								}}
								aria-label="Delete"
							>
								<Trash2 />
							</Button>
						</div>
					{/if}
				</div>
			{/each}
		</div>
		<DataPagination {pager} onChange={loadProjects} />
	{/if}
</div>

<ConfirmDialog
	bind:open={deleteOpen}
	title="Delete project"
	confirmLabel="Delete"
	onConfirm={confirmDelete}
	loading={deleting}
	icon={Trash2}
>
	{#snippet description()}
		Delete <strong>{deleteTarget?.title}</strong>? Issues stay, only the board is removed.
	{/snippet}
</ConfirmDialog>
