<script lang="ts">
	import { onMount } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { GitBranch, Plus } from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { Code, ConnectError } from '@connectrpc/connect';
	import { authStore } from '$lib/stores/auth.svelte';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { toast } from 'svelte-sonner';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { MatchKind } from '$lib/proto/distroface/v1/pagination_pb';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Textarea } from '$lib/components/ui/textarea';
	import {
		Select, SelectContent, SelectItem, SelectTrigger
	} from '$lib/components/ui/select';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import MirrorConfigFields, { emptyMirrorForm, mirrorInit } from '$lib/components/mirror-config-fields.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import PageHeader from '$lib/components/page-header.svelte';
	import GitRepoList from './git-repo-list.svelte';
	import { GitRepoType, Visibility } from '$lib/proto/distroface/v1/types_pb';
	import type { GitRepository } from '$lib/proto/distroface/v1/git_pb';

	let repos = $state<GitRepository[]>([]);
	let loading = $state(true);
	let loaded = $state(false);
	let browseDenied = $state(false);
	const pager = new Pager(20);
	const filter = new QueryFilter([
		{ key: 'name', label: 'Name' },
		{ key: 'namespace', label: 'Namespace' },
		{ key: 'description', label: 'Description' },
		{ key: 'topic', label: 'Topic' }
	]);
	let typeFilter = $state<GitRepoType>(GitRepoType.UNSPECIFIED);
	let orderBy = $state('updated_at desc');

	const typeFilterOptions = [
		{ value: GitRepoType.UNSPECIFIED, label: 'All types' },
		{ value: GitRepoType.STANDARD, label: 'Standard' },
		{ value: GitRepoType.MIRROR, label: 'Mirrors' },
		{ value: GitRepoType.FORK, label: 'Forks' },
		{ value: GitRepoType.TEMPLATE, label: 'Templates' }
	];
	const orderOptions = [
		{ value: 'updated_at desc', label: 'Recently updated' },
		{ value: 'created_at desc', label: 'Newest' },
		{ value: 'stars desc', label: 'Most stars' },
		{ value: 'name asc', label: 'Name' }
	];

	async function loadRepos() {
		loading = true;
		browseDenied = false;
		const req = filter.request();
		// Topic filters ride the dedicated request field
		const topic = req.filters.find((f) => f.field === 'topic')?.value ?? '';
		req.filters = req.filters.filter((f) => f.field !== 'topic');
		try {
			const resp = await rpcClient.git.listGitRepositories(
				{ page: pager.request(req, orderBy), type: typeFilter, topic },
				authStore.isAuthenticated ? undefined : silentCallOptions
			);
			repos = resp.repositories;
			pager.apply(resp.page);
		} catch (err) {
			repos = [];
			pager.apply();
			browseDenied = !authStore.isAuthenticated && err instanceof ConnectError && err.code === Code.PermissionDenied;
		} finally {
			loading = false;
			loaded = true;
		}
	}

	function filterChanged() {
		pager.reset();
		loadRepos();
	}

	const emptyMessage = $derived(
		browseDenied ? 'Sign in to browse git repositories' : filter.active || typeFilter !== GitRepoType.UNSPECIFIED ? 'No git repositories found' : 'No git repositories yet'
	);
	const emptyDescription = $derived(
		browseDenied
			? 'Anonymous browsing is not permitted here'
			: filter.active
				? 'No results match the current filter'
				: authStore.isAuthenticated
					? 'Create one here, then push to the clone url it shows you.'
					: undefined
	);

	// Create panel
	type CreateKind = 'standard' | 'mirror' | 'import' | 'template';
	const createKinds: { value: CreateKind; label: string; description: string }[] = [
		{ value: 'standard', label: 'Standard repository', description: 'Empty or initialized, populated by pushes' },
		{ value: 'mirror', label: 'Mirror', description: 'Keeps refs in sync with an upstream git url' },
		{ value: 'import', label: 'Import from another forge', description: 'Mirror plus issues, pulls, labels, and milestones' },
		{ value: 'template', label: 'From a template', description: 'Copy of a template repository on this instance' }
	];
	const gitignoreTemplates = ['Go', 'Node', 'Python', 'Rust', 'Java', 'Kotlin', 'C', 'C++', 'CSharp', 'Ruby', 'Swift', 'Dart', 'Elixir', 'Haskell', 'Scala', 'Terraform', 'Unity', 'VisualStudio'];
	const licenses = [
		{ id: 'MIT', label: 'MIT License' },
		{ id: 'Apache-2.0', label: 'Apache License 2.0' },
		{ id: 'GPL-3.0', label: 'GNU GPL v3' },
		{ id: 'LGPL-3.0', label: 'GNU LGPL v3' },
		{ id: 'AGPL-3.0', label: 'GNU AGPL v3' },
		{ id: 'BSD-2-Clause', label: 'BSD 2-Clause' },
		{ id: 'BSD-3-Clause', label: 'BSD 3-Clause' },
		{ id: 'MPL-2.0', label: 'Mozilla Public License 2.0' },
		{ id: 'ISC', label: 'ISC License' },
		{ id: 'Unlicense', label: 'The Unlicense' }
	];

	let createPanelOpen = $state(false);
	let createKind = $state<CreateKind>('standard');
	let newNamespace = $state('');
	let newName = $state('');
	let newDescription = $state('');
	let newWebsite = $state('');
	let newTopics = $state('');
	let newPrivate = $state(false);
	let newDefaultBranch = $state('');
	let newAutoInit = $state(false);
	let newGitignore = $state('');
	let newLicense = $state('');
	let newMirror = $state(emptyMirrorForm());
	let newMirrorReleases = $state(false);
	let newMirrorPackages = $state(false);
	let newMirrorImages = $state('');
	let newImportMetadata = $state(true);
	let newTemplate = $state('');
	let newIncludeLabels = $state(true);
	let creating = $state(false);

	const ownNamespace = $derived(authStore.user?.username ?? '');
	const usesMirror = $derived(createKind === 'mirror' || createKind === 'import');
	const nameValid = $derived(/^[a-z0-9][a-z0-9._-]{0,99}$/.test(newName.trim()) && !/\.(git|wiki)$/.test(newName.trim()));
	const canSubmit = $derived(
		nameValid && (!usesMirror || !!newMirror.upstream.trim()) && (createKind !== 'template' || !!newTemplate)
	);

	function parseTopics(raw: string): string[] {
		return [...new Set(raw.split(/[,\s]+/).map((t) => t.trim().toLowerCase()).filter(Boolean))];
	}

	async function createRepo() {
		if (!canSubmit || creating) return;
		creating = true;
		const namespace = newNamespace || ownNamespace;
		const name = newName.trim();
		try {
			let created: GitRepository | undefined;
			if (createKind === 'template') {
				const [templateNamespace, templateName] = newTemplate.split('/');
				const resp = await rpcClient.git.useTemplate({
					templateNamespace,
					templateName,
					namespace,
					name,
					description: newDescription.trim(),
					visibility: newPrivate ? Visibility.PRIVATE : Visibility.PUBLIC,
					includeLabels: newIncludeLabels
				});
				created = resp.repository;
			} else {
				const resp = await rpcClient.git.createGitRepository({
					namespace,
					name,
					description: newDescription.trim(),
					website: newWebsite.trim(),
					topics: parseTopics(newTopics),
					visibility: newPrivate ? Visibility.PRIVATE : Visibility.PUBLIC,
					defaultBranch: newDefaultBranch.trim(),
					type: usesMirror ? GitRepoType.MIRROR : GitRepoType.STANDARD,
					mirror: usesMirror ? mirrorInit(newMirror) : undefined,
					autoInit: !usesMirror && newAutoInit,
					gitignoreTemplate: !usesMirror && newAutoInit ? newGitignore : '',
					license: !usesMirror && newAutoInit ? newLicense : '',
					mirrorReleases: usesMirror && newMirrorReleases,
					mirrorImages: usesMirror && newMirrorPackages ? newMirrorImages.split(/\n+/).map((s) => s.trim()).filter(Boolean) : [],
					importMetadata: createKind === 'import' && newImportMetadata
				});
				created = resp.repository;
			}
			toast.success('Repository created');
			closeCreatePanel();
			if (created) await goto(resolve('/[namespace]/[name]', { namespace: created.namespace, name: created.name }));
			else await loadRepos();
		} catch {
			// Error interceptor already toasted
		} finally {
			creating = false;
		}
	}

	function closeCreatePanel() {
		createPanelOpen = false;
		createKind = 'standard';
		newNamespace = '';
		newName = '';
		newDescription = '';
		newWebsite = '';
		newTopics = '';
		newPrivate = false;
		newDefaultBranch = '';
		newAutoInit = false;
		newGitignore = '';
		newLicense = '';
		newMirror = emptyMirrorForm();
		newMirrorReleases = false;
		newMirrorPackages = false;
		newMirrorImages = '';
		newImportMetadata = true;
		newTemplate = '';
		newIncludeLabels = true;
	}

	function openCreate(namespace = '') {
		newNamespace = namespace;
		createPanelOpen = true;
	}

	onMount(() => {
		const params = page.url.searchParams;
		const topic = params.get('topic');
		if (topic) filter.add('topic', MatchKind.EQUALS, topic);
		loadRepos();
		if (params.get('new') && authStore.isAuthenticated) openCreate(params.get('namespace') ?? '');
		mirrorSyncStore.ensure();
	});

	let syncSeqSeen = 0;
	$effect(() => {
		const seq = mirrorSyncStore.finishedSeq;
		if (seq === syncSeqSeen) return;
		syncSeqSeen = seq;
		const kind = mirrorSyncStore.lastFinished?.kind;
		if (kind === 'git' || kind === 'import') loadRepos();
	});
