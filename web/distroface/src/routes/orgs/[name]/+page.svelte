<script lang="ts">
	import { page } from '$app/state';
	import { onMount, getContext } from 'svelte';
	import { Package, Plus } from '@lucide/svelte';
	import RepoList from '$lib/components/repo-list.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import MirrorConfigFields, {
		emptyMirrorForm, mirrorInit
	} from '$lib/components/mirror-config-fields.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import {
		Select, SelectContent, SelectItem, SelectTrigger
	} from '$lib/components/ui/select';
	import { rpcClient } from '$lib/api/rpc-client';
	import { configStore } from '$lib/stores/config.svelte';
	import { portalStore } from '$lib/stores/portal.svelte';
	import { toast } from 'svelte-sonner';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';
	import { RepositoryType, Visibility } from '$lib/proto/distroface/v1/types_pb';
	import type { Repository } from '$lib/proto/distroface/v1/types_pb';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');

	let repos = $state<Repository[]>([]);
	const pager = new Pager(20);
	const filter = new QueryFilter([
		{ key: 'name', label: 'Name' },
		{ key: 'description', label: 'Description' }
	]);
	let loading = $state(true);
	let loaded = $state(false);

	async function loadRepos() {
		loading = true;
		try {
			const resp = await rpcClient.repository.listRepositories({
				namespace: orgName,
				page: pager.request(filter.request())
			});
			repos = resp.repositories;
			pager.apply(resp.page);
		} catch {
			repos = [];
		} finally {
			loading = false;
			loaded = true;
		}
	}

	function filterChanged() {
		pager.reset();
		loadRepos();
	}

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
	let newDescription = $state('');
	let newPrivate = $state(false);
	let newType = $state<RepositoryType>(RepositoryType.STANDARD);
	let newMirror = $state(emptyMirrorForm());
	let creating = $state(false);
	const isMirror = $derived(newType === RepositoryType.MIRROR);

	async function createRepo() {
		if (!newName.trim() || (isMirror && !newMirror.upstream.trim())) return;
		creating = true;
		try {
			await rpcClient.repository.createRepository({
				namespace: orgName,
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
		newDescription = '';
		newPrivate = false;
		newType = RepositoryType.STANDARD;
		newMirror = emptyMirrorForm();
	}

	onMount(loadRepos);
</script>

<div class="space-y-4">
	<div class="section-header">
		<h2 class="section-title">Image Repositories</h2>
		{#if ctx.canAdmin}
			<Button size="sm" onclick={() => (createPanelOpen = true)}>
				<Plus class="h-4 w-4 mr-1.5" />New Repository
			</Button>
		{/if}
	</div>

	<div class="max-w-md">
		<QueryFilterBar {filter} placeholder="Search repositories..." onchange={filterChanged} />
	</div>

	<RepoList
		{repos}
		{pager}
		onChange={loadRepos}
		{loading}
		{loaded}
		emptyMessage={filter.active ? 'No image repositories found' : 'No image repositories yet'}
		emptyDescription={filter.active ? 'No results match the current filter' : undefined}
	>
		{#snippet emptyActions()}
			{#if !filter.active}
				<div class="text-center space-y-3">
					{#if ctx.canAdmin}
						<Button variant="outline" size="sm" onclick={() => (createPanelOpen = true)}>
							<Plus class="h-4 w-4 mr-1.5" />New Image Repository
						</Button>
					{/if}
					<p class="text-[13px] text-muted-foreground">Or push your first image:</p>
					<code class="code-inline block text-xs">
						docker push {portalStore.host(configStore.publicHostname)}/{portalStore.imageRef(
							orgName,
							'myimage'
						)}:latest
					</code>
				</div>
			{/if}
		{/snippet}
	</RepoList>
</div>

<FormPanel
	open={createPanelOpen}
	onOpenChange={(v) => { if (!v) closeCreatePanel(); }}
	title="New Image Repository"
	description="Create under {orgName} ahead of a push, or mirror an upstream image."
	icon={Package}
>
	<div class="space-y-6">
		<FormSection title="Repository Details">
			<div class="space-y-3">
				<FormField label="Name" id="org-image-repo-name" required help="Lowercase letters, digits, dots, dashes, underscores">
					<Input id="org-image-repo-name" bind:value={newName} placeholder="base-images" />
				</FormField>
				<FormField label="Description" id="org-image-repo-description">
					<Input id="org-image-repo-description" bind:value={newDescription} placeholder="What is stored here?" />
				</FormField>
				<FormField label="Type" id="org-image-repo-type" help="Mirrors pull new upstream tags automatically">
					<Select type="single" value={String(newType)} onValueChange={(v) => { if (v) newType = Number(v) as RepositoryType; }}>
						<SelectTrigger class="w-full" id="org-image-repo-type">
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
				<FormField label="Private" horizontal help="Visible only to members and admins">
					<Switch bind:checked={newPrivate} />
				</FormField>
			</div>
		</FormSection>

		{#if isMirror}
			<FormSection title="Mirror Source">
				<MirrorConfigFields form={newMirror} kind="oci" idPrefix="org-image-repo-mirror" />
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
