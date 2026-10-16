<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import { SquareKanban, ArrowLeft, Pencil, CircleCheck, RotateCw, Trash2, Lock } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import KanbanBoard from '$lib/components/git/kanban-board.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import type { Project } from '$lib/proto/distroface/v1/project_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';
	import { toast } from 'svelte-sonner';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');
	const id = $derived(page.params.id ?? '');
	const canEdit = $derived(ctx.canAdmin);

	let project = $state<Project | undefined>();
	let notFound = $state(false);
	let loadedId = '';

	async function load() {
		try {
			const resp = await rpcClient.project.getProject({ id }, silentCallOptions);
			project = resp.project;
			notFound = !project;
		} catch {
			project = undefined;
			notFound = true;
		}
	}

	$effect(() => {
		if (!id || loadedId === id) return;
		loadedId = id;
		load();
	});

	let editOpen = $state(false);
	let fTitle = $state('');
	let fDescription = $state('');
	let fPublic = $state(false);
	let saving = $state(false);

	const visibilities = [
		{ value: 'private', label: 'Private', help: 'Only organization members see this board' },
		{ value: 'public', label: 'Public', help: 'Anyone who can see the organization sees this board' }
	];

	function openEdit() {
		if (!project) return;
		fTitle = project.title;
		fDescription = project.description;
		fPublic = project.public;
		editOpen = true;
	}

	async function save() {
		if (!project || !fTitle.trim() || saving) return;
		saving = true;
		try {
			await rpcClient.project.updateProject({ id: project.id, title: fTitle.trim(), description: fDescription.trim(), public: fPublic });
			editOpen = false;
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			saving = false;
		}
	}

	let toggling = $state(false);
	async function toggleClosed() {
		if (!project) return;
		toggling = true;
		try {
			await rpcClient.project.closeProject({ id: project.id, closed: project.state !== 'closed' });
			toast.success(project.state === 'closed' ? 'Project reopened' : 'Project closed');
			await load();
		} catch {
			// Error interceptor already toasted
		} finally {
			toggling = false;
		}
	}

	let deleteOpen = $state(false);
	let deleting = $state(false);
	async function confirmDelete() {
		if (!project) return;
		deleting = true;
		try {
			await rpcClient.project.deleteProject({ id: project.id });
			toast.success('Project deleted');
			await goto(resolve('/orgs/[name]/projects', { name: orgName }));
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}
</script>

{#if notFound}
	<EmptyState icon={SquareKanban} message="Project not found" description="This board does not exist or you cannot see it.">
		{#snippet actions()}
			<Button variant="outline" size="sm" href={resolve('/orgs/[name]/projects', { name: orgName })}><ArrowLeft class="h-3.5 w-3.5 mr-1.5" />Back to Projects</Button>
		{/snippet}
	</EmptyState>
{:else if !project}
	<div class="space-y-4">
		<Skeleton class="h-8 w-64" />
		<div class="flex gap-4 overflow-hidden">
			{#each { length: 3 }, i (i)}<Skeleton class="h-80 w-72 shrink-0 rounded-xl" />{/each}
		</div>
	</div>
{:else}
	<div class="space-y-4">
		<div class="flex items-start gap-3 flex-wrap">
			<Button variant="ghost" size="sm" class="h-8" href={resolve('/orgs/[name]/projects', { name: orgName })}><ArrowLeft class="h-3.5 w-3.5 mr-1" />Projects</Button>
			<div class="min-w-0 flex-1">
				<div class="flex items-center gap-2 flex-wrap">
					<h2 class="section-title truncate">{project.title}</h2>
					{#if !project.public}<Badge variant="outline" class="text-[11px] gap-1 text-muted-foreground" title="Only organization members see this board"><Lock class="h-3 w-3" />Private</Badge>{/if}
					{#if project.state === 'closed'}<Badge variant="outline" class="text-[11px]">Closed</Badge>{/if}
					<span class="text-[12.5px] text-muted-foreground tabular-nums">{project.openCount} open, {project.closedCount} closed</span>
				</div>
				{#if project.description}<p class="section-subtitle mt-0.5">{project.description}</p>{/if}
			</div>
			{#if canEdit}
				<div class="flex items-center gap-1 shrink-0">
					<Button variant="outline" size="sm" class="h-8" onclick={openEdit}><Pencil class="h-3.5 w-3.5 mr-1.5" />Edit</Button>
					<Button variant="outline" size="sm" class="h-8" onclick={toggleClosed} disabled={toggling}>
						{#if project.state === 'closed'}<RotateCw class="h-3.5 w-3.5 mr-1.5" />Reopen{:else}<CircleCheck class="h-3.5 w-3.5 mr-1.5" />Close{/if}
					</Button>
					<Button variant="ghost" size="icon" class="h-8 w-8 text-destructive hover:text-destructive" onclick={() => (deleteOpen = true)} aria-label="Delete project"><Trash2 class="h-3.5 w-3.5" /></Button>
				</div>
			{/if}
		</div>

		<KanbanBoard {project} canEdit={canEdit && project.state !== 'closed'} onChanged={load} searchNamespace={orgName} />
	</div>
{/if}

<FormPanel bind:open={editOpen} title="Edit Project" description="Title and description shown on the board." icon={SquareKanban}>
	<div class="space-y-3">
		<FormField label="Title" id="org-project-title" required>
			<Input id="org-project-title" bind:value={fTitle} />
		</FormField>
		<FormField label="Description" id="org-project-desc">
			<Textarea id="org-project-desc" bind:value={fDescription} rows={2} />
		</FormField>
		<FormField label="Visibility" id="org-project-visibility" help={visibilities.find((v) => v.value === (fPublic ? 'public' : 'private'))?.help}>
			<Select type="single" value={fPublic ? 'public' : 'private'} onValueChange={(v) => (fPublic = v === 'public')}>
				<SelectTrigger id="org-project-visibility" class="w-full">{fPublic ? 'Public' : 'Private'}</SelectTrigger>
				<SelectContent>
					{#each visibilities as v (v.value)}
						<SelectItem value={v.value}>{v.label}</SelectItem>
					{/each}
				</SelectContent>
			</Select>
		</FormField>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (editOpen = false)}>Cancel</Button>
		<Button onclick={save} disabled={saving || !fTitle.trim()}>{saving ? 'Saving...' : 'Save Changes'}</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteOpen} title="Delete Project" confirmLabel="Delete" onConfirm={confirmDelete} loading={deleting} icon={Trash2}>
	{#snippet description()}
		This deletes <strong>{project?.title}</strong> and its columns. Issues stay in their repositories.
	{/snippet}
</ConfirmDialog>

<svelte:head>
	<title>{project?.title ?? 'Project'} - {orgName}</title>
</svelte:head>
