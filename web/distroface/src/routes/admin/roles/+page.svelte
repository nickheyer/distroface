<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Badge } from '$lib/components/ui/badge';
	import { Switch } from '$lib/components/ui/switch';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import AppDialog from '$lib/components/app-dialog.svelte';
	import FormPanel from '$lib/components/form-panel.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import {
		Plus, Trash2, Pencil, Loader2, Shield, KeyRound, Save,
		Globe, Target, Package, Building2, Archive
	} from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import { toast } from 'svelte-sonner';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import type { ResourceActions } from '$lib/proto/distroface/v1/role_pb';
	import type { Permission, Role } from '$lib/proto/distroface/v1/types_pb';

	let roles = $state<Role[]>([]);
	let resourceActions = $state<ResourceActions[]>([]);
	let permissionMatrix = $state<Record<string, Permission[]>>({});
	let loading = $state(true);
	let loaded = $state(false);
	const pager = new Pager(20);
	const filter = new QueryFilter([
		{ key: 'name', label: 'Name' },
		{ key: 'description', label: 'Description' }
	]);

	let showCreateDialog = $state(false);
	let showPermissionsDialog = $state(false);
	let editingRole = $state<Role | null>(null);
	let editingPermissions = $state<Record<string, boolean>>({});
	let savingPermissions = $state(false);

	let activeSection = $state<'global' | 'scoped'>('global');
	let scopedPermissions = $state<Record<string, boolean>>({});
	let scopedResource = $state('');
	let scopedObjectRows = $state<Record<string, string[]>>({});
	let objectNames = $state<Record<string, string>>({});
	let addObjectId = $state('');

	let deleteDialogOpen = $state(false);
	let deleteTarget = $state<Role | null>(null);
	let deleting = $state(false);

	let newRoleForm = $state({ name: '', description: '', isDefault: false });
	let creating = $state(false);

	let allActions = $derived(
		[...new Set(resourceActions.flatMap((ra) => ra.actions))].sort()
	);

	let globalPermCount = $derived(
		Object.values(editingPermissions).filter(Boolean).length
	);

	let scopedCount = $derived(Object.values(scopedPermissions).filter(Boolean).length);

	let totalPermCount = $derived(globalPermCount + scopedCount);

	const scopeableResources = ['repositories', 'artifacts', 'organizations'];

	let currentRows = $derived(scopedObjectRows[scopedResource] ?? []);

	let scopedResourceActions = $derived(() => {
		if (!scopedResource) return [];
		const ra = resourceActions.find((r) => r.resource === scopedResource);
		return ra?.actions ?? [];
	});

	const RESOURCE_ICONS: Record<string, typeof Package> = {
		repositories: Package,
		artifacts: Archive,
		organizations: Building2
	};

	function hasFullAccess(roleId: string): boolean {
		const perms = permissionMatrix[roleId] || [];
		return perms.some((p) => p.resource === '*' && p.action === '*');
	}

	function objectName(objectId: string): string {
		return objectNames[`${scopedResource}:${objectId}`] ?? objectId;
	}

	function formatResourceName(resource: string): string {
		return resource.replace(/_/g, ' ');
	}

	function getResourcePermCount(resource: string): number {
		const ra = resourceActions.find((r) => r.resource === resource);
		if (!ra) return 0;
		return ra.actions.filter((act) => editingPermissions[`${resource}:${act}`]).length;
	}

	function isResourceAllChecked(resource: string): boolean {
		const ra = resourceActions.find((r) => r.resource === resource);
		if (!ra) return false;
		return ra.actions.every((act) => editingPermissions[`${resource}:${act}`]);
	}

	function isResourceIndeterminate(resource: string): boolean {
		const ra = resourceActions.find((r) => r.resource === resource);
		if (!ra) return false;
		const checked = ra.actions.filter((act) => editingPermissions[`${resource}:${act}`]).length;
		return checked > 0 && checked < ra.actions.length;
	}

	function togglePermission(key: string) {
		editingPermissions = { ...editingPermissions, [key]: !editingPermissions[key] };
	}

	function toggleResourceAll(resource: string) {
		const ra = resourceActions.find((r) => r.resource === resource);
		if (!ra) return;
		const allEnabled = ra.actions.every((act) => editingPermissions[`${resource}:${act}`]);
		const updated = { ...editingPermissions };
		for (const act of ra.actions) {
			updated[`${resource}:${act}`] = !allEnabled;
		}
		editingPermissions = updated;
	}

	function toggleScopedPermission(objectId: string, action: string) {
		const key = `${scopedResource}:${action}:${objectId}`;
		scopedPermissions = { ...scopedPermissions, [key]: !scopedPermissions[key] };
	}

	function isGlobalCovered(resource: string, action: string): boolean {
		return editingPermissions[`${resource}:${action}`] || false;
	}

	function getScopedCountForObject(objectId: string): number {
		const actions = scopedResourceActions();
		return actions.filter((act) => scopedPermissions[`${scopedResource}:${act}:${objectId}`]).length;
	}

	function isObjectAllChecked(objectId: string): boolean {
		const actions = scopedResourceActions();
		if (actions.length === 0) return false;
		return actions.every(
			(act) => isGlobalCovered(scopedResource, act) || scopedPermissions[`${scopedResource}:${act}:${objectId}`]
		);
	}

	function isObjectIndeterminate(objectId: string): boolean {
		const actions = scopedResourceActions();
		if (actions.length === 0) return false;
		const checked = actions.filter(
			(act) => isGlobalCovered(scopedResource, act) || scopedPermissions[`${scopedResource}:${act}:${objectId}`]
		).length;
		return checked > 0 && checked < actions.length;
	}

	function toggleObjectAll(objectId: string) {
		const actions = scopedResourceActions();
		const nonGlobalActions = actions.filter((act) => !isGlobalCovered(scopedResource, act));
		if (nonGlobalActions.length === 0) return;
		const allEnabled = nonGlobalActions.every(
			(act) => scopedPermissions[`${scopedResource}:${act}:${objectId}`]
		);
		const updated = { ...scopedPermissions };
		for (const act of nonGlobalActions) {
			updated[`${scopedResource}:${act}:${objectId}`] = !allEnabled;
		}
		scopedPermissions = updated;
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
			resourceActions = matrixResp.resourceActions;

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

	async function fetchObjectPage(query: string, pageToken: string) {
		const resp = await rpcClient.role.listScopeableObjects({
			page: { query: { text: query, filters: [] }, pageToken, pageSize: 20 },
			resource: scopedResource
		});
		for (const obj of resp.objects) {
			objectNames[`${obj.resource}:${obj.id}`] = obj.name;
		}
		return {
			items: resp.objects.map((obj) => ({ value: obj.id, label: obj.name })),
			nextPageToken: resp.page?.nextPageToken ?? ''
		};
	}

	$effect(() => {
		if (!addObjectId) return;
		const objectId = addObjectId;
		addObjectId = '';
		const rows = scopedObjectRows[scopedResource] ?? [];
		if (!rows.includes(objectId)) {
			scopedObjectRows = { ...scopedObjectRows, [scopedResource]: [...rows, objectId] };
		}
	});

	function openPermissionsDialog(role: Role) {
		editingRole = role;
		activeSection = 'global';
		scopedResource = scopeableResources[0];
		addObjectId = '';
		objectNames = {};

		const globalMap: Record<string, boolean> = {};
		const scopedMap: Record<string, boolean> = {};
		const rows: Record<string, string[]> = {};
		const rolePerms = permissionMatrix[role.id] || [];

		for (const perm of rolePerms) {
			if (perm.resource === '*' && perm.action === '*') {
				for (const ra of resourceActions) {
					for (const act of ra.actions) {
						globalMap[`${ra.resource}:${act}`] = true;
					}
				}
			} else if (!perm.objectId || perm.objectId === '*') {
				globalMap[`${perm.resource}:${perm.action}`] = true;
			} else {
				scopedMap[`${perm.resource}:${perm.action}:${perm.objectId}`] = true;
				const list = (rows[perm.resource] ??= []);
				if (!list.includes(perm.objectId)) list.push(perm.objectId);
			}
		}

		editingPermissions = globalMap;
		scopedPermissions = scopedMap;
		scopedObjectRows = rows;
		showPermissionsDialog = true;
	}

	async function savePermissions() {
		if (!editingRole) return;
		savingPermissions = true;

		const permissions: { resource: string; action: string; objectId: string }[] = [];

		for (const [key, enabled] of Object.entries(editingPermissions)) {
			if (enabled) {
				const [resource, action] = key.split(':');
				permissions.push({ resource, action, objectId: '*' });
			}
		}

		for (const [key, enabled] of Object.entries(scopedPermissions)) {
			if (enabled) {
				const parts = key.split(':');
				const resource = parts[0];
				const action = parts[1];
				const objectId = parts.slice(2).join(':');
				if (!editingPermissions[`${resource}:${action}`]) {
					permissions.push({ resource, action, objectId });
				}
			}
		}

		try {
			await rpcClient.role.updatePermissions({
				roleId: editingRole.id,
				permissions
			});
			toast.success('Permissions updated');
			showPermissionsDialog = false;
			editingRole = null;
			await loadRoles();
		} catch {
			// error interceptor
		} finally {
			savingPermissions = false;
		}
	}

	async function createRole() {
		if (!newRoleForm.name.trim()) return;
		creating = true;
		try {
			await rpcClient.role.createRole({
				name: newRoleForm.name.trim().toLowerCase(),
				description: newRoleForm.description.trim(),
				isDefault: newRoleForm.isDefault,
				permissions: []
			});
			toast.success('Role created');
			showCreateDialog = false;
			newRoleForm = { name: '', description: '', isDefault: false };
			pager.reset();
			await loadRoles();
		} catch {
			// error interceptor
		} finally {
			creating = false;
		}
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
			toast.success('Role deleted');
			deleteDialogOpen = false;
			await loadRoles();
		} catch {
			// error interceptor
		} finally {
			deleting = false;
		}
	}

	onMount(() => {
		if (!authStore.hasPermission('roles', 'read')) { goto(resolve('/admin')); return; }
		loadRoles();
	});
