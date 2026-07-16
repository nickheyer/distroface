<script lang="ts">
	import { onMount } from 'svelte';
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
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import {
		Archive, ArrowLeft, ChevronDown, Download, Lock, Globe, Pencil,
		Plus, Search, Settings, Tag, Tags, Trash2, Upload, X
	} from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime, formatBytes, truncateDigest } from '$lib/utils';
	import type { Artifact, ArtifactRepository } from '$lib/proto/distroface/v1/types_pb';
	import type { ArtifactVersionGroup } from '$lib/proto/distroface/v1/artifact_pb';

	const SESSION_KEY = 'distroface_session';
	const repoName = $derived(page.params.repo ?? '');
	const namespace = $derived(page.params.namespace ?? '');

	let repo = $state<ArtifactRepository | null>(null);
	let versions = $state<ArtifactVersionGroup[]>([]);
	let loading = $state(true);
	let notFound = $state(false);
	let expandedVersions = $state<Record<string, boolean>>({});

	// Search mode
	let filterQuery = $state('');
	let searchResults = $state<Artifact[] | null>(null);
	let searching = $state(false);

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
	let savingSettings = $state(false);

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
		const resp = await rpcClient.artifact.listArtifactVersions({ repoName, namespace });
		versions = resp.versions;
		if (versions.length > 0 && Object.keys(expandedVersions).length === 0) {
			expandedVersions = { [versions[0].version]: true };
		}
	}

	let searchTimer: ReturnType<typeof setTimeout>;
	function handleFilterInput() {
		clearTimeout(searchTimer);
		searchTimer = setTimeout(runSearch, 250);
	}

	async function runSearch() {
		const q = filterQuery.trim();
		if (!q) {
			searchResults = null;
			return;
		}
		searching = true;
		try {
			const resp = await rpcClient.artifact.searchArtifacts({
				repoName,
				namespace,
				name: q,
				pageSize: 100
			});
			searchResults = resp.artifacts;
		} catch {
			searchResults = [];
		} finally {
			searching = false;
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
		settingsPanelOpen = true;
	}

	async function saveSettings() {
		savingSettings = true;
		try {
			const resp = await rpcClient.artifact.updateArtifactRepository({
				name: repoName,
				namespace,
				description: settingsDescription,
				isPrivate: settingsPrivate
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

	onMount(loadRepo);
</script>

<svelte:head>
	<title>{namespace}/{repoName} - Artifacts - Distroface</title>
</svelte:head>

{#snippet artifactTable(artifacts: Artifact[])}
	<Table>
		<TableHeader>
			<TableRow class="bg-muted/30 hover:bg-muted/30">
				<TableHead class="th">Path</TableHead>
				<TableHead class="th">Size</TableHead>
				<TableHead class="th">Type</TableHead>
				<TableHead class="th">Digest</TableHead>
				<TableHead class="th">Uploaded</TableHead>
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
				</p>
			</div>
			<div class="flex items-center gap-2">
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

		<div class="relative max-w-sm">
			<Search class="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
			<Input bind:value={filterQuery} placeholder="Filter artifacts by name..." class="pl-9" oninput={handleFilterInput} />
		</div>

		{#if searchResults !== null}
			{#if searching}
				<Skeleton class="h-24 w-full rounded-xl" />
			{:else if searchResults.length === 0}
				<EmptyState icon={Search} message="No matching artifacts" description={`No results for "${filterQuery}"`} />
			{:else}
				<div class="data-table">
					{@render artifactTable(searchResults)}
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
								{@render artifactTable(group.artifacts)}
							</CollapsibleContent>
						</div>
					</Collapsible>
				{/each}
			</div>
		{/if}
	{/if}
</div>

<!-- Upload panel -->
<FormPanel
	open={uploadPanelOpen}
	onOpenChange={(v) => { if (!v) closeUploadPanel(); }}
	title="Upload Artifact"
	description="Upload a file into {repoName}. Re-uploading the same version and path replaces the existing artifact."
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
				<FormField label="Version" id="upload-version" required help="e.g. 1.0.0 or a build number.">
					<Input id="upload-version" bind:value={uploadVersion} placeholder="1.0.0" />
				</FormField>
				<FormField label="Path" id="upload-path" help="Relative path within the version. Defaults to the file name.">
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
				<FormField label="Private" help="Private repositories are only visible to you and admins.">
					<Switch bind:checked={settingsPrivate} />
				</FormField>
			</div>
		</FormSection>
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
	description="Key/value properties for {propsTarget?.path ?? ''}. Saving replaces the full set."
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
		Are you sure you want to delete <strong>{deleteTarget?.path}</strong> ({deleteTarget?.version})?
		This cannot be undone.
	{/snippet}
</ConfirmDialog>
