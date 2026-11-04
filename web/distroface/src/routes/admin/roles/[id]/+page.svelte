<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Key, Loader, ShieldAlert } from '$lib/icons';
	import { toast } from 'svelte-sonner';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription, AlertTitle } from '$lib/components/ui/alert';
	import {
		Table,
		TableBody,
		TableCell,
		TableHead,
		TableHeader,
		TableRow
	} from '$lib/components/ui/table';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { cn } from '$lib/utils';
	import type { ResourceActions } from '$lib/proto/distroface/v1/role_pb';
	import type { Role } from '$lib/proto/distroface/v1/types_pb';

	const id = $derived(page.params.id ?? '');
	const listHref = resolve('/admin/roles');
	const canUpdate = $derived(authStore.hasPermission('roles', 'update'));
	const scopeableResources = ['repositories', 'artifacts', 'organizations'];

	let role = $state<Role | undefined>(undefined);
	let resourceActions = $state<ResourceActions[]>([]);
	let loading = $state(true);
	let fullAccess = $state(false);

	let name = $state('');
	let description = $state('');
	let isDefault = $state(false);
	let savingDetails = $state(false);

	let selectedResource = $state('');
	let editingPermissions = $state<Record<string, boolean>>({});
	let scopedPermissions = $state<Record<string, boolean>>({});
	let scopedObjectRows = $state<Record<string, string[]>>({});
	let objectNames = $state<Record<string, string>>({});
	let addObjectId = $state('');
	let savingPermissions = $state(false);

	const detailsChanged = $derived(
		!!role &&
			(name.trim().toLowerCase() !== role.name ||
				description.trim() !== role.description ||
				isDefault !== role.isDefault)
	);
	const globalPermCount = $derived(Object.values(editingPermissions).filter(Boolean).length);
	const scopedCount = $derived(Object.values(scopedPermissions).filter(Boolean).length);
	const totalPermCount = $derived(globalPermCount + scopedCount);
	const selectedActions = $derived(
		resourceActions.find((r) => r.resource === selectedResource)?.actions ?? []
	);
	const isScopeable = $derived(scopeableResources.includes(selectedResource));
	const currentRows = $derived(scopedObjectRows[selectedResource] ?? []);

	function formatResourceName(resource: string): string {
		return resource.replace(/_/g, ' ');
	}

	function objectName(objectId: string): string {
		return objectNames[`${selectedResource}:${objectId}`] ?? objectId;
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
		const key = `${selectedResource}:${action}:${objectId}`;
		scopedPermissions = { ...scopedPermissions, [key]: !scopedPermissions[key] };
	}

	function isGlobalCovered(resource: string, action: string): boolean {
		return editingPermissions[`${resource}:${action}`] || false;
	}

	function getScopedCountForObject(objectId: string): number {
		return selectedActions.filter(
			(act) => scopedPermissions[`${selectedResource}:${act}:${objectId}`]
		).length;
	}

	function isObjectAllChecked(objectId: string): boolean {
		if (selectedActions.length === 0) return false;
		return selectedActions.every(
			(act) =>
				isGlobalCovered(selectedResource, act) ||
				scopedPermissions[`${selectedResource}:${act}:${objectId}`]
		);
	}

	function isObjectIndeterminate(objectId: string): boolean {
		if (selectedActions.length === 0) return false;
		const checked = selectedActions.filter(
			(act) =>
				isGlobalCovered(selectedResource, act) ||
				scopedPermissions[`${selectedResource}:${act}:${objectId}`]
		).length;
		return checked > 0 && checked < selectedActions.length;
	}

	function toggleObjectAll(objectId: string) {
		const nonGlobalActions = selectedActions.filter(
			(act) => !isGlobalCovered(selectedResource, act)
		);
		if (nonGlobalActions.length === 0) return;
		const allEnabled = nonGlobalActions.every(
			(act) => scopedPermissions[`${selectedResource}:${act}:${objectId}`]
		);
		const updated = { ...scopedPermissions };
		for (const act of nonGlobalActions) {
			updated[`${selectedResource}:${act}:${objectId}`] = !allEnabled;
		}
		scopedPermissions = updated;
	}

	async function fetchObjectPage(query: string, pageToken: string) {
		const resp = await rpcClient.role.listScopeableObjects({
			page: { query: { text: query, filters: [] }, pageToken, pageSize: 20 },
			resource: selectedResource
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
		const rows = scopedObjectRows[selectedResource] ?? [];
		if (!rows.includes(objectId)) {
			scopedObjectRows = { ...scopedObjectRows, [selectedResource]: [...rows, objectId] };
		}
	});

	function prefill(r: Role) {
		name = r.name;
		description = r.description;
		isDefault = r.isDefault;
		selectedResource = resourceActions[0]?.resource ?? '';
		objectNames = {};
		fullAccess = r.permissions.some((p) => p.resource === '*' && p.action === '*');

		const globalMap: Record<string, boolean> = {};
		const scopedMap: Record<string, boolean> = {};
		const rows: Record<string, string[]> = {};
		for (const perm of r.permissions) {
			if (perm.resource === '*' && perm.action === '*') continue;
			if (!perm.objectId || perm.objectId === '*') {
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
	}

	async function load() {
		loading = true;
		try {
			const [roleResp, matrixResp] = await Promise.all([
				rpcClient.role.getRole({ id }, silentCallOptions),
				rpcClient.role.getPermissionMatrix({}, silentCallOptions)
			]);
			role = roleResp.role;
			resourceActions = matrixResp.resourceActions;
			if (role) prefill(role);
		} catch {
			role = undefined;
		} finally {
			loading = false;
		}
	}

	async function saveDetails() {
		if (!role || role.isSystem || !detailsChanged || !name.trim() || savingDetails) return;
		savingDetails = true;
		try {
			const resp = await rpcClient.role.updateRole({
				id: role.id,
				name: name.trim().toLowerCase(),
				description: description.trim(),
				isDefault
			});
			if (resp.role) role = resp.role;
		} catch {
			savingDetails = false;
			return;
		}
		savingDetails = false;
		toast.success('Role updated');
	}

	async function savePermissions() {
		if (!role || fullAccess || savingPermissions) return;
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
			await rpcClient.role.updatePermissions({ roleId: role.id, permissions });
		} catch {
			savingPermissions = false;
			return;
		}
		toast.success('Permissions saved');
		await goto(resolve('/admin/roles'));
	}

	onMount(() => {
		if (canUpdate) load();
	});
</script>

<svelte:head>
	<title>Edit role{role ? ` - ${role.name}` : ''}</title>
</svelte:head>

{#if !canUpdate}
	<EmptyState
		icon={Key}
		message="You cannot edit roles"
		description="Ask an administrator for the roles permission."
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to roles</Button>
		{/snippet}
	</EmptyState>
{:else if loading}
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
{:else if !role}
	<EmptyState
		icon={Key}
		message="Role not found"
		description="No role with this id exists on this instance."
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to roles</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		level="section"
		title="Edit role"
		description="Name, default assignment, and what the role can do across this instance."
		backHref={listHref}
		backLabel="Roles"
	>
		<FormSection
			title="Details"
			description={role.isSystem
				? 'System role, its name and description are fixed.'
				: 'Name and whether new users get it.'}
		>
			{#snippet footer()}
				{#if !role?.isSystem}
					<Button
						variant="outline"
						onclick={saveDetails}
						disabled={savingDetails || !detailsChanged || !name.trim()}
					>
						{#if savingDetails}<Loader class="animate-spin" />{/if}
						{savingDetails ? 'Saving' : 'Save details'}
					</Button>
				{/if}
			{/snippet}
			<FormField
				label="Name"
				id="role-name"
				required
				help="Lowercase letters, numbers, hyphens"
				span="half"
			>
				<Input
					id="role-name"
					bind:value={name}
					placeholder="moderator"
					class="font-mono"
					autocomplete="off"
					spellcheck="false"
					disabled={role.isSystem}
				/>
			</FormField>
			<FormField label="Description" id="role-desc">
				<Input
					id="role-desc"
					bind:value={description}
					placeholder="What this role is for"
					disabled={role.isSystem}
				/>
			</FormField>
			<FormField label="Default role" help="Granted to new users" layout="toggle">
				<Switch bind:checked={isDefault} disabled={role.isSystem} />
			</FormField>
		</FormSection>

		<div class="space-y-4">
			<div>
				<h3 class="text-base leading-6 font-semibold">Permissions</h3>
				<p class="mt-1 text-sm text-muted-foreground">
					Global grants apply to every object of a resource, scoped grants only to the objects you
					add.
				</p>
			</div>
			{#if fullAccess}
				<Alert variant="warning">
					<ShieldAlert />
					<AlertTitle>Full access</AlertTitle>
					<AlertDescription
						>This role grants every action on every resource and cannot be narrowed.</AlertDescription
					>
				</Alert>
			{:else}
				<div class="flex flex-col gap-6 md:flex-row">
					<nav class="shrink-0 md:w-44" aria-label="Resources">
						<div
							class="flex gap-0.5 overflow-x-auto pb-2 md:sticky md:top-20 md:flex-col md:overflow-visible md:pb-0"
						>
							{#each resourceActions as ra (ra.resource)}
								{@const active = selectedResource === ra.resource}
								<button
									type="button"
									aria-current={active ? 'true' : undefined}
									onclick={() => (selectedResource = ra.resource)}
									class={cn(
										'relative flex items-center gap-2.5 rounded-md px-3 py-1.5 text-left text-sm whitespace-nowrap transition-colors',
										active
											? 'bg-muted font-medium text-foreground'
											: 'text-muted-foreground hover:bg-muted/60 hover:text-foreground'
									)}
								>
									{#if active}
										<span
											class="absolute inset-y-1.5 left-0 hidden w-0.5 rounded-full bg-primary md:block"
											aria-hidden="true"
										></span>
									{/if}
									<span class="flex-1 capitalize">{formatResourceName(ra.resource)}</span>
									<span class="text-xs text-muted-foreground tabular-nums"
										>{getResourcePermCount(ra.resource)}/{ra.actions.length}</span
									>
								</button>
							{/each}
						</div>
					</nav>

					<div class="min-w-0 flex-1 space-y-4">
						{#if isScopeable}
							{#key selectedResource}
								<div class="sm:max-w-sm">
									<AsyncSelect
										bind:selected={addObjectId}
										placeholder="Add {formatResourceName(selectedResource)}"
										searchPlaceholder="Search {formatResourceName(selectedResource)}"
										fetchPage={fetchObjectPage}
									/>
								</div>
							{/key}
						{/if}

						<div class="data-table">
							<Table>
								<TableHeader>
									<TableRow>
										<TableHead class="sticky left-0 z-10 w-48 border-r bg-muted">Scope</TableHead>
										{#each selectedActions as action (action)}
											<TableHead class="text-center">
												<span class="capitalize">{action}</span>
											</TableHead>
										{/each}
									</TableRow>
								</TableHeader>
								<TableBody>
									<TableRow>
										{@const count = getResourcePermCount(selectedResource)}
										<TableCell class="sticky left-0 z-10 border-r bg-card font-medium">
											<div class="flex items-center gap-2">
												<Checkbox
													checked={isResourceAllChecked(selectedResource)}
													indeterminate={isResourceIndeterminate(selectedResource)}
													onCheckedChange={() => toggleResourceAll(selectedResource)}
													aria-label="Toggle all {formatResourceName(selectedResource)} actions"
												/>
												<span class="text-sm">All {formatResourceName(selectedResource)}</span>
												{#if count > 0}
													<Badge variant="secondary" class="ml-auto tabular-nums"
														>{count}/{selectedActions.length}</Badge
													>
												{/if}
											</div>
										</TableCell>
										{#each selectedActions as action (action)}
											{@const key = `${selectedResource}:${action}`}
											<TableCell class="text-center">
												<div class="flex justify-center">
													<Checkbox
														checked={editingPermissions[key] || false}
														onCheckedChange={() => togglePermission(key)}
														aria-label="{action} {formatResourceName(selectedResource)}"
													/>
												</div>
											</TableCell>
										{/each}
									</TableRow>
									{#each currentRows as objectId (objectId)}
										{@const objScopedCount = getScopedCountForObject(objectId)}
										<TableRow>
											<TableCell class="sticky left-0 z-10 border-r bg-card font-medium">
												<div class="flex items-center gap-2">
													<Checkbox
														checked={isObjectAllChecked(objectId)}
														indeterminate={isObjectIndeterminate(objectId)}
														onCheckedChange={() => toggleObjectAll(objectId)}
														aria-label="Toggle all actions for {objectName(objectId)}"
													/>
													<span class="max-w-40 truncate text-sm">{objectName(objectId)}</span>
													{#if objScopedCount > 0}
														<Badge variant="secondary" class="ml-auto shrink-0 tabular-nums"
															>{objScopedCount}</Badge
														>
													{/if}
												</div>
											</TableCell>
											{#each selectedActions as action (action)}
												{@const globalCovered = isGlobalCovered(selectedResource, action)}
												{@const scopedKey = `${selectedResource}:${action}:${objectId}`}
												<TableCell class="text-center">
													{#if globalCovered}
														<div class="flex items-center justify-center gap-1">
															<Checkbox
																checked={true}
																disabled
																aria-label="{action} granted globally"
															/>
															<Badge variant="outline">global</Badge>
														</div>
													{:else}
														<div class="flex justify-center">
															<Checkbox
																checked={scopedPermissions[scopedKey] || false}
																onCheckedChange={() => toggleScopedPermission(objectId, action)}
																aria-label="{action} {objectName(objectId)}"
															/>
														</div>
													{/if}
												</TableCell>
											{/each}
										</TableRow>
									{/each}
									{#if isScopeable && currentRows.length === 0}
										<TableRow>
											<TableCell
												colspan={selectedActions.length + 1}
												class="py-6 text-center text-sm whitespace-normal text-muted-foreground"
											>
												No scoped {formatResourceName(selectedResource)}, search above to add one
											</TableCell>
										</TableRow>
									{/if}
								</TableBody>
							</Table>
						</div>
					</div>
				</div>
			{/if}
		</div>

		{#snippet footer()}
			<Button onclick={savePermissions} disabled={savingPermissions || fullAccess}>
				{#if savingPermissions}<Loader class="animate-spin" />{/if}
				{savingPermissions ? 'Saving' : `Save permissions (${totalPermCount})`}
			</Button>
			<Button variant="ghost-muted" href={listHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}