</script>

<div class="space-y-4">
	<div class="section-header">
		<div>
			<h2 class="section-title">Roles & Permissions</h2>
			<p class="section-subtitle">Manage roles and their access permissions</p>
		</div>
		<div class="flex items-center gap-2">
			<div class="w-96">
				<QueryFilterBar {filter} placeholder="Search roles..." onchange={filterChanged} />
			</div>
			<PermissionGate resource="roles" action="create">
				<Button size="sm" onclick={() => (showCreateDialog = true)}>
					<Plus class="h-4 w-4 mr-1" />
					Create Role
				</Button>
			</PermissionGate>
		</div>
	</div>

	{#if !loaded}
		<div class="space-y-3">
			{#each { length: 3 }, i (i)}
				<Skeleton class="h-20 w-full rounded-xl" />
			{/each}
		</div>
	{:else}
		<div class="space-y-2 transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			{#each roles as role (role.id)}
				<div class="rounded-xl border border-border/60 bg-card p-4">
					<div class="flex items-center gap-4">
						<div class="h-10 w-10 rounded-lg shrink-0 flex items-center justify-center {role.isSystem ? 'bg-primary/10' : 'bg-muted'}">
							<Shield class="h-5 w-5 {role.isSystem ? 'text-primary' : 'text-muted-foreground'}" />
						</div>

						<div class="flex-1 min-w-0">
							<div class="flex items-center gap-2">
								<span class="font-medium">{role.name}</span>
								{#if role.isSystem}
									<Badge variant="secondary" class="text-[10px] px-1.5 py-0">System</Badge>
								{:else}
									<Badge variant="outline" class="text-[10px] px-1.5 py-0">Custom</Badge>
								{/if}
								{#if role.isDefault}
									<Badge variant="outline" class="text-[10px] px-1.5 py-0 border-success/30 text-success">Default</Badge>
								{/if}
							</div>
							<p class="text-[13px] text-muted-foreground mt-0.5 truncate">
								{role.description || 'No description'}
							</p>
						</div>

						<div class="shrink-0 flex items-center gap-2">
							{#if hasFullAccess(role.id)}
								<Badge variant="destructive" class="text-xs">Full Access</Badge>
							{:else if authStore.canUpdateRoles}
								<Button
									size="sm"
									variant="outline"
									onclick={() => openPermissionsDialog(role)}
								>
									<Pencil class="mr-1.5 h-3 w-3" />
									Permissions ({getPermCount(role.id)})
								</Button>
							{:else}
								<span class="text-sm text-muted-foreground">{getPermCount(role.id)} permissions</span>
							{/if}

							{#if !role.isSystem}
								<PermissionGate resource="roles" action="delete">
									<Button
										variant="ghost"
										size="icon"
										class="h-8 w-8 shrink-0 text-destructive hover:text-destructive"
										onclick={() => openDelete(role)}
									>
										<Trash2 class="h-4 w-4" />
									</Button>
								</PermissionGate>
							{/if}
						</div>
					</div>
				</div>
			{/each}
		</div>

		<DataPagination
			page={pager.page} pageSize={pager.pageSize} totalCount={pager.totalCount}
			onPrev={() => { if (pager.prev()) loadRoles(); }}
			onNext={() => { if (pager.next()) loadRoles(); }}
		/>
	{/if}
</div>

<FormPanel
	bind:open={showCreateDialog}
	title="Create Role"
	description="Create a custom role with specific permissions. You can configure permissions after creation."
	icon={Shield}
>
	<div class="space-y-6">
		<FormSection title="Details">
			<div class="space-y-3">
				<FormField label="Name" id="role-name" help="Lowercase letters, numbers, hyphens. This is used as the role identifier." required>
					<Input id="role-name" bind:value={newRoleForm.name} placeholder="e.g., moderator" />
				</FormField>
				<FormField label="Description" id="role-desc" help="A human-readable description of what this role is for.">
					<Input id="role-desc" bind:value={newRoleForm.description} placeholder="What this role is for" />
				</FormField>
			</div>
		</FormSection>

		<FormSection title="Options">
			<FormField label="Default role" help="Automatically assign to new users on registration." horizontal>
				<Switch
					checked={newRoleForm.isDefault}
					onCheckedChange={(checked) => (newRoleForm.isDefault = checked)}
				/>
			</FormField>
		</FormSection>
	</div>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (showCreateDialog = false)}>Cancel</Button>
		<Button onclick={createRole} disabled={creating || !newRoleForm.name.trim()}>
			{creating ? 'Creating...' : 'Create Role'}
		</Button>
	{/snippet}
</FormPanel>

<AppDialog
	bind:open={showPermissionsDialog}
	title={activeSection === 'global' ? 'Global Permissions' : 'Scoped Permissions'}
	icon={activeSection === 'global' ? KeyRound : Target}
	sidebarTitle={editingRole?.name ?? ''}
	sidebarSubtitle={editingRole?.isSystem ? 'System role' : 'Custom role'}
	size="full"
	description={activeSection === 'global'
		? 'Global permissions apply to all objects of each resource type.'
		: 'Scoped permissions grant access to specific objects only.'}
>
	{#snippet sidebar()}
		<nav class="flex-1 p-4 space-y-1">
			<button
				class="w-full flex items-center gap-3 px-4 py-3 rounded-lg text-left transition-colors {activeSection === 'global'
					? 'bg-primary text-primary-foreground'
					: 'hover:bg-muted'}"
				onclick={() => (activeSection = 'global')}
			>
				<Globe class="h-5 w-5" />
				<span class="font-medium flex-1">Global</span>
				<span class="text-xs opacity-75">{globalPermCount}</span>
			</button>
			<button
				class="w-full flex items-center gap-3 px-4 py-3 rounded-lg text-left transition-colors {activeSection === 'scoped'
					? 'bg-primary text-primary-foreground'
					: 'hover:bg-muted'}"
				onclick={() => (activeSection = 'scoped')}
			>
				<Target class="h-5 w-5" />
				<span class="font-medium flex-1">Scoped</span>
				<span class="text-xs opacity-75">{scopedCount}</span>
			</button>
		</nav>
	{/snippet}

	{#if activeSection === 'global'}
		<div class="overflow-x-auto border rounded-xl">
			<Table>
				<TableHeader>
					<TableRow class="bg-muted/50">
						<TableHead class="th sticky left-0 bg-muted/50 z-10 w-50 border-r">Resource</TableHead>
						{#each allActions as action (action)}
							<TableHead class="th text-center">
								<span class="capitalize">{action}</span>
							</TableHead>
						{/each}
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each resourceActions as ra (ra.resource)}
						{@const count = getResourcePermCount(ra.resource)}
						{@const total = ra.actions.length}
						<TableRow class="hover:bg-muted/30">
							<TableCell class="sticky left-0 bg-background z-10 font-medium border-r px-3">
								<div class="flex items-center gap-2">
									<Checkbox
										checked={isResourceAllChecked(ra.resource)}
										indeterminate={isResourceIndeterminate(ra.resource)}
										onCheckedChange={() => toggleResourceAll(ra.resource)}
									/>
									<span class="capitalize text-sm">{formatResourceName(ra.resource)}</span>
									{#if count > 0}
										<Badge variant="secondary" class="text-[10px] ml-auto px-1.5 py-0">{count}/{total}</Badge>
									{/if}
								</div>
							</TableCell>
							{#each allActions as action (action)}
								{@const key = `${ra.resource}:${action}`}
								{@const hasAction = ra.actions.includes(action)}
								{@const checked = hasAction && (editingPermissions[key] || false)}
								<TableCell class="text-center px-3">
									{#if hasAction}
										<div class="flex justify-center">
											<Checkbox {checked} onCheckedChange={() => togglePermission(key)} />
										</div>
									{:else}
										<span class="text-muted-foreground/20">&mdash;</span>
									{/if}
								</TableCell>
							{/each}
						</TableRow>
					{/each}
				</TableBody>
			</Table>
		</div>
	{:else}
		<div class="flex gap-2 mb-4">
			{#each scopeableResources as res (res)}
				{@const Icon = RESOURCE_ICONS[res] || Package}
				<button
					class="flex items-center gap-2 px-4 py-2 rounded-lg border text-sm font-medium transition-colors {scopedResource === res
						? 'bg-primary text-primary-foreground border-primary'
						: 'bg-background hover:bg-muted border-border'}"
					onclick={() => (scopedResource = res)}
				>
					<Icon class="h-4 w-4" />
					<span class="capitalize">{formatResourceName(res)}</span>
				</button>
			{/each}
		</div>

		{#key scopedResource}
			<div class="mb-3">
				<AsyncSelect
					bind:selected={addObjectId}
					placeholder="Add {formatResourceName(scopedResource)}..."
					searchPlaceholder="Search {formatResourceName(scopedResource)}..."
					fetchPage={fetchObjectPage}
				/>
			</div>
		{/key}

		{#if currentRows.length === 0}
			<EmptyState
				message="No scoped {formatResourceName(scopedResource)}"
				description="Search above to add {formatResourceName(scopedResource)} and grant per-object permissions."
				icon={RESOURCE_ICONS[scopedResource] || Package}
			/>
		{:else}
			<div class="overflow-x-auto border rounded-xl">
				<Table>
					<TableHeader>
						<TableRow class="bg-muted/50">
							<TableHead class="th sticky left-0 bg-muted/50 z-10 w-60 border-r">Object</TableHead>
							{#each scopedResourceActions() as action (action)}
								<TableHead class="th text-center">
									<span class="capitalize">{action}</span>
								</TableHead>
							{/each}
						</TableRow>
					</TableHeader>
					<TableBody>
						{#each currentRows as objectId (objectId)}
							{@const objScopedCount = getScopedCountForObject(objectId)}
							<TableRow class="hover:bg-muted/30">
								<TableCell class="sticky left-0 bg-background z-10 font-medium border-r px-3">
									<div class="flex items-center gap-2">
										<Checkbox
											checked={isObjectAllChecked(objectId)}
											indeterminate={isObjectIndeterminate(objectId)}
											onCheckedChange={() => toggleObjectAll(objectId)}
										/>
										<span class="text-sm truncate max-w-40">{objectName(objectId)}</span>
										{#if objScopedCount > 0}
											<Badge variant="secondary" class="text-[10px] ml-auto px-1.5 py-0 shrink-0">{objScopedCount}</Badge>
										{/if}
									</div>
								</TableCell>
								{#each scopedResourceActions() as action (action)}
									{@const globalCovered = isGlobalCovered(scopedResource, action)}
									{@const scopedKey = `${scopedResource}:${action}:${objectId}`}
									{@const checked = globalCovered || (scopedPermissions[scopedKey] || false)}
									<TableCell class="text-center px-3">
										{#if globalCovered}
											<div class="flex justify-center items-center gap-1">
												<Checkbox checked={true} disabled />
												<Badge variant="outline" class="text-[9px] px-1 py-0 text-muted-foreground">(global)</Badge>
											</div>
										{:else}
											<div class="flex justify-center">
												<Checkbox {checked} onCheckedChange={() => toggleScopedPermission(objectId, action)} />
											</div>
										{/if}
									</TableCell>
								{/each}
							</TableRow>
						{/each}
					</TableBody>
				</Table>
			</div>
		{/if}
	{/if}

	{#snippet footer()}
		<Button variant="outline" onclick={() => (showPermissionsDialog = false)}>
			Cancel
		</Button>
		<Button onclick={savePermissions} disabled={savingPermissions} class="gap-2">
			{#if savingPermissions}
				<Loader2 class="h-4 w-4 animate-spin" />
				Saving...
			{:else}
				<Save class="h-4 w-4" />
				Save Permissions ({totalPermCount})
			{/if}
		</Button>
	{/snippet}
</AppDialog>

<ConfirmDialog
	bind:open={deleteDialogOpen}
	title="Delete Role"
	confirmLabel="Delete"
	onConfirm={confirmDelete}
	loading={deleting}
	icon={Trash2}
>
	{#snippet description()}
		Are you sure you want to delete the <strong>{deleteTarget?.name}</strong> role? Users with
		this role will lose its permissions.
	{/snippet}
</ConfirmDialog>
