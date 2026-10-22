<script lang="ts">
	import { onMount } from 'svelte';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Input } from '$lib/components/ui/input';
	import {
		Table,
		TableBody,
		TableCell,
		TableHead,
		TableHeader,
		TableRow
	} from '$lib/components/ui/table';
	import {
		Collapsible,
		CollapsibleContent,
		CollapsibleTrigger
	} from '$lib/components/ui/collapsible';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import MirrorBadge from '$lib/components/mirror-badge.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import {
		Archive,
		ArrowDown,
		ArrowLeft,
		ArrowUp,
		ArrowUpDown,
		ChevronDown,
		Download,
		HardDrive,
		HardDriveDownload,
		Lock,
		Globe,
		Loader2,
		Package,
		Pencil,
		Play,
		Plus,
		RefreshCw,
		Search,
		Settings,
		Square,
		Tag,
		Tags,
		Trash2,
		Upload,
		User,
		X,
		GitBranch,
		Rocket,
		Workflow
	} from '@lucide/svelte';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import * as Tabs from '$lib/components/ui/tabs';
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
	import { artifactMirrorLabel } from '$lib/mirror';

	const SESSION_KEY = 'distroface_session';
	const repoName = $derived(page.params.repo ?? '');
	const namespace = $derived(page.params.namespace ?? '');
	const uploadHref = $derived(
		resolve('/artifacts/[namespace]/[repo]/upload', { namespace, repo: repoName })
	);
	const settingsHref = $derived(
		resolve('/artifacts/[namespace]/[repo]/settings', { namespace, repo: repoName })
	);

	let repo = $state<ArtifactRepository | null>(null);
	// Namespace and name of the git repository this package belongs to
	const sourceRepo = $derived.by(() => {
		const [namespace = '', name = ''] = (repo?.sourceRepository ?? '').split('/');
		return { namespace, name };
	});
	let versions = $state<ArtifactVersionGroup[]>([]);
	let loading = $state(true);
	let notFound = $state(false);
	let expandedVersions = $state<Record<string, boolean>>({});
	const versionPager = new Pager(10);

	// Run uploads list apart from package files
	let workflow = $state(false);
	const showWorkflowTab = $derived(
		!!repo && (!!repo.sourceRepository || Number(repo.workflowArtifactCount) > 0)
	);

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

	const repoIsMirror = $derived(
		!!repo && repo.type !== ArtifactRepoType.FILE && repo.type !== ArtifactRepoType.UNSPECIFIED
	);

	// Editing swaps the artifact row for an inline form
	let propsTarget = $state<Artifact | null>(null);
	let propsRows = $state<{ key: string; value: string }[]>([]);
	let savingProps = $state(false);

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
			namespace,
			workflow
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

	function setView(v: string) {
		const next = v === 'workflow';
		if (next === workflow) return;
		workflow = next;
		expandedVersions = {};
		versionPager.reset();
		loadVersions();
		if (searchResults !== null) runSearch();
	}

	let fileSort = $state('path asc');

	function sortedFiles(list: Artifact[]) {
		const [col, dir] = fileSort.split(' ');
		const mul = dir === 'desc' ? -1 : 1;
		return [...list].sort((a, b) => {
			switch (col) {
				case 'size':
					return mul * (Number(a.size) - Number(b.size));
				case 'created_at': {
					const at = a.createdAt ? timestampDate(a.createdAt).getTime() : 0;
					const bt = b.createdAt ? timestampDate(b.createdAt).getTime() : 0;
					return mul * (at - bt);
				}
				default:
					return mul * a.path.localeCompare(b.path);
			}
		});
	}

	function toggleSearchSort(col: string) {
		searchSort = searchSort === `${col} desc` ? `${col} asc` : `${col} desc`;
		searchPager.reset();
		fetchSearch();
	}

	function toggleFileSort(col: string) {
		fileSort = fileSort === `${col} desc` ? `${col} asc` : `${col} desc`;
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
				namespace,
				workflow
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

	// Browser streams the file itself, a scoped cookie carries auth
	function downloadArtifact(artifact: Artifact) {
		const prefix = `/api/v1/artifacts/_ns/${encodeURIComponent(namespace)}/${encodeURIComponent(repoName)}`;
		const url = `${prefix}/${encodeURIComponent(artifact.version)}/${artifact.path.split('/').map(encodeURIComponent).join('/')}`;
		const token = localStorage.getItem(SESSION_KEY);
		if (token) {
			const secure = location.protocol === 'https:' ? '; Secure' : '';
			document.cookie = `${SESSION_KEY}=${token}; Path=/api/v1/artifacts; Max-Age=60; SameSite=Strict${secure}`;
		}
		const a = document.createElement('a');
		a.href = url;
		a.download = artifact.name;
		a.click();
	}

	// ── Properties ───────────────────────────────────────────────────────

	function closeEdit() {
		propsTarget = null;
		renameTarget = null;
	}

	function openProps(artifact: Artifact) {
		renameTarget = null;
		propsTarget = artifact;
		propsRows = Object.entries(artifact.properties).map(([key, value]) => ({ key, value }));
		if (propsRows.length === 0) propsRows = [{ key: '', value: '' }];
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
			closeEdit();
			await refreshAfterMutation();
		} catch {
			// Error interceptor already toasted
		} finally {
			savingProps = false;
		}
	}

	// ── Rename ───────────────────────────────────────────────────────────

	function openRename(artifact: Artifact) {
		propsTarget = null;
		renameTarget = artifact;
		renamePath = artifact.path;
		renameVersion = artifact.version;
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
			closeEdit();
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
	let stopping = $state(false);
	const syncActive = $derived(
		!!repo && (repo.mirrorSyncing || mirrorSyncStore.syncing('artifact', repo.id))
	);

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

	async function stopSync() {
		if (stopping) return;
		stopping = true;
		try {
			await rpcClient.artifact.stopArtifactRepositorySync({ name: repoName, namespace });
			toast.success('Sync stopped');
		} catch {
			// Error interceptor already toasted
		} finally {
			stopping = false;
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

{#snippet sortHeader(label: string, col: string, current: string, toggle: (col: string) => void)}
	<button
		type="button"
		class="inline-flex items-center gap-1 transition-colors hover:text-foreground"
		onclick={() => toggle(col)}
	>
		{label}
		{#if current === `${col} desc`}
			<ArrowDown class="size-3" />
		{:else if current === `${col} asc`}
			<ArrowUp class="size-3" />
		{:else}
			<ArrowUpDown class="size-3 opacity-40" />
		{/if}
	</button>
{/snippet}

{#snippet propertyRows(
	rows: { key: string; value: string }[],
	onChange: (rows: { key: string; value: string }[]) => void
)}
	<div class="space-y-2">
		{#each rows as row, i (i)}
			<div class="flex items-center gap-2">
				<Input bind:value={row.key} placeholder="key" class="font-mono" aria-label="Property key" />
				<Input
					bind:value={row.value}
					placeholder="value"
					class="font-mono"
					aria-label="Property value"
				/>
				<Button
					variant="ghost-muted"
					size="icon"
					class="shrink-0"
					onclick={() => onChange(rows.filter((_, j) => j !== i))}
					aria-label="Remove property"
				>
					<X />
				</Button>
			</div>
		{/each}
	</div>
	<Button variant="outline" size="sm" onclick={() => onChange([...rows, { key: '', value: '' }])}>
		<Plus />
		Add property
	</Button>
{/snippet}

{#snippet artifactTable(artifacts: Artifact[], sortable: boolean = false)}
	<Table>
		<TableHeader>
			<TableRow>
				<TableHead>
					{#if sortable}{@render sortHeader(
							'Path',
							'path',
							searchSort,
							toggleSearchSort
						)}{:else}{@render sortHeader('Path', 'path', fileSort, toggleFileSort)}{/if}
				</TableHead>
				{#if sortable}
					<TableHead
						>{@render sortHeader('Version', 'version', searchSort, toggleSearchSort)}</TableHead
					>
				{/if}
				<TableHead>
					{#if sortable}{@render sortHeader(
							'Size',
							'size',
							searchSort,
							toggleSearchSort
						)}{:else}{@render sortHeader('Size', 'size', fileSort, toggleFileSort)}{/if}
				</TableHead>
				<TableHead>Type</TableHead>
				<TableHead>Digest</TableHead>
				<TableHead>
					{#if sortable}{@render sortHeader(
							'Uploaded',
							'created_at',
							searchSort,
							toggleSearchSort
						)}{:else}{@render sortHeader('Uploaded', 'created_at', fileSort, toggleFileSort)}{/if}
				</TableHead>
				<TableHead class="w-32"></TableHead>
			</TableRow>
		</TableHeader>
		<TableBody>
			{#each artifacts as artifact (artifact.id)}
				{#if propsTarget?.id === artifact.id}
					<TableRow class="bg-muted/30 hover:[&>td]:bg-transparent">
						<TableCell colspan={sortable ? 7 : 6} class="px-4 py-4 whitespace-normal">
							<div class="form-fields">
								<FormField
									label="Properties of {artifact.path}"
									help="Saving replaces the full set"
								>
									{@render propertyRows(propsRows, (rows) => (propsRows = rows))}
								</FormField>
								<div class="flex flex-wrap items-center gap-2">
									<Button onclick={saveProps} disabled={savingProps}>
										{#if savingProps}<Loader2 class="animate-spin" />{/if}
										{savingProps ? 'Saving' : 'Save properties'}
									</Button>
									<Button variant="ghost-muted" onclick={closeEdit} disabled={savingProps}
										>Cancel</Button
									>
								</div>
							</div>
						</TableCell>
					</TableRow>
				{:else if renameTarget?.id === artifact.id}
					<TableRow class="bg-muted/30 hover:[&>td]:bg-transparent">
						<TableCell colspan={sortable ? 7 : 6} class="px-4 py-4 whitespace-normal">
							<div class="form-fields">
								<FormField label="Path" id="rename-path" required span="half">
									<Input id="rename-path" bind:value={renamePath} class="font-mono" />
								</FormField>
								<FormField label="Version" id="rename-version" span="half">
									<Input id="rename-version" bind:value={renameVersion} class="font-mono" />
								</FormField>
								<div class="flex flex-wrap items-center gap-2">
									<Button onclick={saveRename} disabled={renaming || !renamePath.trim()}>
										{#if renaming}<Loader2 class="animate-spin" />{/if}
										{renaming ? 'Saving' : 'Save changes'}
									</Button>
									<Button variant="ghost-muted" onclick={closeEdit} disabled={renaming}
										>Cancel</Button
									>
								</div>
							</div>
						</TableCell>
					</TableRow>
				{:else}
					<TableRow>
						<TableCell>
							<span class="font-mono text-sm font-medium">{artifact.path}</span>
							{#if Object.keys(artifact.properties).length > 0}
								<div class="mt-1 flex flex-wrap gap-1">
									{#each Object.entries(artifact.properties) as [key, value] (key)}
										<Badge variant="secondary" class="font-mono">{key}={value}</Badge>
									{/each}
								</div>
							{/if}
						</TableCell>
						{#if sortable}
							<TableCell>
								<Badge variant="secondary" class="font-mono">{artifact.version}</Badge>
							</TableCell>
						{/if}
						<TableCell class="tabular-nums">{formatBytes(Number(artifact.size))}</TableCell>
						<TableCell class="font-mono text-xs text-muted-foreground"
							>{artifact.mimeType || '-'}</TableCell
						>
						<TableCell>
							<div class="flex items-center gap-1">
								<code class="font-mono text-xs text-muted-foreground"
									>{truncateDigest(artifact.digest, 8)}</code
								>
								<CopyButton text={artifact.digest} label="Digest copied" />
							</div>
						</TableCell>
						<TableCell class="text-muted-foreground">
							{artifact.createdAt ? relativeTime(timestampDate(artifact.createdAt)) : '-'}
						</TableCell>
						<TableCell class="text-right">
							<div class="flex items-center justify-end gap-0.5">
								<Button
									variant="ghost-muted"
									size="icon-sm"
									title="Download"
									aria-label="Download"
									onclick={() => downloadArtifact(artifact)}
								>
									<Download />
								</Button>
								{#if canMutate}
									<Button
										variant="ghost-muted"
										size="icon-sm"
										title="Edit properties"
										aria-label="Edit properties"
										onclick={() => openProps(artifact)}
									>
										<Tags />
									</Button>
									<Button
										variant="ghost-muted"
										size="icon-sm"
										title="Rename or move"
										aria-label="Rename or move"
										onclick={() => openRename(artifact)}
									>
										<Pencil />
									</Button>
									<Button
										variant="ghost-muted"
										size="icon-sm"
										class="hover:text-destructive"
										title="Delete"
										aria-label="Delete"
										onclick={() => openDelete(artifact)}
									>
										<Trash2 />
									</Button>
								{/if}
							</div>
						</TableCell>
					</TableRow>
				{/if}
			{/each}
		</TableBody>
	</Table>
{/snippet}

<div class="space-y-6">
	{#if loading}
		<div class="flex items-start gap-3 border-b pb-5">
			<Skeleton class="size-8 rounded-md" />
			<Skeleton class="mt-1.5 size-5" />
			<div class="flex-1 space-y-2">
				<Skeleton class="h-6 w-64" />
				<Skeleton class="h-4 w-96" />
				<Skeleton class="h-3.5 w-48" />
			</div>
		</div>
		<div class="box divide-y">
			{#each { length: 3 }, i (i)}
				<div class="flex items-center gap-3 px-4 py-3">
					<Skeleton class="size-4" />
					<Skeleton class="h-4 w-32" />
					<Skeleton class="ml-auto h-3 w-12" />
				</div>
			{/each}
		</div>
	{:else if notFound || !repo}
		<EmptyState
			icon={Archive}
			message="Repository not found"
			description="It may be private or deleted."
		>
			{#snippet actions()}
				<Button variant="outline" href={resolve('/artifacts')}>
					<ArrowLeft />
					Back to artifacts
				</Button>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="flex items-start gap-3 border-b pb-5">
			<Button
				variant="ghost-muted"
				size="icon"
				class="shrink-0"
				href={resolve('/artifacts')}
				aria-label="Back to artifacts"
			>
				<ArrowLeft />
			</Button>
			<Archive class="mt-1.5 size-5 shrink-0 text-muted-foreground" />
			<div class="min-w-0 flex-1">
				<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
					<h1 class="text-xl leading-7 font-semibold tracking-tight">
						<span class="font-normal text-muted-foreground">{namespace}/</span>{repo.name}
					</h1>
					<Badge variant="outline">
						{#if repo.isPrivate}
							<Lock />Private
						{:else}
							<Globe />Public
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
				{#if repo.sourceRepository}
					<p class="mt-0.5 flex items-center gap-1.5 text-sm text-muted-foreground">
						<GitBranch class="size-3.5" />
						Source repository
						<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
						<a href="/{repo.sourceRepository}" class="link font-medium">{repo.sourceRepository}</a>
					</p>
				{/if}
				<p class="mt-0.5 text-sm text-muted-foreground">{repo.description || 'No description'}</p>
				<div
					class="mt-1.5 flex flex-wrap items-center gap-x-3.5 gap-y-1 text-xs text-muted-foreground"
				>
					<span class="flex items-center gap-1 tabular-nums">
						<Archive class="size-3.5" />{repo.artifactCount} artifact{Number(repo.artifactCount) ===
						1
							? ''
							: 's'}
					</span>
					<span class="flex items-center gap-1"
						><HardDrive class="size-3.5" />{formatBytes(Number(repo.totalSize))}</span
					>
					{#if Number(repo.workflowArtifactCount) > 0}
						<span class="flex items-center gap-1 tabular-nums">
							<Workflow class="size-3.5" />{repo.workflowArtifactCount} workflow file{Number(
								repo.workflowArtifactCount
							) === 1
								? ''
								: 's'}, {formatBytes(Number(repo.workflowTotalSize))}
						</span>
					{/if}
					{#if repo.owner}
						<span class="flex items-center gap-1"><User class="size-3.5" />{repo.owner}</span>
					{/if}
					{#if repoIsMirror && repo.mirror?.upstream}
						<span class="flex items-center gap-1">
							<HardDriveDownload class="size-3.5" />
							Mirrors {repo.mirror.upstream}{repo.mirrorSyncing ||
							mirrorSyncStore.syncing('artifact', repo.id)
								? ', syncing now'
								: repo.mirrorLastSync
									? `, synced ${relativeTime(timestampDate(repo.mirrorLastSync))}`
									: ', first sync queued'}
						</span>
					{/if}
				</div>
			</div>
			<div class="flex shrink-0 items-center gap-2">
				{#if repoIsMirror && canMutate}
					{#if syncActive}
						<Button variant="outline" onclick={stopSync} disabled={stopping}>
							<Square />
							Stop sync
						</Button>
					{:else}
						<Button variant="outline" onclick={syncNow} disabled={syncing}>
							<RefreshCw class={syncing ? 'animate-spin' : ''} />
							Sync now
						</Button>
					{/if}
				{/if}
				{#if canMutate}
					<Button variant="outline" href={settingsHref}>
						<Settings />
						Settings
					</Button>
				{/if}
				<PermissionGate resource="artifacts" action="push">
					<Button href={uploadHref}>
						<Upload />
						Upload
					</Button>
				</PermissionGate>
			</div>
		</div>

		<div class="flex flex-wrap items-center gap-2">
			{#if showWorkflowTab}
				<Tabs.Root value={workflow ? 'workflow' : 'artifacts'} onValueChange={setView}>
					<Tabs.List>
						<Tabs.Trigger value="artifacts"><Package />Artifacts</Tabs.Trigger>
						<Tabs.Trigger value="workflow"><Workflow />Workflow runs</Tabs.Trigger>
					</Tabs.List>
				</Tabs.Root>
			{/if}
			<div class="w-full sm:max-w-md sm:flex-1">
				<QueryFilterBar
					{filter}
					placeholder={workflow ? 'Filter workflow files' : 'Filter artifacts'}
					onchange={runSearch}
				/>
			</div>
			{#if searchResults === null}
				<Select
					type="single"
					value={versionSort}
					onValueChange={(v) => {
						if (v) setVersionSort(v);
					}}
				>
					<SelectTrigger class="w-52" aria-label="Sort">
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
				<Skeleton class="h-24 w-full rounded-lg" />
			{:else if searchResults.length === 0}
				<EmptyState
					icon={Search}
					message="No matching artifacts"
					description="No results match the current filter"
				/>
			{:else}
				<div class="data-table transition-opacity duration-200 {searching ? 'opacity-60' : ''}">
					{@render artifactTable(searchResults, true)}
					<DataPagination attached pager={searchPager} onChange={fetchSearch} />
				</div>
			{/if}
		{:else if versions.length === 0 && workflow}
			<EmptyState
				icon={Workflow}
				message="No workflow uploads yet"
				description="Files that workflow runs upload land here, apart from the package contents."
			/>
		{:else if versions.length === 0}
			<EmptyState
				icon={Archive}
				message="No artifacts yet"
				description="Upload files through the UI, dfcli, or the REST API."
			>
				{#snippet actions()}
					<PermissionGate resource="artifacts" action="push">
						<Button variant="outline" href={uploadHref}>
							<Upload />
							Upload artifact
						</Button>
					</PermissionGate>
				{/snippet}
			</EmptyState>
		{:else}
			<div class="space-y-4">
				{#each versions as group (group.version)}
					{@const expanded = expandedVersions[group.version] ?? false}
					<Collapsible
						open={expanded}
						onOpenChange={(v) => (expandedVersions = { ...expandedVersions, [group.version]: v })}
					>
						<div class="box">
							<div class="flex items-center bg-muted/40 {expanded ? 'border-b' : ''}">
								<CollapsibleTrigger
									class="flex min-w-0 flex-1 items-center gap-2.5 px-4 py-2.5 text-left text-sm font-semibold transition-colors hover:bg-muted/60"
								>
									<ChevronDown
										class="size-4 text-muted-foreground transition-transform {expanded
											? ''
											: '-rotate-90'}"
									/>
									<Tag class="size-3.5 text-muted-foreground" />
									<span class="font-mono">{group.version}</span>
									<span class="ml-auto text-xs font-normal text-muted-foreground tabular-nums">
										{group.artifacts.length} file{group.artifacts.length === 1 ? '' : 's'}
									</span>
								</CollapsibleTrigger>
								{#if group.runId && repo.sourceRepository}
									<Badge
										variant="outline"
										href={resolve('/[namespace]/[name]/actions/runs/[id]', {
											...sourceRepo,
											id: group.runId
										})}
										class="mr-4 shrink-0"
									>
										<Play />Run
									</Badge>
								{/if}
								{#if group.hasRelease && repo.sourceRepository}
									<Badge
										href={resolve('/[namespace]/[name]/releases/[tag]', {
											...sourceRepo,
											tag: group.version
										})}
										class="mr-4 shrink-0"
									>
										<Rocket />Release
									</Badge>
								{/if}
							</div>
							<CollapsibleContent>
								{@render artifactTable(sortedFiles(group.artifacts))}
							</CollapsibleContent>
						</div>
					</Collapsible>
				{/each}
			</div>

			<DataPagination
				pager={versionPager}
				onChange={loadVersions}
				pageSizeOptions={[5, 10, 20, 50]}
			/>
		{/if}
	{/if}
</div>

<ConfirmDialog
	bind:open={deleteDialogOpen}
	title="Delete artifact"
	confirmLabel="Delete"
	onConfirm={confirmDelete}
	loading={deleting}
>
	{#snippet description()}
		This permanently deletes <strong>{deleteTarget?.path}</strong> ({deleteTarget?.version}).
	{/snippet}
</ConfirmDialog>
