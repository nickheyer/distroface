<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import {
		SquareKanban,
		ArrowLeft,
		Pencil,
		CircleCheck,
		RotateCw,
		Trash2,
		Lock
	} from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import KanbanBoard from '$lib/components/git/kanban-board.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import type { Project } from '$lib/proto/distroface/v1/project_pb';
	import { toast } from 'svelte-sonner';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const base = $derived(`/${namespace}/${name}`);
	const id = $derived(page.params.id ?? '');
	const canEdit = $derived(gitRepoStore.canWrite(GitUnit.PROJECTS) && !repo.isArchived);
	const settingsHref = $derived(
		resolve('/[namespace]/[name]/projects/[id]/settings', { namespace, name, id })
	);

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
		gitRepoStore.load(namespace, name);
		load();
	});

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
			await goto(resolve('/[namespace]/[name]/projects', { namespace, name }));
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}
</script>

{#if notFound}
	<EmptyState
		icon={SquareKanban}
		message="Project not found"
		description="This board does not exist or you cannot see it."
	>
		{#snippet actions()}
			<Button variant="outline" href="{base}/projects"><ArrowLeft />Back to projects</Button>
		{/snippet}
	</EmptyState>
{:else if !project}
	<div class="space-y-4">
		<Skeleton class="h-8 w-64" />
		<div class="flex gap-4">
			{#each { length: 3 }, i (i)}<Skeleton class="h-80 w-72 rounded-lg" />{/each}
		</div>
	</div>
{:else}
	<div class="space-y-4">
		<div class="flex flex-wrap items-start gap-3">
			<Button variant="ghost" href="{base}/projects"><ArrowLeft />Projects</Button>
			<div class="min-w-0 flex-1 basis-48">
				<div class="flex flex-wrap items-center gap-2">
					<h2 class="section-title min-w-0 wrap-break-word">{project.title}</h2>
					{#if !project.public}<Badge
							variant="outline"
							title="Only collaborators with write access to projects see this board"
							><Lock />Private</Badge
						>{/if}
					{#if project.state === 'closed'}<Badge variant="outline">Closed</Badge>{/if}
					<span class="text-xs text-muted-foreground tabular-nums"
						>{project.openCount} open, {project.closedCount} closed</span
					>
				</div>
				{#if project.description}<p class="section-subtitle">{project.description}</p>{/if}
			</div>
			{#if canEdit}
				<div class="ml-auto flex flex-wrap items-center gap-1">
					<Button variant="outline" href={settingsHref}><Pencil />Edit</Button>
					<Button variant="outline" onclick={toggleClosed} disabled={toggling}>
						{#if project.state === 'closed'}<RotateCw />Reopen{:else}<CircleCheck />Close{/if}
					</Button>
					<Button
						variant="ghost-muted"
						size="icon"
						class="text-destructive hover:text-destructive"
						onclick={() => (deleteOpen = true)}
						aria-label="Delete project"><Trash2 /></Button
					>
				</div>
			{/if}
		</div>

		<KanbanBoard {project} canEdit={canEdit && project.state !== 'closed'} onChanged={load} />
	</div>
{/if}

<ConfirmDialog
	bind:open={deleteOpen}
	title="Delete project"
	confirmLabel="Delete"
	onConfirm={confirmDelete}
	loading={deleting}
	icon={Trash2}
>
	{#snippet description()}
		This deletes <strong>{project?.title}</strong> and its columns. Issues stay in the repository.
	{/snippet}
</ConfirmDialog>

<svelte:head>
	<title>{project?.title ?? 'Project'} - {namespace}/{name}</title>
</svelte:head>
