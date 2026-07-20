<script lang="ts">
	import { onMount } from 'svelte';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { Package, Plus } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import { toast } from 'svelte-sonner';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import {
		Select, SelectContent, SelectItem, SelectTrigger
	} from '$lib/components/ui/select';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import MirrorConfigFields, {
		emptyMirrorForm, mirrorInit
	} from '$lib/components/mirror-config-fields.svelte';
	import RepoList from '$lib/components/repo-list.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import PageHeader from '$lib/components/page-header.svelte';
	import { RepositoryType, Visibility } from '$lib/proto/distroface/v1/types_pb';
	import type { Repository } from '$lib/proto/distroface/v1/types_pb';
  import { resolve } from '$app/paths';

	let repos = $state<Repository[]>([]);
	let repoLoading = $state(true);
	let repoLoaded = $state(false);
	const repoPager = new Pager(20);
	const filter = new QueryFilter([
		{ key: 'name', label: 'Name' },
		{ key: 'namespace', label: 'Namespace' },
		{ key: 'description', label: 'Description' }
	]);

	const repoTypeOptions = [
		{
			value: RepositoryType.STANDARD,
			label: 'Standard repository',
			description: 'Images pushed by you or CI'
		},
		{
			value: RepositoryType.MIRROR,
			label: 'Pull-through mirror',
			description: 'Watches an upstream OCI repository and mirrors its tags'
		}
	];

	let createPanelOpen = $state(false);
	let newName = $state('');
	let newNamespace = $state('');
	let newDescription = $state('');
	let newPrivate = $state(false);
	let newType = $state<RepositoryType>(RepositoryType.STANDARD);
	let newMirror = $state(emptyMirrorForm());
	let creating = $state(false);
	const isMirror = $derived(newType === RepositoryType.MIRROR);
	const ownNamespace = $derived(authStore.user?.username ?? '');

	async function createRepo() {
		if (!newName.trim() || (isMirror && !newMirror.upstream.trim())) return;
		creating = true;
		try {
			await rpcClient.repository.createRepository({
				namespace: newNamespace || ownNamespace,
				name: newName.trim(),
				description: newDescription.trim(),
				visibility: newPrivate ? Visibility.PRIVATE : Visibility.PUBLIC,
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
		newNamespace = '';
		newDescription = '';
		newPrivate = false;
		newType = RepositoryType.STANDARD;
		newMirror = emptyMirrorForm();
	}

	async function loadRepos() {
		repoLoading = true;
		try {
			const response = await rpcClient.repository.listRepositories({
				page: repoPager.request(filter.request())
			});
			repos = response.repositories;
			repoPager.apply(response.page);
		} catch {
			repos = [];
			repoPager.apply();
		} finally {
			repoLoading = false;
			repoLoaded = true;
		}
	}

	function filterChanged() {
		repoPager.reset();
		loadRepos();
	}

	const emptyMessage = $derived(
		filter.active ? 'No image repositories found' : 'No image repositories yet'
	);
	const emptyDescription = $derived(
		filter.active ? 'No results match the current filter' : undefined
	);

	onMount(loadRepos);

	// Finished syncs refresh the list live
	let syncSeqSeen = 0;
	$effect(() => {
		const seq = mirrorSyncStore.finishedSeq;
		if (seq === syncSeqSeen) return;
		syncSeqSeen = seq;
		if (mirrorSyncStore.lastFinished?.kind === 'image') loadRepos();
	});
</script>

<PageHeader
	title="Image Repositories"
	subtitle={authStore.isAuthenticated
		? `Welcome back, ${authStore.user?.displayName || authStore.user?.username}`
		: 'Browse container images'}
	icon={Package}
>
	{#snippet actions()}
		{#if !repoLoading && repoPager.totalCount > 0}
			<span class="text-[12px] text-muted-foreground/60 tabular-nums">{repoPager.totalCount} repositor{repoPager.totalCount === 1 ? 'y' : 'ies'}</span>
		{/if}
		{#if authStore.isAuthenticated}
			<Button size="sm" onclick={() => (createPanelOpen = true)}>
				<Plus class="h-4 w-4 mr-1.5" />
				New Repository
			</Button>
		{/if}
	{/snippet}
</PageHeader>

<div class="space-y-6">
	<div class="max-w-md">
		<QueryFilterBar {filter} placeholder="Search repositories..." onchange={filterChanged} />
	</div>

	<RepoList
		{repos}
		pager={repoPager}
		onChange={loadRepos}
		loading={repoLoading}
		loaded={repoLoaded}
		{emptyMessage}
		{emptyDescription}
	>
		{#snippet emptyActions()}
			{#if !filter.active}
				{#if authStore.isAuthenticated}
					<div class="text-center space-y-3">
						<Button variant="outline" size="sm" onclick={() => (createPanelOpen = true)}>
							<Plus class="h-4 w-4 mr-1.5" />New Image Repository
						</Button>
						<p class="text-[13px] text-muted-foreground">Or push your first image:</p>
						<code class="code-inline block text-xs">
							docker push {portalStore.host(
								configStore.publicHostname
							)}/{portalStore.imageRef(
								portalStore.isPortal ? portalStore.orgName : (authStore.user?.username ?? ''),
								'myimage'
							)}:latest
						</code>
					</div>
				{:else}
					<p class="text-[13px] text-muted-foreground">
						<a href={resolve("/login")} class="text-primary underline-offset-4 hover:underline">Sign in</a> to push images
					</p>
				{/if}
			{/if}
		{/snippet}
	</RepoList>
</div>

<FormPanel
	open={createPanelOpen}
	onOpenChange={(v) => { if (!v) closeCreatePanel(); }}
	title="New Image Repository"
	description="Create ahead of a push, or mirror an upstream image."
	icon={Package}
>
	<div class="space-y-6">
		<FormSection title="Repository Details">
			<div class="space-y-3">
				<FormField label="Namespace" id="image-repo-namespace" help="Your namespace or an organization you administer">
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
				<FormField label="Name" id="image-repo-name" required help="Lowercase letters, digits, dots, dashes, underscores">
					<Input id="image-repo-name" bind:value={newName} placeholder="base-images" />
				</FormField>
				<FormField label="Description" id="image-repo-description">
					<Input id="image-repo-description" bind:value={newDescription} placeholder="What is stored here?" />
				</FormField>
				<FormField label="Type" id="image-repo-type" help="Mirrors pull new upstream tags automatically">
					<Select type="single" value={String(newType)} onValueChange={(v) => { if (v) newType = Number(v) as RepositoryType; }}>
						<SelectTrigger class="w-full" id="image-repo-type">
							{repoTypeOptions.find((o) => o.value === newType)?.label ?? 'Select type'}
						</SelectTrigger>
						<SelectContent>
							{#each repoTypeOptions as o (o.value)}
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
				<FormField label="Private" horizontal help="Visible only to you and admins">
					<Switch bind:checked={newPrivate} />
				</FormField>
			</div>
		</FormSection>

		{#if isMirror}
			<FormSection title="Mirror Source">
				<MirrorConfigFields form={newMirror} kind="oci" idPrefix="image-repo-mirror" />
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
