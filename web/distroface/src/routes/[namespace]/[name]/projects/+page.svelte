<script lang="ts">
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { SquareKanban, Plus, Trash2, CircleCheck, RotateCw, Lock } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import { ProjectTemplate, type Project } from '$lib/proto/distroface/v1/project_pb';
	import { relativeTime, cn } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const canEdit = $derived(gitRepoStore.canWrite(GitUnit.PROJECTS) && !repo.isArchived);

	const templates = [
		{ value: ProjectTemplate.BASIC_KANBAN, label: 'Basic kanban', help: 'To do, In progress, Done' },
		{ value: ProjectTemplate.BUG_TRIAGE, label: 'Bug triage', help: 'Needs triage, High, Low, Closed' },
		{ value: ProjectTemplate.NONE, label: 'Empty', help: 'Start with no columns' }
	];

	let projects = $state<Project[]>([]);
	let loading = $state(true);
	let stateFilter = $state<'open' | 'closed'>('open');
	const pager = new Pager(20);

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.project.listProjects({ namespace, name, state: stateFilter, page: pager.request() }, silentCallOptions);
			projects = resp.projects;
			pager.apply(resp.page);
		} catch {
			projects = [];
			pager.apply();
		} finally {
			loading = false;
		}
	}

	function setTab(tab: 'open' | 'closed') {
		if (stateFilter === tab) return;
		stateFilter = tab;
		pager.reset();
		load();
	}

	onMount(() => {
		gitRepoStore.load(namespace, name);
		load();
	});

	let createOpen = $state(false);
	let newTitle = $state('');
	let newDescription = $state('');
	let newTemplate = $state<ProjectTemplate>(ProjectTemplate.BASIC_KANBAN);
	let newPublic = $state(false);
	let creating = $state(false);

	const visibilities = [
		{ value: 'private', label: 'Private', help: 'Only collaborators with write access to projects see this board' },
		{ value: 'public', label: 'Public', help: 'Anyone who can see the repository sees this board' }
	];

	function openCreate() {
		newTitle = '';
		newDescription = '';
		newTemplate = ProjectTemplate.BASIC_KANBAN;
		newPublic = false;
		createOpen = true;
	}

	async function createProject() {
		if (!newTitle.trim() || creating) return;
		creating = true;
		try {
			await rpcClient.project.createProject({ namespace, name, title: newTitle.trim(), description: newDescription.trim(), template: newTemplate, public: newPublic });
			toast.success('Project created');
			createOpen = false;
			if (stateFilter !== 'open') {
				stateFilter = 'open';
				pager.reset();
			}
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			creating = false;
		}
	}

	let toggling = $state('');
	async function toggleClosed(p: Project) {
		toggling = p.id;
		try {
			await rpcClient.project.closeProject({ id: p.id, closed: p.state !== 'closed' });
			toast.success(p.state === 'closed' ? 'Project reopened' : 'Project closed');
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			toggling = '';
		}
	}

	let deleteOpen = $state(false);
	let deleteTarget = $state<Project | null>(null);
	let deleting = $state(false);

	async function confirmDelete() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await rpcClient.project.deleteProject({ id: deleteTarget.id });
			toast.success('Project deleted');
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
			<button type="button" class={cn('px-3 py-1 text-[12.5px] rounded-md', stateFilter === 'open' ? 'bg-muted font-semibold' : 'text-muted-foreground hover:text-foreground')} onclick={() => setTab('open')}>Open</button>
			<button type="button" class={cn('px-3 py-1 text-[12.5px] rounded-md', stateFilter === 'closed' ? 'bg-muted font-semibold' : 'text-muted-foreground hover:text-foreground')} onclick={() => setTab('closed')}>Closed</button>
		</div>
		<span class="text-[12.5px] text-muted-foreground tabular-nums">{pager.totalCount || projects.length} project{(pager.totalCount || projects.length) === 1 ? '' : 's'}</span>
		<div class="ml-auto">
			<PermissionGate allowed={canEdit}>
				<Button size="sm" class="h-8" onclick={openCreate}><Plus class="h-3.5 w-3.5 mr-1.5" />New Project</Button>
			</PermissionGate>
		</div>
	</div>

	{#if loading && projects.length === 0}
		<div class="space-y-2">
			{#each { length: 3 }, i (i)}
				<Skeleton class="h-16 w-full rounded-lg" />
			{/each}
		</div>
	{:else if projects.length === 0}
		<EmptyState icon={SquareKanban} message={stateFilter === 'closed' ? 'No closed projects' : 'No open projects'} description="Projects are kanban boards that track issues across columns.">
			{#snippet actions()}
				{#if canEdit && stateFilter === 'open'}<Button size="sm" onclick={openCreate}><Plus class="h-3.5 w-3.5 mr-1.5" />New Project</Button>{/if}
			{/snippet}
		</EmptyState>
	{:else}
		<div class="rounded-xl border border-border/60 divide-y divide-border/40 transition-opacity {loading ? 'opacity-60' : ''}">
			{#each projects as p (p.id)}
				<div class="flex flex-wrap items-center gap-x-4 gap-y-2 px-4 py-3">
					<div class="h-9 w-9 rounded-lg bg-primary/10 flex items-center justify-center shrink-0">
						<SquareKanban class="h-4.5 w-4.5 text-primary" />
					</div>
					<div class="min-w-0 flex-1 basis-48">
						<div class="flex items-center gap-2 min-w-0">
							<a href={resolve('/[namespace]/[name]/projects/[id]', { namespace, name, id: p.id })} class="font-medium hover:text-primary transition-colors truncate">{p.title}</a>
							{#if !p.public}<Badge variant="outline" class="text-[11px] gap-1 text-muted-foreground"><Lock class="h-3 w-3" />Private</Badge>{/if}
							{#if p.state === 'closed'}<Badge variant="outline" class="text-[11px]">Closed</Badge>{/if}
						</div>
						<p class="text-[13px] text-muted-foreground mt-0.5 truncate">{p.description || 'No description'}</p>
						<p class="text-[12px] text-muted-foreground/80 mt-1 tabular-nums">
							{p.openCount} open, {p.closedCount} closed
							{#if p.updatedAt}<span class="mx-1">/</span> updated {relativeTime(timestampDate(p.updatedAt))}{/if}
							{#if p.creator}<span class="mx-1">/</span> by {p.creator}{/if}
						</p>
					</div>
					{#if canEdit}
						<div class="flex items-center gap-1 shrink-0 ml-auto">
							<Button variant="ghost" size="sm" class="h-8" disabled={toggling === p.id} onclick={() => toggleClosed(p)}>
								{#if p.state === 'closed'}<RotateCw class="h-3.5 w-3.5 mr-1.5" />Reopen{:else}<CircleCheck class="h-3.5 w-3.5 mr-1.5" />Close{/if}
							</Button>
							<Button variant="ghost" size="icon" class="h-8 w-8 text-destructive hover:text-destructive" onclick={() => { deleteTarget = p; deleteOpen = true; }} aria-label="Delete"><Trash2 class="h-3.5 w-3.5" /></Button>
						</div>
					{/if}
				</div>
			{/each}
			<DataPagination attached {pager} onChange={load} />
		</div>
	{/if}
</div>

<FormPanel bind:open={createOpen} title="New project" description="A kanban board for this repository's issues and pull requests." icon={SquareKanban}>
	<div class="space-y-3">
		<FormField label="Title" id="project-title" required>
			<Input id="project-title" bind:value={newTitle} placeholder="Release 2.0" />
		</FormField>
		<FormField label="Description" id="project-desc">
			<Textarea id="project-desc" bind:value={newDescription} rows={2} placeholder="What this board tracks" />
		</FormField>
		<FormField label="Template" id="project-template" help={templates.find((t) => t.value === newTemplate)?.help}>
			<Select type="single" value={String(newTemplate)} onValueChange={(v) => { if (v) newTemplate = Number(v) as ProjectTemplate; }}>
				<SelectTrigger id="project-template" class="w-full">{templates.find((t) => t.value === newTemplate)?.label ?? 'Select template'}</SelectTrigger>
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
	{#snippet footer()}
		<Button variant="outline" onclick={() => (createOpen = false)}>Cancel</Button>
		<Button onclick={createProject} disabled={creating || !newTitle.trim()}>{creating ? 'Creating...' : 'Create Project'}</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteOpen} title="Delete project" confirmLabel="Delete" onConfirm={confirmDelete} loading={deleting} icon={Trash2}>
	{#snippet description()}
		This deletes <strong>{deleteTarget?.title}</strong> and its columns. Issues stay in the repository.
	{/snippet}
</ConfirmDialog>
