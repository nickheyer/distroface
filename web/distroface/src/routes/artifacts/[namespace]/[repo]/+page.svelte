<script lang="ts">
	import { onMount } from 'svelte';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { page } from '$app/state';
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
		Collapsible, CollapsibleContent, CollapsibleTrigger
	} from '$lib/components/ui/collapsible';
	import FormPanel from '$lib/components/form-panel.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import MirrorBadge from '$lib/components/mirror-badge.svelte';
	import MirrorConfigFields, {
		emptyMirrorForm, mirrorFormFrom, mirrorInit
	} from '$lib/components/mirror-config-fields.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import {
		Archive, ArrowDown, ArrowLeft, ArrowUp, ArrowUpDown, ChevronDown, Download,
		Lock, Globe, Pencil, Plus, RefreshCw, Search, Settings, Tag, Tags, Trash2, Upload, X
	} from '@lucide/svelte';
	import {
		Select, SelectContent, SelectItem, SelectTrigger
	} from '$lib/components/ui/select';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime, formatBytes, truncateDigest } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { ArtifactRepoType } from '$lib/proto/distroface/v1/types_pb';
	import type { Artifact, ArtifactRepository } from '$lib/proto/distroface/v1/types_pb';
	import type { ArtifactVersionGroup } from '$lib/proto/distroface/v1/artifact_pb';
	import { artifactMirrorKind, artifactMirrorLabel } from '$lib/mirror';

	const SESSION_KEY = 'distroface_session';
	const repoName = $derived(page.params.repo ?? '');
	const namespace = $derived(page.params.namespace ?? '');

	let repo = $state<ArtifactRepository | null>(null);
	let versions = $state<ArtifactVersionGroup[]>([]);
	let loading = $state(true);
	let notFound = $state(false);
	let expandedVersions = $state<Record<string, boolean>>({});
	const versionPager = new Pager(10);

	const versionSortOptions = [
		{ value: 'version desc', label: 'Newest version' },
		{ value: 'version asc', label: 'Oldest version' },
		{ value: 'activity desc', label: 'Recently updated' },
		{ value: 'activity asc', label: 'Least recently updated' }
	];
	let versionSort = $state('version desc');
	let searchSort = $state('created_at desc');

	// Search mode
	const filter = new QueryFilter([
		{ key: 'name', label: 'Name' },
		{ key: 'version', label: 'Version' },
		{ key: 'path', label: 'Path' }
	]);
	let searchResults = $state<Artifact[] | null>(null);
	let searching = $state(false);
	let searchLoaded = $state(false);
	const searchPager = new Pager(20);

	// Upload
	let uploadPanelOpen = $state(false);
	let uploadFile = $state<File | null>(null);
	let uploadVersion = $state('');
	let uploadPath = $state('');
	let uploadProps = $state<{ key: string; value: string }[]>([]);
	let uploading = $state(false);
	let uploadStage = $state('');

	// Repo settings
	let settingsPanelOpen = $state(false);
	let settingsDescription = $state('');
	let settingsPrivate = $state(false);
	let settingsMirror = $state(emptyMirrorForm());
	let savingSettings = $state(false);

	const repoIsMirror = $derived(
		!!repo && repo.type !== ArtifactRepoType.FILE && repo.type !== ArtifactRepoType.UNSPECIFIED
	);

	// Properties editor
	let propsPanelOpen = $state(false);
	let propsTarget = $state<Artifact | null>(null);
	let propsRows = $state<{ key: string; value: string }[]>([]);
	let savingProps = $state(false);

	// Rename
	let renamePanelOpen = $state(false);
	let renameTarget = $state<Artifact | null>(null);
	let renamePath = $state('');
	let renameVersion = $state('');
	let renaming = $state(false);

	// Delete
	let deleteDialogOpen = $state(false);
	let deleteTarget = $state<Artifact | null>(null);
	let deleting = $state(false);

	const canMutate = $derived(
		!!repo &&
			(repo.owner === authStore.user?.username ||
				authStore.hasPermission('artifacts', 'manage') ||
				authStore.hasPermission('organizations', 'update', namespace))
	);

	async function loadRepo() {
		loading = true;
		notFound = false;
		try {
			const resp = await rpcClient.artifact.getArtifactRepository({ name: repoName, namespace });
			repo = resp.repository ?? null;
			await loadVersions();
		} catch {
			notFound = true;
		} finally {
			loading = false;
		}
	}

	async function loadVersions() {
		const resp = await rpcClient.artifact.listArtifactVersions({
			page: versionPager.request(undefined, versionSort),
			repoName,
			namespace
		});
		versions = resp.versions;
		versionPager.apply(resp.page);
		if (versions.length > 0 && Object.keys(expandedVersions).length === 0) {
			expandedVersions = { [versions[0].version]: true };
		}
	}

	function setVersionSort(v: string) {
		if (v === versionSort) return;
		versionSort = v;
		versionPager.reset();
		loadVersions();
	}

	// Release assets read best alphabetically within a version
	function sortedFiles(list: Artifact[]) {
		return [...list].sort((a, b) => a.path.localeCompare(b.path));
	}

	function toggleSearchSort(col: string) {
		searchSort = searchSort === `${col} desc` ? `${col} asc` : `${col} desc`;
		searchPager.reset();
		fetchSearch();
	}

	async function runSearch() {
		searchPager.reset();
		await fetchSearch();
	}

	async function fetchSearch() {
		if (!filter.active) {
			searchResults = null;
			searchPager.reset();
			return;
		}
		searching = true;
		try {
			const resp = await rpcClient.artifact.searchArtifacts({
				page: searchPager.request(filter.request(), searchSort),
				repoName,
				namespace
			});
			searchResults = resp.artifacts;
			searchPager.apply(resp.page);
		} catch {
			searchResults = [];
			searchPager.apply();
		} finally {
			searching = false;
			searchLoaded = true;
		}
	}

	// ── Download ─────────────────────────────────────────────────────────

	async function downloadArtifact(artifact: Artifact) {
		const prefix = `/api/v1/artifacts/_ns/${encodeURIComponent(namespace)}/${encodeURIComponent(repoName)}`;
		const url = `${prefix}/${encodeURIComponent(artifact.version)}/${artifact.path.split('/').map(encodeURIComponent).join('/')}`;
		try {
			const token = localStorage.getItem(SESSION_KEY);
			const resp = await fetch(url, {
				headers: token ? { Authorization: `Bearer ${token}` } : {}
			});
			if (!resp.ok) {
				toast.error(`Download failed (${resp.status})`);
				return;
			}
			const blob = await resp.blob();
			const objectUrl = URL.createObjectURL(blob);
			const a = document.createElement('a');
			a.href = objectUrl;
			a.download = artifact.name;
			a.click();
			URL.revokeObjectURL(objectUrl);
		} catch {
			toast.error('Download failed');
		}
	}

	// ── Upload ───────────────────────────────────────────────────────────

	function onFileSelected(e: Event) {
		const input = e.target as HTMLInputElement;
		uploadFile = input.files?.[0] ?? null;
		if (uploadFile && !uploadPath) uploadPath = uploadFile.name;
	}

	async function doUpload() {
		if (!uploadFile || !uploadVersion.trim()) return;
		uploading = true;
		try {
			uploadStage = 'Initiating...';
			const init = await rpcClient.artifact.initiateArtifactUpload({ repoName, namespace });

			uploadStage = 'Uploading...';
			const token = localStorage.getItem(SESSION_KEY);
			const patchResp = await fetch(init.uploadUrl, {
				method: 'PATCH',
				headers: token ? { Authorization: `Bearer ${token}` } : {},
				body: uploadFile
			});
			if (!patchResp.ok) {
				toast.error(`Upload failed (${patchResp.status})`);
				return;
			}

			uploadStage = 'Finalizing...';
			const properties: Record<string, string> = {};
			for (const row of uploadProps) {
				if (row.key.trim()) properties[row.key.trim()] = row.value;
			}
			await rpcClient.artifact.completeArtifactUpload({
				repoName,
				namespace,
				uploadId: init.uploadId,
				version: uploadVersion.trim(),
				path: uploadPath.trim(),
				properties
			});
			toast.success('Artifact uploaded');
			closeUploadPanel();
			await loadVersions();
		} catch {
			// Error interceptor already toasted
		} finally {
			uploading = false;
			uploadStage = '';
		}
	}

	function closeUploadPanel() {
		uploadPanelOpen = false;
		uploadFile = null;
		uploadVersion = '';
		uploadPath = '';
		uploadProps = [];
	}

	// ── Repo settings ────────────────────────────────────────────────────

	function openSettings() {
		if (!repo) return;
		settingsDescription = repo.description;
		settingsPrivate = repo.isPrivate;
		settingsMirror = mirrorFormFrom(repo.mirror);
		settingsPanelOpen = true;
	}

	async function saveSettings() {
		savingSettings = true;
		try {
			const resp = await rpcClient.artifact.updateArtifactRepository({
				name: repoName,
				namespace,
				description: settingsDescription,
				isPrivate: settingsPrivate,
				mirror: repoIsMirror ? mirrorInit(settingsMirror) : undefined
			});
			repo = resp.repository ?? repo;
			toast.success('Repository updated');
			settingsPanelOpen = false;
		} catch {
			// Error interceptor already toasted
		} finally {
			savingSettings = false;
		}
	}

	// ── Properties ───────────────────────────────────────────────────────

	function openProps(artifact: Artifact) {
		propsTarget = artifact;
		propsRows = Object.entries(artifact.properties).map(([key, value]) => ({ key, value }));
		if (propsRows.length === 0) propsRows = [{ key: '', value: '' }];
		propsPanelOpen = true;
	}

	async function saveProps() {
		if (!propsTarget) return;
		savingProps = true;
		try {
			const properties: Record<string, string> = {};
			for (const row of propsRows) {
				if (row.key.trim()) properties[row.key.trim()] = row.value;
			}
			await rpcClient.artifact.setArtifactProperties({
				repoName,
				namespace,
				id: propsTarget.id,
				properties
			});
			toast.success('Properties updated');
			propsPanelOpen = false;
			await refreshAfterMutation();
		} catch {
			// Error interceptor already toasted
		} finally {
			savingProps = false;
		}
	}

	// ── Rename ───────────────────────────────────────────────────────────

	function openRename(artifact: Artifact) {
		renameTarget = artifact;
		renamePath = artifact.path;
		renameVersion = artifact.version;
		renamePanelOpen = true;
	}

	async function saveRename() {
		if (!renameTarget || !renamePath.trim()) return;
		renaming = true;
		try {
			await rpcClient.artifact.updateArtifact({
				repoName,
				namespace,
				id: renameTarget.id,
				path: renamePath.trim(),
				version: renameVersion.trim() || undefined
			});
			toast.success('Artifact updated');
			renamePanelOpen = false;
			await refreshAfterMutation();
		} catch {
			// Error interceptor already toasted
		} finally {
			renaming = false;
		}
	}

	// ── Delete ───────────────────────────────────────────────────────────

	function openDelete(artifact: Artifact) {
		deleteTarget = artifact;
		deleteDialogOpen = true;
	}

	async function confirmDelete() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await rpcClient.artifact.deleteArtifact({ repoName, namespace, id: deleteTarget.id });
			toast.success('Artifact deleted');
			deleteDialogOpen = false;
			await refreshAfterMutation();
		} catch {
			// Error interceptor already toasted
		} finally {
			deleting = false;
		}
	}

	async function refreshAfterMutation() {
		await loadVersions();
		if (searchResults !== null) await runSearch();
	}

	// ── Sync now ─────────────────────────────────────────────────────────

	let syncing = $state(false);

	async function syncNow() {
		if (syncing) return;
		syncing = true;
		try {
			await rpcClient.artifact.syncArtifactRepository({ name: repoName, namespace });
			toast.success('Sync started');
		} catch {
			// Error interceptor already toasted
		} finally {
			syncing = false;
		}
	}

	// Sync finish events refresh the page live
	let syncSeqSeen = 0;
	$effect(() => {
		const seq = mirrorSyncStore.finishedSeq;
		const ev = mirrorSyncStore.lastFinished;
		if (seq === syncSeqSeen) return;
		syncSeqSeen = seq;
		if (ev?.kind === 'artifact' && repo && ev.repoId === String(repo.id)) {
			loadRepo();
			loadVersions();
		}
	});

	onMount(() => {
		mirrorSyncStore.ensure();
		loadRepo();
	});
