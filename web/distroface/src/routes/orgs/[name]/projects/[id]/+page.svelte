<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import { Kanban, ArrowLeft, Pencil, CheckCircle, RotateCw, Trash, Lock } from '$lib/icons';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
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
	const settingsHref = $derived(
		resolve('/orgs/[name]/projects/[id]/settings', { name: orgName, id })
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
			await goto(resolve('/orgs/[name]/projects', { name: orgName }));
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}
</script>

{#if notFound}
	<EmptyState
		icon={Kanban}
		message="Project not found"
		description="This board does not exist or you cannot see it."
	>
		{#snippet actions()}
			<Button variant="outline" href={resolve('/orgs/[name]/projects', { name: orgName })}>
				<ArrowLeft />
				Back to projects
			</Button>
		{/snippet}
	</EmptyState>
{:else if !project}
	<div class="space-y-6">
		<Skeleton class="h-5 w-64" />
		<div class="flex gap-4 overflow-hidden">
			{#each { length: 3 }, i (i)}
				<Skeleton class="h-80 w-72 shrink-0 rounded-lg" />
			{/each}
		</div>
	</div>
{:else}
	<div class="space-y-6">
		<div class="flex flex-wrap items-start gap-3">
			<Button variant="ghost" href={resolve('/orgs/[name]/projects', { name: orgName })}>
				<ArrowLeft />
				Projects
			</Button>
			<div class="min-w-0 flex-1">
				<div class="flex flex-wrap items-center gap-2">
					<h2 class="section-title truncate">{project.title}</h2>
					{#if !project.public}
						<Badge variant="outline" title="Only organization members see this board"
							><Lock />Private</Badge
						>
					{/if}
					{#if project.state === 'closed'}
						<Badge variant="outline">Closed</Badge>
					{/if}
					<span class="text-xs text-muted-foreground tabular-nums">
						{project.openCount} open, {project.closedCount} closed
					</span>
				</div>
				{#if project.description}
					<p class="section-subtitle">{project.description}</p>
				{/if}
			</div>
			{#if canEdit}
				<div class="flex shrink-0 items-center gap-1">
					<Button variant="outline" href={settingsHref}>
						<Pencil />
						Edit
					</Button>
					<Button variant="outline" onclick={toggleClosed} disabled={toggling}>
						{#if project.state === 'closed'}
							<RotateCw />
							Reopen
						{:else}
							<CheckCircle />
							Close
						{/if}
					</Button>
					<Button
						variant="ghost"
						size="icon"
						class="text-destructive hover:text-destructive"
						onclick={() => (deleteOpen = true)}
						aria-label="Delete project"
					>
						<Trash />
					</Button>
				</div>
			{/if}
		</div>

		<KanbanBoard
			{project}
			canEdit={canEdit && project.state !== 'closed'}
			onChanged={load}
			searchNamespace={orgName}
		/>
	</div>
{/if}

<ConfirmDialog
	bind:open={deleteOpen}
	title="Delete project"
	confirmLabel="Delete"
	onConfirm={confirmDelete}
	loading={deleting}
	icon={Trash}
>
	{#snippet description()}
		This deletes <strong>{project?.title}</strong> and its columns. Issues stay in their repositories.
	{/snippet}
</ConfirmDialog>

<svelte:head>
	<title>{project?.title ?? 'Project'} - {orgName}</title>
</svelte:head>
