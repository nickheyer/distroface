<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { Card, CardContent, CardHeader, CardTitle } from '$lib/components/ui/card';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import PageHeader from '$lib/components/page-header.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import { Building2, Plus, Users, ShieldCheck } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime, orgRoleLabel } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { OrgRole } from '$lib/proto/distroface/v1/types_pb';
	import type { Organization } from '$lib/proto/distroface/v1/types_pb';

	let orgs = $state<Organization[]>([]);
	let loading = $state(true);
	let loaded = $state(false);
	const pager = new Pager(20);
	const filter = new QueryFilter([
		{ key: 'name', label: 'Name' },
		{ key: 'display_name', label: 'Display Name' },
		{ key: 'description', label: 'Description' }
	]);

	let createPanelOpen = $state(false);
	let orgName = $state('');
	let orgDisplayName = $state('');
	let orgDescription = $state('');
	let creating = $state(false);

	async function loadOrgs() {
		loading = true;
		try {
			const resp = await rpcClient.organization.listOrganizations({
				page: pager.request(filter.request())
			});
			orgs = resp.organizations;
			pager.apply(resp.page);
		} catch {
			// error interceptor
		} finally {
			loading = false;
			loaded = true;
		}
	}

	function filterChanged() {
		pager.reset();
		loadOrgs();
	}

	function resetForm() {
		orgName = '';
		orgDisplayName = '';
		orgDescription = '';
	}

	async function createOrg() {
		if (!orgName.trim()) return;
		creating = true;
		try {
			await rpcClient.organization.createOrganization({
				name: orgName.trim().toLowerCase(),
				displayName: orgDisplayName.trim(),
				description: orgDescription.trim()
			});
			toast.success('Organization created');
			createPanelOpen = false;
			resetForm();
			await loadOrgs();
		} catch {
			// error interceptor
		} finally {
			creating = false;
		}
	}

	onMount(loadOrgs);
</script>

<PageHeader title="Organizations" subtitle="Manage your team namespaces" icon={Building2}>
	{#snippet actions()}
		<PermissionGate resource="organizations" action="create">
			<Button size="sm" onclick={() => (createPanelOpen = true)}>
				<Plus class="h-4 w-4 mr-1.5" />
				New Organization
			</Button>
		</PermissionGate>
	{/snippet}
</PageHeader>

<div class="max-w-md mb-4">
	<QueryFilterBar {filter} placeholder="Search organizations..." onchange={filterChanged} />
</div>

{#if !loaded}
	<div class="grid gap-4 md:grid-cols-2 lg:grid-cols-3">
		{#each { length: 3 }, i (i)}
			<Skeleton class="h-36 rounded-xl" />
		{/each}
	</div>
{:else if orgs.length === 0}
	<EmptyState
		icon={Building2}
		message={filter.active ? 'No matching organizations' : 'No organizations yet'}
		description={filter.active
			? 'Try a different search.'
			: 'Create an organization to collaborate with your team.'}
	>
		{#snippet actions()}
			<PermissionGate resource="organizations" action="create">
				<Button variant="outline" onclick={() => (createPanelOpen = true)}>
					<Plus class="h-4 w-4 mr-1.5" />
					Create Organization
				</Button>
			</PermissionGate>
		{/snippet}
	</EmptyState>
{:else}
	<div class="grid gap-4 md:grid-cols-2 lg:grid-cols-3 transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
		{#each orgs as org (org.id)}
			<a href={resolve('/orgs/[name]', { name: org.name })} class="block group">
				<Card class="border-border/60 hover:border-primary/20 transition-all hover:shadow-sm h-full">
					<CardHeader class="pb-2">
						<div class="flex items-center gap-3">
							<div class="h-10 w-10 rounded-lg bg-primary/10 flex items-center justify-center">
								<Building2 class="h-5 w-5 text-primary" />
							</div>
							<div class="min-w-0 flex-1">
								<CardTitle class="text-base truncate group-hover:text-primary transition-colors">
									{org.displayName || org.name}
								</CardTitle>
								{#if org.displayName && org.displayName !== org.name}
									<p class="text-xs text-muted-foreground truncate">{org.name}</p>
								{/if}
							</div>
							{#if org.currentUserRole !== OrgRole.UNSPECIFIED}
								<Badge
									variant={org.currentUserRole === OrgRole.OWNER ? 'default' : 'outline'}
									class="text-xs shrink-0"
								>
									{orgRoleLabel(org.currentUserRole)}
								</Badge>
							{/if}
						</div>
					</CardHeader>
					<CardContent>
						{#if org.description}
							<p class="text-[13px] text-muted-foreground line-clamp-2 mb-3">{org.description}</p>
						{/if}
						<div class="flex items-center gap-3 text-xs text-muted-foreground">
							<span class="flex items-center gap-1">
								<Users class="h-3.5 w-3.5" />
								{org.memberCount} member{org.memberCount !== 1 ? 's' : ''}
							</span>
							{#if org.createdAt}
								<span>Created {relativeTime(timestampDate(org.createdAt))}</span>
							{/if}
							{#if authStore.user && org.currentUserRole === OrgRole.UNSPECIFIED}
								<span class="flex items-center gap-1 text-muted-foreground/60">
									<ShieldCheck class="h-3 w-3" />Managed
								</span>
							{/if}
						</div>
					</CardContent>
				</Card>
			</a>
		{/each}
	</div>
	<DataPagination
		page={pager.page}
		pageSize={pager.pageSize}
		totalCount={pager.totalCount}
		onPrev={() => { if (pager.prev()) loadOrgs(); }}
		onNext={() => { if (pager.next()) loadOrgs(); }}
	/>
{/if}

<!-- Create Org Panel -->
<FormPanel
	bind:open={createPanelOpen}
	title="Create Organization"
	description="Organizations are team namespaces for shared repositories. The name becomes the Docker namespace for pushing images."
	icon={Building2}
>
	<div class="space-y-6">
		<FormSection title="Identity">
			<div class="space-y-3">
				<FormField
					label="Name"
					id="org-name"
					help="Lowercase letters, numbers, hyphens. Used as the Docker namespace (e.g., docker push host/name/image)."
					required
				>
					<Input id="org-name" bind:value={orgName} placeholder="my-team" />
				</FormField>

				<FormField label="Display Name" id="org-display" help="A human-readable name shown in the UI.">
					<Input id="org-display" bind:value={orgDisplayName} placeholder="My Team" />
				</FormField>
			</div>
		</FormSection>

		<FormSection title="About">
			<FormField label="Description" id="org-desc" help="A brief description of what this organization does.">
				<Textarea id="org-desc" bind:value={orgDescription} placeholder="What does this organization do?" rows={3} />
			</FormField>
		</FormSection>
	</div>

	{#snippet footer()}
		<Button variant="outline" onclick={() => (createPanelOpen = false)}>Cancel</Button>
		<Button onclick={createOrg} disabled={creating || !orgName.trim()}>
			{creating ? 'Creating...' : 'Create Organization'}
		</Button>
	{/snippet}
</FormPanel>
