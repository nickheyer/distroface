<script lang="ts">
	import { onMount } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import FormPanel from '$lib/components/form-panel.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import { Archive, Plus, Trash2, Lock, Globe, Search } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime, pageToToken, formatBytes } from '$lib/utils';
	import type { ArtifactRepository } from '$lib/proto/distroface/v1/types_pb';

	let { namespace, canCreate = false }: { namespace: string; canCreate?: boolean } = $props();

	let repos = $state<ArtifactRepository[]>([]);
	let loading = $state(true);
	let totalCount = $state(0);
	let currentPage = $state(1);
	const pageSize = 20;
	let searchQuery = $state('');
	let searchTimeout: ReturnType<typeof setTimeout> | undefined;

	let createPanelOpen = $state(false);
	let newName = $state('');
	let newDescription = $state('');
	let newPrivate = $state(false);
	let creating = $state(false);

	let deleteDialogOpen = $state(false);
	let deleteTarget = $state<ArtifactRepository | null>(null);
	let deleting = $state(false);

	async function loadRepos() {
		loading = true;
		try {
			const resp = await rpcClient.artifact.listArtifactRepositories({
				namespace,
				search: searchQuery.trim(),
				pageSize,
				pageToken: pageToToken(currentPage, pageSize)
			});
			repos = resp.repositories;
			totalCount = Number(resp.totalCount);
		} catch {
			repos = [];
			totalCount = 0;
		} finally {
			loading = false;
		}
	}

	function handleSearchInput() {
		clearTimeout(searchTimeout);
		searchTimeout = setTimeout(() => {
			currentPage = 1;
			loadRepos();
		}, 300);
	}

	async function createRepo() {
		if (!newName.trim()) return;
		creating = true;
		try {
			await rpcClient.artifact.createArtifactRepository({
				name: newName.trim(),
				namespace,
				description: newDescription.trim(),
				isPrivate: newPrivate
			});
			toast.success('Repository created');
			closeCreatePanel();
			await loadRepos();
		} catch {
			// Error interceptor already toasted
		} finally {
			creating = false;
		}
	}

	function closeCreatePanel() {
		createPanelOpen = false;
		newName = '';
		newDescription = '';
		newPrivate = false;
	}

	function openDelete(repo: ArtifactRepository, e: MouseEvent) {
		e.stopPropagation();
		deleteTarget = repo;
		deleteDialogOpen = true;
	}

	async function confirmDelete() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await rpcClient.artifact.deleteArtifactRepository({
				name: deleteTarget.name,
				namespace: deleteTarget.namespace
			});
			toast.success('Repository deleted');
			deleteDialogOpen = false;
			await loadRepos();
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}

	function canDelete(repo: ArtifactRepository): boolean {
		return (
			canCreate ||
			repo.owner === authStore.user?.username ||
			authStore.hasPermission('artifacts', 'manage')
		);
	}

	onMount(loadRepos);
</script>