</script>

<svelte:head>
	<title>{namespace}/{repoName} - Artifacts - Distroface</title>
</svelte:head>

{#snippet sortHeader(label: string, col: string)}
	<button type="button" class="inline-flex items-center gap-1 hover:text-foreground transition-colors" onclick={() => toggleSearchSort(col)}>
		{label}
		{#if searchSort === `${col} desc`}
			<ArrowDown class="h-3 w-3" />
		{:else if searchSort === `${col} asc`}
			<ArrowUp class="h-3 w-3" />
		{:else}
			<ArrowUpDown class="h-3 w-3 opacity-40" />
		{/if}
	</button>
{/snippet}

{#snippet artifactTable(artifacts: Artifact[], sortable: boolean = false)}
	<Table>
		<TableHeader>
			<TableRow class="bg-muted/30 hover:bg-muted/30">
				<TableHead class="th">
					{#if sortable}{@render sortHeader('Path', 'path')}{:else}Path{/if}
				</TableHead>
				{#if sortable}
					<TableHead class="th">{@render sortHeader('Version', 'version')}</TableHead>
				{/if}
				<TableHead class="th">
					{#if sortable}{@render sortHeader('Size', 'size')}{:else}Size{/if}
				</TableHead>
				<TableHead class="th">Type</TableHead>
				<TableHead class="th">Digest</TableHead>
				<TableHead class="th">
					{#if sortable}{@render sortHeader('Uploaded', 'created_at')}{:else}Uploaded{/if}
				</TableHead>
				<TableHead class="th w-32"></TableHead>
			</TableRow>
		</TableHeader>
		<TableBody>
			{#each artifacts as artifact (artifact.id)}
				<TableRow>
					<TableCell class="py-3 px-3">
						<span class="font-medium font-mono text-[13px]">{artifact.path}</span>
						{#if Object.keys(artifact.properties).length > 0}
							<div class="flex flex-wrap gap-1 mt-1">
								{#each Object.entries(artifact.properties) as [key, value] (key)}
									<Badge variant="secondary" class="text-[10px] font-mono px-1.5 py-0">{key}={value}</Badge>
								{/each}
							</div>
						{/if}
					</TableCell>
					{#if sortable}
						<TableCell class="py-3 px-3">
							<Badge variant="secondary" class="font-mono text-xs font-medium px-2 py-0.5">{artifact.version}</Badge>
						</TableCell>
					{/if}
					<TableCell class="text-sm py-3 px-3 tabular-nums">{formatBytes(Number(artifact.size))}</TableCell>
					<TableCell class="text-muted-foreground text-xs py-3 px-3 font-mono">{artifact.mimeType || '-'}</TableCell>
					<TableCell class="py-3 px-3">
						<div class="flex items-center gap-1">
							<code class="text-xs text-muted-foreground">{truncateDigest(artifact.digest, 8)}</code>
							<CopyButton text={artifact.digest} label="Digest copied!" />
						</div>
					</TableCell>
					<TableCell class="text-muted-foreground text-sm py-3 px-3">
						{artifact.createdAt ? relativeTime(timestampDate(artifact.createdAt)) : '-'}
					</TableCell>
					<TableCell class="text-right py-3 px-3">
						<div class="flex items-center justify-end gap-0.5">
							<Button
								variant="ghost" size="icon" class="h-7 w-7" title="Download"
								onclick={() => downloadArtifact(artifact)}
							>
								<Download class="h-3.5 w-3.5" />
							</Button>
							{#if canMutate}
								<Button
									variant="ghost" size="icon" class="h-7 w-7" title="Edit properties"
									onclick={() => openProps(artifact)}
								>
									<Tags class="h-3.5 w-3.5" />
								</Button>
								<Button
									variant="ghost" size="icon" class="h-7 w-7" title="Rename / move"
									onclick={() => openRename(artifact)}
								>
									<Pencil class="h-3.5 w-3.5" />
								</Button>
								<Button
									variant="ghost" size="icon"
									class="h-7 w-7 text-destructive hover:text-destructive" title="Delete"
									onclick={() => openDelete(artifact)}
								>
									<Trash2 class="h-3.5 w-3.5" />
								</Button>
							{/if}
						</div>
					</TableCell>
				</TableRow>
			{/each}
		</TableBody>
	</Table>
{/snippet}

<div class="space-y-6">
	{#if loading}
		<Skeleton class="h-16 w-full rounded-xl" />
		<div class="space-y-2">
			{#each Array(3)}
				<Skeleton class="h-14 w-full rounded-xl" />
			{/each}
		</div>
	{:else if notFound || !repo}
		<EmptyState icon={Archive} message="Repository not found" description="It may be private or deleted.">
			{#snippet actions()}
				<Button variant="outline" size="sm" onclick={() => goto(resolve('/artifacts'))}>
					<ArrowLeft class="h-4 w-4 mr-1.5" />
					Back to Artifacts
				</Button>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="flex items-center gap-4">
			<Button variant="ghost" size="icon" class="shrink-0" onclick={() => goto(resolve('/artifacts'))}>
				<ArrowLeft class="h-4 w-4" />
			</Button>
			<div class="h-12 w-12 rounded-xl bg-linear-to-br from-primary/15 to-primary/5 flex items-center justify-center shrink-0 border border-primary/10">
				<Archive class="h-6 w-6 text-primary" />
			</div>
			<div class="flex-1 min-w-0">
				<div class="flex items-center gap-2">
					<h1 class="text-2xl font-bold tracking-tight">
						<span class="text-muted-foreground font-normal">{namespace}/</span>{repo.name}
					</h1>
					<Badge variant="outline" class="text-xs gap-1">
						{#if repo.isPrivate}
							<Lock class="h-2.5 w-2.5" />Private
						{:else}
							<Globe class="h-2.5 w-2.5" />Public
						{/if}
					</Badge>
					{#if repoIsMirror}
						<MirrorBadge
							label={artifactMirrorLabel(repo.type)}
							error={repo.mirrorLastError}
							title={repo.mirror?.upstream ?? ''}
							syncing={repo.mirrorSyncing || mirrorSyncStore.syncing('artifact', repo.id)}
						/>
					{/if}
				</div>
				<p class="text-[13px] text-muted-foreground mt-0.5">
					{repo.description || 'No description'}
					<span class="text-muted-foreground/50 mx-1.5">·</span>
					{repo.artifactCount} artifact{Number(repo.artifactCount) === 1 ? '' : 's'}
					<span class="text-muted-foreground/50 mx-1.5">·</span>
					{formatBytes(Number(repo.totalSize))}
					{#if repo.owner}
						<span class="text-muted-foreground/50 mx-1.5">·</span>
						by {repo.owner}
					{/if}
					{#if repoIsMirror && repo.mirror?.upstream}
						<span class="text-muted-foreground/50 mx-1.5">·</span>
						mirrors {repo.mirror.upstream}{repo.mirrorSyncing ||
						mirrorSyncStore.syncing('artifact', repo.id)
							? ', syncing now'
							: repo.mirrorLastSync
								? `, synced ${relativeTime(timestampDate(repo.mirrorLastSync))}`
								: ', first sync queued'}
					{/if}
				</p>
			</div>
			<div class="flex items-center gap-2">
				{#if repoIsMirror && canMutate}
					<Button variant="outline" size="sm" onclick={syncNow} disabled={syncing}>
						<RefreshCw class="h-4 w-4 mr-1.5 {syncing ? 'animate-spin' : ''}" />
						Sync Now
					</Button>
				{/if}
				{#if canMutate}
					<Button variant="outline" size="sm" onclick={openSettings}>
						<Settings class="h-4 w-4 mr-1.5" />
						Settings
					</Button>
				{/if}
				<PermissionGate resource="artifacts" action="push">
					<Button size="sm" onclick={() => (uploadPanelOpen = true)}>
						<Upload class="h-4 w-4 mr-1.5" />
						Upload
					</Button>
				</PermissionGate>
			</div>
		</div>

		<div class="flex items-center gap-3 flex-wrap">
			<div class="max-w-md flex-1 min-w-64">
				<QueryFilterBar {filter} placeholder="Filter artifacts..." onchange={runSearch} />
			</div>
			{#if searchResults === null}
				<Select type="single" value={versionSort} onValueChange={(v) => { if (v) setVersionSort(v); }}>
					<SelectTrigger class="h-9 w-52 text-[13px]" size="sm">
						{versionSortOptions.find((o) => o.value === versionSort)?.label ?? 'Sort'}
					</SelectTrigger>
					<SelectContent>
						{#each versionSortOptions as o (o.value)}
							<SelectItem value={o.value}>{o.label}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			{/if}
		</div>

		{#if searchResults !== null}
			{#if !searchLoaded}
				<Skeleton class="h-24 w-full rounded-xl" />
			{:else if searchResults.length === 0}
				<EmptyState icon={Search} message="No matching artifacts" description="No results match the current filter" />
			{:else}
				<div class="data-table transition-opacity duration-200 {searching ? 'opacity-60' : ''}">
					{@render artifactTable(searchResults, true)}
					<DataPagination attached pager={searchPager} onChange={fetchSearch} />
				</div>
			{/if}
		{:else if versions.length === 0}
			<EmptyState
				icon={Archive}
				message="No artifacts yet"
				description="Upload files through the UI, dfcli, or the REST API."
			>
				{#snippet actions()}
					<PermissionGate resource="artifacts" action="push">
						<Button variant="outline" size="sm" onclick={() => (uploadPanelOpen = true)}>
							<Upload class="h-4 w-4 mr-1.5" />
							Upload Artifact
						</Button>
					</PermissionGate>
				{/snippet}
			</EmptyState>
		{:else}
			<div class="space-y-3">
				{#each versions as group (group.version)}
					<Collapsible
						open={expandedVersions[group.version] ?? false}
						onOpenChange={(v) => (expandedVersions = { ...expandedVersions, [group.version]: v })}
					>
						<div class="rounded-xl border border-border/50 overflow-hidden">
							<CollapsibleTrigger
								class="flex w-full items-center gap-2.5 px-4 py-3 bg-muted/20 hover:bg-muted/40 transition-colors text-left"
							>
								<ChevronDown class="h-4 w-4 text-muted-foreground transition-transform {expandedVersions[group.version] ? '' : '-rotate-90'}" />
								<Tag class="h-3.5 w-3.5 text-primary" />
								<span class="font-medium font-mono text-sm">{group.version}</span>
								<span class="text-xs text-muted-foreground ml-auto tabular-nums">
									{group.artifacts.length} file{group.artifacts.length === 1 ? '' : 's'}
								</span>
							</CollapsibleTrigger>
							<CollapsibleContent>
								{@render artifactTable(sortedFiles(group.artifacts))}
							</CollapsibleContent>
						</div>
					</Collapsible>
				{/each}
			</div>

			<DataPagination pager={versionPager} onChange={loadVersions} pageSizeOptions={[5, 10, 20, 50]} />
		{/if}
	{/if}
</div>

<!-- Upload panel -->
<FormPanel
	open={uploadPanelOpen}
	onOpenChange={(v) => { if (!v) closeUploadPanel(); }}
	title="Upload Artifact"
	description="Re-uploading the same version and path replaces the artifact."
	icon={Upload}
>
	<div class="space-y-6">
		<FormSection title="File">
			<div class="space-y-3">
				<FormField label="File" id="upload-file" required>
					<Input id="upload-file" type="file" onchange={onFileSelected} />
					{#if uploadFile}
						<p class="text-xs text-muted-foreground mt-1">{uploadFile.name} · {formatBytes(uploadFile.size)}</p>
					{/if}
				</FormField>
				<FormField label="Version" id="upload-version" required>
					<Input id="upload-version" bind:value={uploadVersion} placeholder="1.0.0" />
				</FormField>
				<FormField label="Path" id="upload-path" help="Defaults to the file name">
					<Input id="upload-path" bind:value={uploadPath} placeholder="dist/app.zip" />
				</FormField>
			</div>
		</FormSection>

		<FormSection title="Properties">
			<div class="space-y-2">
				{#each uploadProps as row, i (i)}
					<div class="flex items-center gap-2">
						<Input bind:value={row.key} placeholder="key" class="font-mono text-sm" />
						<Input bind:value={row.value} placeholder="value" class="font-mono text-sm" />
						<Button
							variant="ghost" size="icon" class="h-8 w-8 shrink-0"
							onclick={() => (uploadProps = uploadProps.filter((_, j) => j !== i))}
						>
							<X class="h-3.5 w-3.5" />
						</Button>
					</div>
				{/each}
				<Button variant="outline" size="sm" onclick={() => (uploadProps = [...uploadProps, { key: '', value: '' }])}>
					<Plus class="h-3.5 w-3.5 mr-1.5" />
					Add Property
				</Button>
			</div>
		</FormSection>
	</div>

	{#snippet footer()}
		<Button variant="outline" onclick={closeUploadPanel} disabled={uploading}>Cancel</Button>
		<Button onclick={doUpload} disabled={uploading || !uploadFile || !uploadVersion.trim()}>
			{uploading ? uploadStage || 'Uploading...' : 'Upload'}
		</Button>
	{/snippet}
</FormPanel>

<!-- Repo settings panel -->
<FormPanel
	open={settingsPanelOpen}
	onOpenChange={(v) => (settingsPanelOpen = v)}
	title="Repository Settings"
	description="Update the description and visibility of {repoName}."
	icon={Settings}
>
	<div class="space-y-6">
		<FormSection title="General">
			<div class="space-y-3">
				<FormField label="Description" id="settings-description">
					<Input id="settings-description" bind:value={settingsDescription} placeholder="What is stored here?" />
				</FormField>
				<FormField label="Private" horizontal help="Visible only to you and admins">
					<Switch bind:checked={settingsPrivate} />
				</FormField>
			</div>
		</FormSection>

		{#if repoIsMirror && repo}
			<FormSection title="Mirror Source">
				<MirrorConfigFields
					form={settingsMirror}
					kind={artifactMirrorKind(repo.type)}
					tokenSet={repo.mirror?.authTokenSet ?? false}
					lastSync={repo.mirrorLastSync}
					lastError={repo.mirrorLastError}
					nextAttempt={repo.mirrorNextAttempt}
					idPrefix="settings-mirror"
				/>
			</FormSection>
		{/if}
	</div>

	{#snippet footer()}
		<Button variant="outline" onclick={() => (settingsPanelOpen = false)}>Cancel</Button>
		<Button onclick={saveSettings} disabled={savingSettings}>
			{savingSettings ? 'Saving...' : 'Save'}
		</Button>
	{/snippet}
</FormPanel>

<!-- Properties editor panel -->
<FormPanel
	open={propsPanelOpen}
	onOpenChange={(v) => (propsPanelOpen = v)}
	title="Edit Properties"
	description="Saving replaces the full set for {propsTarget?.path ?? ''}."
	icon={Tags}
>
	<div class="space-y-2">
		{#each propsRows as row, i (i)}
			<div class="flex items-center gap-2">
				<Input bind:value={row.key} placeholder="key" class="font-mono text-sm" />
				<Input bind:value={row.value} placeholder="value" class="font-mono text-sm" />
				<Button
					variant="ghost" size="icon" class="h-8 w-8 shrink-0"
					onclick={() => (propsRows = propsRows.filter((_, j) => j !== i))}
				>
					<X class="h-3.5 w-3.5" />
				</Button>
			</div>
		{/each}
		<Button variant="outline" size="sm" onclick={() => (propsRows = [...propsRows, { key: '', value: '' }])}>
			<Plus class="h-3.5 w-3.5 mr-1.5" />
			Add Property
		</Button>
	</div>

	{#snippet footer()}
		<Button variant="outline" onclick={() => (propsPanelOpen = false)}>Cancel</Button>
		<Button onclick={saveProps} disabled={savingProps}>
			{savingProps ? 'Saving...' : 'Save Properties'}
		</Button>
	{/snippet}
</FormPanel>

<!-- Rename panel -->
<FormPanel
	open={renamePanelOpen}
	onOpenChange={(v) => (renamePanelOpen = v)}
	title="Rename / Move Artifact"
	description="Change the path or version of {renameTarget?.path ?? ''}."
	icon={Pencil}
>
	<div class="space-y-3">
		<FormField label="Path" id="rename-path" required>
			<Input id="rename-path" bind:value={renamePath} class="font-mono text-sm" />
		</FormField>
		<FormField label="Version" id="rename-version">
			<Input id="rename-version" bind:value={renameVersion} class="font-mono text-sm" />
		</FormField>
	</div>

	{#snippet footer()}
		<Button variant="outline" onclick={() => (renamePanelOpen = false)}>Cancel</Button>
		<Button onclick={saveRename} disabled={renaming || !renamePath.trim()}>
			{renaming ? 'Saving...' : 'Save'}
		</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={deleteDialogOpen} title="Delete Artifact" confirmLabel="Delete" onConfirm={confirmDelete} loading={deleting}>
	{#snippet description()}
		This permanently deletes <strong>{deleteTarget?.path}</strong> ({deleteTarget?.version}).
	{/snippet}
</ConfirmDialog>