</script>

<PageHeader
	title="Git"
	subtitle="Source repositories served over https and ssh"
	icon={GitBranch}
>
	{#snippet actions()}
		{#if !loading && pager.totalCount > 0}
			<span class="text-[12px] text-muted-foreground/60 tabular-nums">{pager.totalCount} repositor{pager.totalCount === 1 ? 'y' : 'ies'}</span>
		{/if}
		{#if authStore.isAuthenticated}
			<Button size="sm" onclick={() => openCreate()}>
				<Plus class="h-4 w-4 mr-1.5" />
				New Repository
			</Button>
		{/if}
	{/snippet}
</PageHeader>

<div class="space-y-6">
	<div class="flex flex-wrap items-center gap-2">
		<div class="w-full max-w-md">
			<QueryFilterBar {filter} placeholder="Search git repositories..." onchange={filterChanged} />
		</div>
		<Select type="single" value={String(typeFilter)} onValueChange={(v) => { if (v !== undefined) { typeFilter = Number(v) as GitRepoType; filterChanged(); } }}>
			<SelectTrigger class="h-9 w-36 text-[13px]" aria-label="Type">
				{typeFilterOptions.find((o) => o.value === typeFilter)?.label}
			</SelectTrigger>
			<SelectContent>
				{#each typeFilterOptions as o (o.value)}
					<SelectItem value={String(o.value)}>{o.label}</SelectItem>
				{/each}
			</SelectContent>
		</Select>
		<Select type="single" value={orderBy} onValueChange={(v) => { if (v) { orderBy = v; filterChanged(); } }}>
			<SelectTrigger class="h-9 w-44 text-[13px]" aria-label="Sort">
				{orderOptions.find((o) => o.value === orderBy)?.label}
			</SelectTrigger>
			<SelectContent>
				{#each orderOptions as o (o.value)}
					<SelectItem value={o.value}>{o.label}</SelectItem>
				{/each}
			</SelectContent>
		</Select>
	</div>

	<GitRepoList
		{repos}
		{pager}
		onChange={loadRepos}
		{loading}
		{loaded}
		{emptyMessage}
		{emptyDescription}
	>
		{#snippet emptyActions()}
			{#if !filter.active && !browseDenied && typeFilter === GitRepoType.UNSPECIFIED}
				{#if authStore.isAuthenticated}
					<Button variant="outline" size="sm" onclick={() => openCreate()}>
						<Plus class="h-4 w-4 mr-1.5" />New Repository
					</Button>
				{:else}
					<p class="text-[13px] text-muted-foreground">
						<a href={resolve('/login')} class="text-primary underline-offset-4 hover:underline">Sign in</a> to create repositories
					</p>
				{/if}
			{/if}
		{/snippet}
	</GitRepoList>
</div>

<FormPanel
	open={createPanelOpen}
	onOpenChange={(v) => { if (!v) closeCreatePanel(); }}
	title="New Git Repository"
	description="Create an empty repository, mirror an upstream, or start from a template."
	icon={GitBranch}
	wide
>
	<div class="space-y-6">
		<FormSection title="Repository">
			<div class="space-y-3">
				<FormField label="Type" id="git-repo-kind">
					<Select type="single" value={createKind} onValueChange={(v) => { if (v) createKind = v as CreateKind; }}>
						<SelectTrigger class="w-full" id="git-repo-kind">
							{createKinds.find((k) => k.value === createKind)?.label}
						</SelectTrigger>
						<SelectContent>
							{#each createKinds as k (k.value)}
								<SelectItem value={k.value}>
									<div>
										<div>{k.label}</div>
										<div class="text-xs text-muted-foreground">{k.description}</div>
									</div>
								</SelectItem>
							{/each}
						</SelectContent>
					</Select>
				</FormField>
				<div class="grid gap-3 sm:grid-cols-2">
					<FormField label="Namespace" id="git-repo-namespace" help="Your namespace or an organization you belong to">
						<AsyncSelect
							bind:selected={newNamespace}
							placeholder={ownNamespace}
							searchPlaceholder="Search organizations..."
							fetchPage={async (query, pageToken) => {
								const resp = await rpcClient.organization.listOrganizations({
									page: { query: { text: query, filters: [] }, pageToken, pageSize: 20 }
								});
								const items = resp.organizations.map((o) => ({ value: o.name, label: o.displayName || o.name }));
								if (!pageToken && ownNamespace && ownNamespace.toLowerCase().includes(query.toLowerCase())) {
									items.unshift({ value: ownNamespace, label: `${ownNamespace} (you)` });
								}
								return { items, nextPageToken: resp.page?.nextPageToken ?? '' };
							}}
						/>
					</FormField>
					<FormField label="Name" id="git-repo-name" required help="Lowercase letters, digits, dots, dashes, underscores" error={newName && !nameValid ? 'Invalid repository name' : ''}>
						<Input id="git-repo-name" bind:value={newName} placeholder="my-project" class="font-mono text-sm" />
					</FormField>
				</div>
				<FormField label="Description" id="git-repo-description">
					<Input id="git-repo-description" bind:value={newDescription} placeholder="What lives here?" />
				</FormField>
				{#if createKind !== 'template'}
					<div class="grid gap-3 sm:grid-cols-2">
						<FormField label="Website" id="git-repo-website">
							<Input id="git-repo-website" bind:value={newWebsite} placeholder="https://example.com" />
						</FormField>
						<FormField label="Topics" id="git-repo-topics" help="Comma separated">
							<Input id="git-repo-topics" bind:value={newTopics} placeholder="go, cli, tooling" />
						</FormField>
					</div>
				{/if}
				<FormField label="Private" horizontal help="Visible only to collaborators and admins">
					<Switch bind:checked={newPrivate} />
				</FormField>
			</div>
		</FormSection>

		{#if createKind === 'standard'}
			<FormSection title="Initial contents">
				<div class="space-y-3">
					<FormField label="Default branch" id="git-repo-branch" help="Empty uses the instance default">
						<Input id="git-repo-branch" bind:value={newDefaultBranch} placeholder="main" class="font-mono text-sm" />
					</FormField>
					<FormField label="Initialize with a README" horizontal help="Creates a first commit so you can clone right away">
						<Switch bind:checked={newAutoInit} />
					</FormField>
					{#if newAutoInit}
						<div class="grid gap-3 sm:grid-cols-2">
							<FormField label="Add .gitignore" id="git-repo-gitignore">
								<Select type="single" value={newGitignore} onValueChange={(v) => (newGitignore = v ?? '')}>
									<SelectTrigger class="w-full" id="git-repo-gitignore">{newGitignore || 'None'}</SelectTrigger>
									<SelectContent>
										<SelectItem value="">None</SelectItem>
										{#each gitignoreTemplates as t (t)}
											<SelectItem value={t}>{t}</SelectItem>
										{/each}
									</SelectContent>
								</Select>
							</FormField>
							<FormField label="License" id="git-repo-license">
								<Select type="single" value={newLicense} onValueChange={(v) => (newLicense = v ?? '')}>
									<SelectTrigger class="w-full" id="git-repo-license">{licenses.find((l) => l.id === newLicense)?.label ?? 'None'}</SelectTrigger>
									<SelectContent>
										<SelectItem value="">None</SelectItem>
										{#each licenses as l (l.id)}
											<SelectItem value={l.id}>{l.label}</SelectItem>
										{/each}
									</SelectContent>
								</Select>
							</FormField>
						</div>
					{/if}
				</div>
			</FormSection>
		{/if}

		{#if usesMirror}
			<FormSection title={createKind === 'import' ? 'Import source' : 'Mirror source'}>
				<div class="space-y-3">
					<MirrorConfigFields form={newMirror} kind="git" idPrefix="git-repo-mirror" />
					<FormField label="Also mirror releases" horizontal help="Creates a linked artifact package that mirrors upstream release assets">
						<Switch bind:checked={newMirrorReleases} />
					</FormField>
					<FormField label="Also mirror packages" horizontal help="Creates linked image mirrors for upstream container images">
						<Switch bind:checked={newMirrorPackages} />
					</FormField>
					{#if newMirrorPackages}
						<FormField label="Image references" id="git-repo-mirror-images" help="One upstream image per line, such as ghcr.io/owner/app">
							<Textarea id="git-repo-mirror-images" bind:value={newMirrorImages} rows={3} class="font-mono text-sm" placeholder="ghcr.io/owner/app" />
						</FormField>
					{/if}
					{#if createKind === 'import'}
						<FormField label="Import metadata" horizontal help="Issues, pull requests, labels, and milestones from the upstream forge">
							<Switch bind:checked={newImportMetadata} />
						</FormField>
					{/if}
				</div>
			</FormSection>
		{/if}

		{#if createKind === 'template'}
			<FormSection title="Template">
				<div class="space-y-3">
					<FormField label="Template repository" id="git-repo-template" required>
						<AsyncSelect
							bind:selected={newTemplate}
							placeholder="Choose a template..."
							searchPlaceholder="Search templates..."
							fetchPage={async (query, pageToken) => {
								const resp = await rpcClient.git.listGitRepositories({
									page: { query: { text: query, filters: [] }, pageToken, pageSize: 20 },
									type: GitRepoType.TEMPLATE
								});
								return {
									items: resp.repositories.map((r) => ({ value: `${r.namespace}/${r.name}`, label: `${r.namespace}/${r.name}`, description: r.description })),
									nextPageToken: resp.page?.nextPageToken ?? ''
								};
							}}
						/>
					</FormField>
					<FormField label="Include labels" horizontal help="Copy the template's issue labels">
						<Switch bind:checked={newIncludeLabels} />
					</FormField>
				</div>
			</FormSection>
		{/if}
	</div>

	{#snippet footer()}
		<Button variant="outline" onclick={closeCreatePanel}>Cancel</Button>
		<Button onclick={createRepo} disabled={creating || !canSubmit}>
			{creating ? (usesMirror ? 'Validating...' : 'Creating...') : 'Create Repository'}
		</Button>
	{/snippet}
</FormPanel>