<div class="space-y-4">
	<div class="section-header">
		<h2 class="section-title">Artifact Repositories</h2>
		{#if canCreate}
			<Button size="sm" onclick={() => (createPanelOpen = true)}>
				<Plus class="h-4 w-4 mr-1.5" />
				New Repository
			</Button>
		{/if}
	</div>

	<div class="relative max-w-md">
		<Search class="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground/50" />
		<Input
			placeholder="Search repositories..."
			class="pl-9 h-9 bg-muted/30 border-border/50 focus-visible:bg-background"
			bind:value={searchQuery}
			oninput={handleSearchInput}
		/>
	</div>

	{#if loading}
		<div class="space-y-2">
			{#each Array(3)}
				<Skeleton class="h-14 w-full rounded-xl" />
			{/each}
		</div>
	{:else if repos.length === 0}
		<EmptyState
			icon={Archive}
			message={searchQuery ? 'No matching repositories' : 'No artifact repositories yet'}
			description={searchQuery
				? 'Try a different search.'
				: `Repositories under ${namespace} hold build artifacts, packages, and other files.`}
		>
			{#snippet actions()}
				{#if canCreate}
					<Button variant="outline" size="sm" onclick={() => (createPanelOpen = true)}>
						<Plus class="h-4 w-4 mr-1.5" />
						New Repository
					</Button>
				{/if}
			{/snippet}
		</EmptyState>
	{:else}
		<div class="data-table">
			<Table>
				<TableHeader>
					<TableRow class="bg-muted/30 hover:bg-muted/30">
						<TableHead class="th">Name</TableHead>
						<TableHead class="th">Artifacts</TableHead>
						<TableHead class="th">Size</TableHead>
						<TableHead class="th">Created</TableHead>
						<TableHead class="th w-16"></TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each repos as repo (repo.id)}
						<TableRow class="cursor-pointer" onclick={() => goto(resolve('/artifacts/[namespace]/[repo]', { namespace: repo.namespace, repo: repo.name }))}>
							<TableCell class="py-3 px-3">
								<div class="flex items-center gap-2">
									<span class="font-medium">{repo.name}</span>
									<Badge
										variant="outline"
										class="text-xs gap-1 {repo.isPrivate ? 'border-amber-500/30 text-amber-600 dark:text-amber-400' : ''}"
									>
										{#if repo.isPrivate}
											<Lock class="h-2.5 w-2.5" />Private
										{:else}
											<Globe class="h-2.5 w-2.5" />Public
										{/if}
									</Badge>
								</div>
								{#if repo.description}
									<p class="text-xs text-muted-foreground mt-0.5 line-clamp-1">{repo.description}</p>
								{/if}
							</TableCell>
							<TableCell class="text-sm py-3 px-3 tabular-nums">{repo.artifactCount}</TableCell>
							<TableCell class="text-sm py-3 px-3 tabular-nums">{formatBytes(Number(repo.totalSize))}</TableCell>
							<TableCell class="text-muted-foreground text-sm py-3 px-3">
								{repo.createdAt ? relativeTime(timestampDate(repo.createdAt)) : '-'}
							</TableCell>
							<TableCell class="text-right py-3 px-3">
								{#if canDelete(repo)}
									<Button
										variant="ghost" size="icon"
										class="h-7 w-7 text-destructive hover:text-destructive"
										onclick={(e) => openDelete(repo, e)}
									>
										<Trash2 class="h-3.5 w-3.5" />
									</Button>
								{/if}
							</TableCell>
						</TableRow>
					{/each}
				</TableBody>
			</Table>
		</div>

		<DataPagination
			page={currentPage} {pageSize} {totalCount}
			onPrev={() => { currentPage--; loadRepos(); }}
			onNext={() => { currentPage++; loadRepos(); }}
		/>
	{/if}
</div>

<FormPanel
	open={createPanelOpen}
	onOpenChange={(v) => { if (!v) closeCreatePanel(); }}
	title="New Artifact Repository"
	description="Create a repository under {namespace} for build artifacts and packages."
	icon={Archive}
>
	<div class="space-y-6">
		<FormSection title="Repository Details">
			<div class="space-y-3">
				<FormField label="Name" id="org-repo-name" required help="Letters, digits, dots, dashes, and underscores.">
					<Input id="org-repo-name" bind:value={newName} placeholder="e.g., build-artifacts" />
				</FormField>
				<FormField label="Description" id="org-repo-description">
					<Input id="org-repo-description" bind:value={newDescription} placeholder="What is stored here?" />
				</FormField>
				<FormField label="Private" help="Private repositories are only visible to members and admins.">
					<Switch bind:checked={newPrivate} />
				</FormField>
			</div>
		</FormSection>
	</div>

	{#snippet footer()}
		<Button variant="outline" onclick={closeCreatePanel}>Cancel</Button>
		<Button onclick={createRepo} disabled={creating || !newName.trim()}>
			{creating ? 'Creating...' : 'Create Repository'}
		</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteDialogOpen} title="Delete Repository" confirmLabel="Delete" onConfirm={confirmDelete} loading={deleting}>
	{#snippet description()}
		Are you sure you want to delete <strong>{deleteTarget?.namespace}/{deleteTarget?.name}</strong>?
		All artifacts in this repository will be permanently removed.
	{/snippet}
</ConfirmDialog>
