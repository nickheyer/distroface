<script lang="ts">
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { SquareKanban, Plus, Trash2, CircleCheck, RotateCw, Lock } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { GitUnit } from '$lib/proto/distroface/v1/git_pb';
	import type { Project } from '$lib/proto/distroface/v1/project_pb';
	import { relativeTime, cn } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { toast } from 'svelte-sonner';

	const repo = $derived(gitRepoStore.repo!);
	const namespace = $derived(repo.namespace);
	const name = $derived(repo.name);
	const canEdit = $derived(gitRepoStore.canWrite(GitUnit.PROJECTS) && !repo.isArchived);
	const newHref = $derived(resolve('/[namespace]/[name]/projects/new', { namespace, name }));

	let projects = $state<Project[]>([]);
	let loading = $state(true);
	let stateFilter = $state<'open' | 'closed'>('open');
	const pager = new Pager(20);

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.project.listProjects(
				{ namespace, name, state: stateFilter, page: pager.request() },
				silentCallOptions
			);
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
		<div class="inline-flex items-center rounded-md border p-0.5">
			<button
				type="button"
				class={cn(
					'rounded-sm px-2.5 py-1 text-xs font-medium transition-colors',
					stateFilter === 'open'
						? 'bg-muted text-foreground'
						: 'text-muted-foreground hover:text-foreground'
				)}
				aria-pressed={stateFilter === 'open'}
				onclick={() => setTab('open')}>Open</button
			>
			<button
				type="button"
				class={cn(
					'rounded-sm px-2.5 py-1 text-xs font-medium transition-colors',
					stateFilter === 'closed'
						? 'bg-muted text-foreground'
						: 'text-muted-foreground hover:text-foreground'
				)}
				aria-pressed={stateFilter === 'closed'}
				onclick={() => setTab('closed')}>Closed</button
			>
		</div>
		<span class="text-xs text-muted-foreground tabular-nums"
			>{pager.totalCount || projects.length} project{(pager.totalCount || projects.length) === 1
				? ''
				: 's'}</span
		>
		<div class="ml-auto">
			<PermissionGate allowed={canEdit}>
				<Button href={newHref}><Plus />New project</Button>
			</PermissionGate>
		</div>
	</div>

	{#if loading && projects.length === 0}
		<div class="box divide-y">
			{#each { length: 3 }, i (i)}
				<div class="flex items-center gap-3 px-4 py-3">
					<Skeleton class="size-4 rounded-sm" />
					<div class="flex-1 space-y-2">
						<Skeleton class="h-4 w-40" />
						<Skeleton class="h-3 w-64" />
					</div>
				</div>
			{/each}
		</div>
	{:else if projects.length === 0}
		<EmptyState
			icon={SquareKanban}
			message={stateFilter === 'closed' ? 'No closed projects' : 'No open projects'}
			description="Projects are kanban boards that track issues across columns."
		>
			{#snippet actions()}
				{#if canEdit && stateFilter === 'open'}<Button variant="outline" href={newHref}
						><Plus />New project</Button
					>{/if}
			{/snippet}
		</EmptyState>
	{:else}
		<div class="box divide-y transition-opacity {loading ? 'opacity-60' : ''}">
			{#each projects as p (p.id)}
				<div class="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 py-3">
					<SquareKanban class="size-4 shrink-0 text-muted-foreground" />
					<div class="min-w-0 flex-1 basis-48">
						<div class="flex min-w-0 items-center gap-2">
							<a
								href={resolve('/[namespace]/[name]/projects/[id]', { namespace, name, id: p.id })}
								class="truncate text-sm font-semibold transition-colors hover:text-primary"
								>{p.title}</a
							>
							{#if !p.public}<Badge variant="outline"><Lock />Private</Badge>{/if}
							{#if p.state === 'closed'}<Badge variant="outline">Closed</Badge>{/if}
						</div>
						<p class="mt-0.5 truncate text-sm text-muted-foreground">
							{p.description || 'No description'}
						</p>
						<p class="mt-1 text-xs text-muted-foreground tabular-nums">
							{p.openCount} open, {p.closedCount} closed
							{#if p.updatedAt}<span class="mx-1">/</span> updated {relativeTime(
									timestampDate(p.updatedAt)
								)}{/if}
							{#if p.creator}<span class="mx-1">/</span> by {p.creator}{/if}
						</p>
					</div>
					{#if canEdit}
						<div class="ml-auto flex shrink-0 items-center gap-1">
							<Button
								variant="ghost"
								size="sm"
								disabled={toggling === p.id}
								onclick={() => toggleClosed(p)}
							>
								{#if p.state === 'closed'}<RotateCw />Reopen{:else}<CircleCheck />Close{/if}
							</Button>
							<Button
								variant="ghost-muted"
								size="icon-sm"
								class="text-destructive hover:text-destructive"
								onclick={() => {
									deleteTarget = p;
									deleteOpen = true;
								}}
								aria-label="Delete"><Trash2 /></Button
							>
						</div>
					{/if}
				</div>
			{/each}
			<DataPagination attached {pager} onChange={load} />
		</div>
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
		This deletes <strong>{deleteTarget?.title}</strong> and its columns. Issues stay in the repository.
	{/snippet}
</ConfirmDialog>
