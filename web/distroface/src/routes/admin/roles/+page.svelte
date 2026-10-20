<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import { Key, Plus } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import type { Permission, Role } from '$lib/proto/distroface/v1/types_pb';

	let roles = $state<Role[]>([]);
	let permissionMatrix = $state<Record<string, Permission[]>>({});
	let loading = $state(true);
	let loaded = $state(false);
	const pager = new Pager(20);
	const filter = new QueryFilter([
		{ key: 'name', label: 'Name' },
		{ key: 'description', label: 'Description' }
	]);
	const newHref = resolve('/admin/roles/new');

	let deleteDialogOpen = $state(false);
	let deleteTarget = $state<Role | null>(null);
	let deleting = $state(false);

	function hasFullAccess(roleId: string): boolean {
		const perms = permissionMatrix[roleId] || [];
		return perms.some((p) => p.resource === '*' && p.action === '*');
	}

	function getPermCount(roleId: string): number {
		return (permissionMatrix[roleId] || []).length;
	}

	async function loadRoles() {
		loading = true;
		try {
			const [rolesResp, matrixResp] = await Promise.all([
				rpcClient.role.listRoles({ page: pager.request(filter.request()) }),
				rpcClient.role.getPermissionMatrix({})
			]);
			roles = rolesResp.roles;
			pager.apply(rolesResp.page);

			const matrix: Record<string, Permission[]> = {};
			for (const [roleId, rolePerms] of Object.entries(matrixResp.rolePermissions)) {
				matrix[roleId] = rolePerms.permissions;
			}
			permissionMatrix = matrix;
		} catch {
			// error interceptor
		} finally {
			loading = false;
			loaded = true;
		}
	}

	function filterChanged() {
		pager.reset();
		loadRoles();
	}

	function openDelete(role: Role) {
		deleteTarget = role;
		deleteDialogOpen = true;
	}

	async function confirmDelete() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await rpcClient.role.deleteRole({ id: deleteTarget.id });
			deleteDialogOpen = false;
			await loadRoles();
		} catch {
			// error interceptor
		} finally {
			deleting = false;
		}
	}

	onMount(() => {
		if (!authStore.hasPermission('roles', 'read')) {
			goto(resolve('/admin'));
			return;
		}
		loadRoles();
	});
</script>

<div class="space-y-4">
	<div class="section-header flex-wrap">
		<div class="min-w-0">
			<h2 class="section-title">Roles & permissions</h2>
			<p class="section-subtitle">Manage roles and their access permissions</p>
		</div>
		<div class="flex shrink-0 items-center gap-2">
			<div class="w-96">
				<QueryFilterBar {filter} placeholder="Search roles" onchange={filterChanged} />
			</div>
			<PermissionGate resource="roles" action="create">
				<Button href={newHref}>
					<Plus />
					Create role
				</Button>
			</PermissionGate>
		</div>
	</div>

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 3 }, i (i)}
				<div class="flex items-center gap-4 px-4 py-3">
					<div class="flex-1 space-y-2">
						<Skeleton class="h-4 w-32" />
						<Skeleton class="h-3 w-56" />
					</div>
					<Skeleton class="h-7 w-28 rounded-md" />
				</div>
			{/each}
		</div>
	{:else if roles.length === 0}
		<EmptyState
			icon={Key}
			message={filter.active ? 'No matching roles' : 'No roles yet'}
			description={filter.active
				? 'No results match the current filter.'
				: 'Create a role to group permissions.'}
		>
			{#snippet actions()}
				{#if !filter.active}
					<PermissionGate resource="roles" action="create">
						<Button variant="outline" href={newHref}>
							<Plus />
							Create role
						</Button>
					</PermissionGate>
				{/if}
			{/snippet}
		</EmptyState>
	{:else}
		<div class="box divide-y transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			{#each roles as role (role.id)}
				<div class="flex items-center gap-4 px-4 py-3">
					<div class="min-w-0 flex-1">
						<div class="flex flex-wrap items-center gap-2">
							<span class="text-sm font-semibold">{role.name}</span>
							{#if role.isSystem}
								<Badge variant="secondary">System</Badge>
							{:else}
								<Badge variant="outline">Custom</Badge>
							{/if}
							{#if role.isDefault}
								<Badge variant="success">Default</Badge>
							{/if}
						</div>
						<p class="mt-0.5 truncate text-sm text-muted-foreground">
							{role.description || 'No description'}
						</p>
					</div>

					<div class="flex shrink-0 items-center gap-2">
						{#if hasFullAccess(role.id)}
							<Badge variant="destructive">Full access</Badge>
						{:else if authStore.canUpdateRoles}
							<Button
								size="sm"
								variant="outline"
								href={resolve('/admin/roles/[id]', { id: role.id })}
							>
								Permissions ({getPermCount(role.id)})
							</Button>
						{:else}
							<span class="text-sm text-muted-foreground">{getPermCount(role.id)} permissions</span>
						{/if}

						{#if !role.isSystem}
							<PermissionGate resource="roles" action="delete">
								<Button
									variant="ghost"
									size="sm"
									class="text-destructive hover:text-destructive"
									onclick={() => openDelete(role)}
								>
									Delete
								</Button>
							</PermissionGate>
						{/if}
					</div>
				</div>
			{/each}
		</div>

		<DataPagination {pager} onChange={loadRoles} />
	{/if}
</div>

<ConfirmDialog
	bind:open={deleteDialogOpen}
	title="Delete role"
	confirmLabel="Delete"
	onConfirm={confirmDelete}
	loading={deleting}
>
	{#snippet description()}
		Are you sure you want to delete the <strong>{deleteTarget?.name}</strong> role? Users with this role
		will lose its permissions.
	{/snippet}
</ConfirmDialog>
