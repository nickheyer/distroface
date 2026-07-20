<script lang="ts">
	import { onMount } from 'svelte';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
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
	import {
		Select, SelectContent, SelectItem, SelectTrigger
	} from '$lib/components/ui/select';
	import FormPanel from '$lib/components/form-panel.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import MirrorBadge from '$lib/components/mirror-badge.svelte';
	import MirrorConfigFields, {
		emptyMirrorForm, mirrorInit
	} from '$lib/components/mirror-config-fields.svelte';
	import { Archive, Plus, Trash2, Lock, Globe } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime, formatBytes } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { ArtifactRepoType } from '$lib/proto/distroface/v1/types_pb';
	import type { ArtifactRepository } from '$lib/proto/distroface/v1/types_pb';
	import {
		artifactRepoTypeOptions, artifactMirrorKind, artifactMirrorLabel
	} from '$lib/mirror';

	let { namespace, canCreate = false }: { namespace: string; canCreate?: boolean } = $props();

	let repos = $state<ArtifactRepository[]>([]);
	let loading = $state(true);
	let loaded = $state(false);
	const pager = new Pager(20);
	const filter = new QueryFilter([
		{ key: 'name', label: 'Name' },
		{ key: 'namespace', label: 'Namespace' },
		{ key: 'description', label: 'Description' }
	]);

	let createPanelOpen = $state(false);
	let newName = $state('');
	let newDescription = $state('');
	let newPrivate = $state(false);
	let newType = $state<ArtifactRepoType>(ArtifactRepoType.FILE);
	let newMirror = $state(emptyMirrorForm());
	let creating = $state(false);

	let deleteDialogOpen = $state(false);
	let deleteTarget = $state<ArtifactRepository | null>(null);
	let deleting = $state(false);

	async function loadRepos() {
		loading = true;
		try {
			const resp = await rpcClient.artifact.listArtifactRepositories({
				page: pager.request(filter.request()),
				namespace
			});
			repos = resp.repositories;
			pager.apply(resp.page);
		} catch {
			repos = [];
			pager.apply();
		} finally {
			loading = false;
			loaded = true;
		}
	}

	function filterChanged() {
		pager.reset();
		loadRepos();
	}

	const isMirror = $derived(newType !== ArtifactRepoType.FILE);

	async function createRepo() {
		if (!newName.trim() || (isMirror && !newMirror.upstream.trim())) return;
		creating = true;
		try {
			await rpcClient.artifact.createArtifactRepository({
				name: newName.trim(),
				namespace,
				description: newDescription.trim(),
				isPrivate: newPrivate,
				type: newType,
				mirror: isMirror ? mirrorInit(newMirror) : undefined
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
		newType = ArtifactRepoType.FILE;
		newMirror = emptyMirrorForm();
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

	onMount(() => {
		mirrorSyncStore.ensure();
		loadRepos();
	});

	// Finished syncs refresh the list live
	let syncSeqSeen = 0;
	$effect(() => {
		const seq = mirrorSyncStore.finishedSeq;
		if (seq === syncSeqSeen) return;
		syncSeqSeen = seq;
		if (mirrorSyncStore.lastFinished?.kind === 'artifact') loadRepos();
	});
</script>

<div class="space-y-4">
	<div class="section-header">
		<h2 class="section-title">Artifact Repositories</h2>
		{#if canCreate}
			<Button size="sm" onclick={() => (createPanelOpen = true)}>
				<Plus class="h-4 w-4 mr-1.5" />New Repository
			</Button>
		{/if}
	</div>

	<div class="max-w-md">
		<QueryFilterBar {filter} placeholder="Search repositories..." onchange={filterChanged} />
	</div>

	{#if !loaded}
		<div class="space-y-2">
			{#each Array(3)}
				<Skeleton class="h-14 w-full rounded-xl" />
			{/each}
		</div>
	{:else if repos.length === 0}
		<EmptyState
			icon={Archive}
			message={filter.active ? 'No artifact repositories found' : 'No artifact repositories yet'}
			description={filter.active
				? 'Try a different search.'
				: `Repositories under ${namespace} hold build artifacts, packages, and other files.`}
		>
			{#snippet actions()}
				{#if canCreate}
					<Button variant="outline" size="sm" onclick={() => (createPanelOpen = true)}>
						<Plus class="h-4 w-4 mr-1.5" />New Artifact Repository
					</Button>
				{/if}
			{/snippet}
		</EmptyState>
	{:else}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
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
									{#if repo.type !== ArtifactRepoType.FILE && repo.type !== ArtifactRepoType.UNSPECIFIED}
										<MirrorBadge
											label={artifactMirrorLabel(repo.type)}
											error={repo.mirrorLastError}
											title={repo.mirror?.upstream ?? ''}
											syncing={repo.mirrorSyncing || mirrorSyncStore.syncing('artifact', repo.id)}
										/>
									{/if}
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
			<DataPagination attached {pager} onChange={loadRepos} />
		</div>
	{/if}
</div>

<FormPanel
	open={createPanelOpen}
	onOpenChange={(v) => { if (!v) closeCreatePanel(); }}
	title="New Artifact Repository"
	description="Build artifacts and packages under {namespace}."
	icon={Archive}
>
	<div class="space-y-6">
		<FormSection title="Repository Details">
			<div class="space-y-3">
				<FormField label="Name" id="org-repo-name" required help="Letters, digits, dots, dashes, underscores">
					<Input id="org-repo-name" bind:value={newName} placeholder="build-artifacts" />
				</FormField>
				<FormField label="Description" id="org-repo-description">
					<Input id="org-repo-description" bind:value={newDescription} placeholder="What is stored here?" />
				</FormField>
				<FormField label="Type" id="org-repo-type" help="Mirrors pull upstream releases automatically">
					<Select type="single" value={String(newType)} onValueChange={(v) => { if (v) newType = Number(v) as ArtifactRepoType; }}>
						<SelectTrigger class="w-full" id="org-repo-type">
							{artifactRepoTypeOptions.find((o) => o.value === newType)?.label ?? 'Select type'}
						</SelectTrigger>
						<SelectContent>
							{#each artifactRepoTypeOptions as o (o.value)}
								<SelectItem value={String(o.value)}>
									<div>
										<div>{o.label}</div>
										<div class="text-xs text-muted-foreground">{o.description}</div>
									</div>
								</SelectItem>
							{/each}
						</SelectContent>
					</Select>
				</FormField>
				<FormField label="Private" horizontal help="Visible only to members and admins">
					<Switch bind:checked={newPrivate} />
				</FormField>
			</div>
		</FormSection>

		{#if isMirror}
			<FormSection title="Mirror Source">
				<MirrorConfigFields form={newMirror} kind={artifactMirrorKind(newType)} idPrefix="org-repo-mirror" />
			</FormSection>
		{/if}
	</div>

	{#snippet footer()}
		<Button variant="outline" onclick={closeCreatePanel}>Cancel</Button>
		<Button onclick={createRepo} disabled={creating || !newName.trim() || (isMirror && !newMirror.upstream.trim())}>
			{creating ? (isMirror ? 'Validating...' : 'Creating...') : 'Create Repository'}
		</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteDialogOpen} title="Delete Repository" confirmLabel="Delete" onConfirm={confirmDelete} loading={deleting}>
	{#snippet description()}
		This permanently deletes <strong>{deleteTarget?.namespace}/{deleteTarget?.name}</strong> and all its artifacts.
	{/snippet}
</ConfirmDialog>
