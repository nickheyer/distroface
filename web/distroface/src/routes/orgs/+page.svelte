<script lang="ts">
	import { onMount } from 'svelte';
	import { resolve } from '$app/paths';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import PageHeader from '$lib/components/page-header.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import { Building, Plus, Users, ShieldCheck, ChevronRight } from '$lib/icons';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
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

	const newHref = resolve('/orgs/new');

	onMount(loadOrgs);
</script>

<PageHeader title="Organizations" subtitle="Shared namespaces for teams" icon={Building}>
	{#snippet actions()}
		<PermissionGate resource="organizations" action="create">
			<Button href={newHref}>
				<Plus />
				New organization
			</Button>
		</PermissionGate>
	{/snippet}
</PageHeader>

<div class="space-y-4">
	<div class="max-w-md">
		<QueryFilterBar {filter} placeholder="Search organizations" onchange={filterChanged} />
	</div>

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 3 }, i (i)}
				<div class="flex items-center gap-3 px-4 py-3">
					<Skeleton class="size-9 rounded-md" />
					<div class="flex-1 space-y-2">
						<Skeleton class="h-4 w-40" />
						<Skeleton class="h-3 w-64" />
					</div>
				</div>
			{/each}
		</div>
	{:else if orgs.length === 0}
		<EmptyState
			icon={Building}
			message={filter.active ? 'No matching organizations' : 'No organizations yet'}
			description={filter.active
				? 'Try a different search.'
				: 'Create an organization to collaborate with your team.'}
		>
			{#snippet actions()}
				<PermissionGate resource="organizations" action="create">
					<Button variant="outline" href={newHref}>
						<Plus />
						New organization
					</Button>
				</PermissionGate>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="box divide-y transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			{#each orgs as org (org.id)}
				<a
					href={resolve('/orgs/[name]', { name: org.name })}
					class="group flex items-center gap-3 px-4 py-3 transition-colors hover:bg-muted/30"
				>
					<div
						class="flex size-9 shrink-0 items-center justify-center rounded-md border bg-muted text-muted-foreground"
					>
						<Building class="size-4" />
					</div>
					<div class="min-w-0 flex-1">
						<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
							<span
								class="truncate text-sm font-semibold underline-offset-2 group-hover:text-primary group-hover:underline"
							>
								{org.displayName || org.name}
							</span>
							{#if org.displayName && org.displayName !== org.name}
								<span class="truncate text-xs text-muted-foreground">{org.name}</span>
							{/if}
							{#if org.currentUserRole !== OrgRole.UNSPECIFIED}
								<Badge variant={org.currentUserRole === OrgRole.OWNER ? 'default' : 'outline'}>
									{orgRoleLabel(org.currentUserRole)}
								</Badge>
							{/if}
						</div>
						{#if org.description}
							<p class="mt-0.5 line-clamp-1 text-sm text-muted-foreground">{org.description}</p>
						{/if}
						<div
							class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground"
						>
							<span class="flex items-center gap-1">
								<Users class="size-3.5" />
								{org.memberCount} member{org.memberCount !== 1 ? 's' : ''}
							</span>
							{#if org.createdAt}
								<span>Created {relativeTime(timestampDate(org.createdAt))}</span>
							{/if}
							{#if authStore.user && org.currentUserRole === OrgRole.UNSPECIFIED}
								<span class="flex items-center gap-1"><ShieldCheck class="size-3.5" />Managed</span>
							{/if}
						</div>
					</div>
					<ChevronRight
						class="size-4 shrink-0 text-muted-foreground/50 transition-colors group-hover:text-muted-foreground"
					/>
				</a>
			{/each}
		</div>
		<DataPagination {pager} onChange={loadOrgs} />
	{/if}
</div>
