<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { onMount, getContext } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import * as Tabs from '$lib/components/ui/tabs';
	import {
		Select, SelectContent, SelectItem, SelectTrigger
	} from '$lib/components/ui/select';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import { SquareKanban, Plus, Trash2, CircleCheck, RotateCw, Lock } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { ProjectTemplate, type Project } from '$lib/proto/distroface/v1/project_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgId = $derived(ctx.org?.id ?? '');
	const orgName = $derived(page.params.name ?? '');

	const templates = [
		{ value: ProjectTemplate.BASIC_KANBAN, label: 'Basic kanban', help: 'To do, In progress, Done' },
		{ value: ProjectTemplate.BUG_TRIAGE, label: 'Bug triage', help: 'Needs triage, High, Low, Closed' },
		{ value: ProjectTemplate.NONE, label: 'Empty', help: 'Start with no columns' }
	];

	let projects = $state<Project[]>([]);
	const pager = new Pager(20);
	let loading = $state(true);
	let loaded = $state(false);
	let stateFilter = $state('open');

	let createOpen = $state(false);
	let newTitle = $state('');
	let newDescription = $state('');
	let newTemplate = $state<ProjectTemplate>(ProjectTemplate.BASIC_KANBAN);
	let newPublic = $state(false);
	let creating = $state(false);

	const visibilities = [
		{ value: 'private', label: 'Private', help: 'Only organization members see this board' },
		{ value: 'public', label: 'Public', help: 'Anyone who can see the organization sees this board' }
	];

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

	function openCreate() {
		newTitle = '';
		newDescription = '';
		newTemplate = ProjectTemplate.BASIC_KANBAN;
		newPublic = false;
		createOpen = true;
	}

	async function createProject() {
		if (!newTitle.trim()) return;
		creating = true;
		try {
			await rpcClient.project.createProject({
				orgId,
				title: newTitle.trim(),
				description: newDescription.trim(),
				template: newTemplate,
				public: newPublic
			});
			toast.success('Project created');
			createOpen = false;
			if (stateFilter !== 'open') {
				stateFilter = 'open';
				pager.reset();
			}
			await loadProjects();
		} catch {
			// error interceptor
		} finally {
			creating = false;
		}
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
			<p class="section-subtitle">Boards that track issues across the organization's repositories.</p>
		</div>
		{#if ctx.canAdmin}
			<Button size="sm" class="shrink-0" onclick={openCreate}>
				<Plus class="h-4 w-4 mr-1.5" />
				New Project
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
		<div class="space-y-2">
			{#each { length: 3 }, i (i)}
				<Skeleton class="h-16 w-full rounded-lg" />
			{/each}
		</div>
	{:else if projects.length === 0}
		<EmptyState
			icon={SquareKanban}
			message={stateFilter === 'closed' ? 'No closed projects' : 'No open projects'}
			description="Projects collect issues from any repository onto one board."
		/>
	{:else}
		<div class="rounded-xl border border-border/60 divide-y divide-border/40 transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			{#each projects as project (project.id)}
				<div class="flex flex-wrap items-center gap-3 sm:gap-4 px-4 py-3">
					<div class="h-9 w-9 rounded-lg bg-primary/10 flex items-center justify-center shrink-0">
						<SquareKanban class="h-4.5 w-4.5 text-primary" />
					</div>
					<div class="min-w-0 flex-1">
						<div class="flex items-center gap-2 min-w-0">
							<a href={resolve('/orgs/[name]/projects/[id]', { name: orgName, id: project.id })} class="font-medium hover:text-primary transition-colors truncate">
								{project.title}
							</a>
							{#if !project.public}
								<Badge variant="outline" class="text-[11px] gap-1 text-muted-foreground"><Lock class="h-3 w-3" />Private</Badge>
							{/if}
							{#if project.state === 'closed'}
								<Badge variant="outline" class="text-[11px]">Closed</Badge>
							{/if}
						</div>
						<p class="text-[13px] text-muted-foreground mt-0.5 truncate">
							{project.description || 'No description'}
						</p>
						<p class="text-[12px] text-muted-foreground/80 mt-1 tabular-nums">
							{project.openCount} open, {project.closedCount} closed
							{#if project.updatedAt}
								<span class="mx-1">/</span> updated {relativeTime(timestampDate(project.updatedAt))}
							{/if}
							{#if project.creator}
								<span class="mx-1">/</span> by {project.creator}
							{/if}
						</p>
					</div>
					{#if ctx.canAdmin}
						<div class="flex items-center gap-1 shrink-0 ml-auto">
							<Button
								variant="ghost" size="sm" class="h-8"
								disabled={!!toggling[project.id]}
								onclick={() => toggleClosed(project)}
							>
								{#if project.state === 'closed'}
									<RotateCw class="h-3.5 w-3.5 mr-1.5" />
									Reopen
								{:else}
									<CircleCheck class="h-3.5 w-3.5 mr-1.5" />
									Close
								{/if}
							</Button>
							<Button
								variant="ghost" size="icon"
								class="h-8 w-8 text-destructive hover:text-destructive"
								onclick={() => { deleteTarget = project; deleteOpen = true; }}
								aria-label="Delete"
							>
								<Trash2 class="h-3.5 w-3.5" />
							</Button>
						</div>
					{/if}
				</div>
			{/each}
			<DataPagination attached {pager} onChange={loadProjects} />
		</div>
	{/if}
</div>

<FormPanel bind:open={createOpen} title="New Project" description="A board for issues from any repository in the organization." icon={SquareKanban}>
	<div class="space-y-6">
		<FormSection title="Project">
			<div class="space-y-3">
				<FormField label="Title" id="project-title" required>
					<Input id="project-title" bind:value={newTitle} placeholder="Q4 roadmap" />
				</FormField>
				<FormField label="Description" id="project-desc">
					<Textarea id="project-desc" bind:value={newDescription} rows={2} placeholder="What this board tracks" />
				</FormField>
				<FormField label="Template" id="project-template" help={templates.find((t) => t.value === newTemplate)?.help}>
					<Select
						type="single"
						value={String(newTemplate)}
						onValueChange={(v) => { if (v) newTemplate = Number(v) as ProjectTemplate; }}
					>
						<SelectTrigger id="project-template" class="w-full">
							{templates.find((t) => t.value === newTemplate)?.label ?? 'Select template'}
						</SelectTrigger>
						<SelectContent>
							{#each templates as t (t.value)}
								<SelectItem value={String(t.value)}>{t.label}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
				</FormField>
				<FormField label="Visibility" id="project-visibility" help={visibilities.find((v) => v.value === (newPublic ? 'public' : 'private'))?.help}>
					<Select type="single" value={newPublic ? 'public' : 'private'} onValueChange={(v) => (newPublic = v === 'public')}>
						<SelectTrigger id="project-visibility" class="w-full">{newPublic ? 'Public' : 'Private'}</SelectTrigger>
						<SelectContent>
							{#each visibilities as v (v.value)}
								<SelectItem value={v.value}>{v.label}</SelectItem>
							{/each}
						</SelectContent>
					</Select>
				</FormField>
			</div>
		</FormSection>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (createOpen = false)}>Cancel</Button>
		<Button onclick={createProject} disabled={creating || !newTitle.trim()}>
			{creating ? 'Creating...' : 'Create Project'}
		</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteOpen} title="Delete Project" confirmLabel="Delete" onConfirm={confirmDelete} loading={deleting} icon={Trash2}>
	{#snippet description()}
		Delete <strong>{deleteTarget?.title}</strong>? Issues stay, only the board is removed.
	{/snippet}
</ConfirmDialog>
