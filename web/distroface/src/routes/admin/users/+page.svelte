<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import { Input } from '$lib/components/ui/input';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import FormPanel from '$lib/components/form-panel.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import BulkActionBar from '$lib/components/bulk-action-bar.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import PasswordInput from '$lib/components/password-input.svelte';
	import PasswordStrength from '$lib/components/password-strength.svelte';
	import {
		Pencil, Trash2, UserCog, UserPlus, UserCheck, UserX, Sparkles, KeyRound, Plus, Minus
	} from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import type { User, BulkOperationError } from '$lib/proto/distroface/v1/types_pb';

	let users = $state<User[]>([]);
	let loading = $state(true);
	let loaded = $state(false);
	const pager = new Pager(20);
	const filter = new QueryFilter([
		{ key: 'username', label: 'Username' },
		{ key: 'email', label: 'Email' },
		{ key: 'display_name', label: 'Display Name' },
		{ key: 'auth_provider', label: 'Auth Provider' }
	]);

	let editPanelOpen = $state(false);
	let editUser = $state<User | null>(null);
	let editSelectedRoles = $state<string[]>([]);
	let editEmail = $state('');
	let editActive = $state(true);
	let editSaving = $state(false);

	let deleteDialogOpen = $state(false);
	let deleteUser = $state<User | null>(null);
	let deleting = $state(false);

	let createPanelOpen = $state(false);
	let createUsername = $state('');
	let createDisplayName = $state('');
	let createEmail = $state('');
	let createPassword = $state('');
	let createSelectedRoles = $state<string[]>([]);
	let createMustChange = $state(true);
	let createSaving = $state(false);

	let bulkRoleId = $state('');

	const selected = new SvelteSet<string>();
	const pageIds = $derived(users.map((u) => u.id));
	const allOnPageSelected = $derived(pageIds.length > 0 && pageIds.every((id) => selected.has(id)));
	const someOnPageSelected = $derived(pageIds.some((id) => selected.has(id)));

	let bulkDeleteDialogOpen = $state(false);
	let bulkWorking = $state(false);

	function getInitials(user: User): string {
		const name = user.displayName || user.username;
		return name.split(/[\s-]+/).map((w) => w[0]).join('').toUpperCase().slice(0, 2);
	}

	async function loadUsers() {
		loading = true;
		try {
			const resp = await rpcClient.user.listUsers({ page: pager.request(filter.request()) });
			users = resp.users;
			pager.apply(resp.page);
		} catch {
			// error interceptor
		} finally {
			loading = false;
			loaded = true;
		}
	}

	async function fetchRolePage(query: string, pageToken: string) {
		const resp = await rpcClient.role.listRoles({
			page: { query: { text: query, filters: [] }, pageToken, pageSize: 20 }
		});
		return {
			items: resp.roles.map((r) => ({ value: r.id, label: r.name, description: r.description })),
			nextPageToken: resp.page?.nextPageToken ?? ''
		};
	}

	function filterChanged() {
		pager.reset();
		loadUsers();
	}

	function toggleSelectPage() {
		if (allOnPageSelected) {
			for (const id of pageIds) selected.delete(id);
		} else {
			for (const id of pageIds) selected.add(id);
		}
	}

	function toggleSelect(id: string) {
		if (selected.has(id)) selected.delete(id);
		else selected.add(id);
	}

	function reportBulkErrors(errors: BulkOperationError[]) {
		if (errors.length === 0) return;
		const lookup = new Map(users.map((u) => [u.id, u.username]));
		const first = errors[0];
		const who = lookup.get(first.id) ?? first.id;
		toast.error(
			errors.length === 1
				? `${who}: ${first.error}`
				: `${errors.length} failed (${who}: ${first.error}, ...)`
		);
	}

	async function bulkSetActive(active: boolean) {
		bulkWorking = true;
		try {
			const resp = await rpcClient.user.adminBulkUpdateUsers({
				userIds: [...selected],
				isActive: active
			});
			toast.success(`${resp.updatedCount} user${resp.updatedCount !== 1 ? 's' : ''} ${active ? 'activated' : 'deactivated'}`);
			reportBulkErrors(resp.errors);
			selected.clear();
			await loadUsers();
		} catch {
			// error interceptor
		} finally {
			bulkWorking = false;
		}
	}

	async function bulkRole(add: boolean) {
		if (!bulkRoleId) return;
		bulkWorking = true;
		try {
			const resp = await rpcClient.user.adminBulkUpdateUsers({
				userIds: [...selected],
				addRoleIds: add ? [bulkRoleId] : [],
				removeRoleIds: add ? [] : [bulkRoleId]
			});
			toast.success(`Role ${add ? 'added to' : 'removed from'} ${resp.updatedCount} user${resp.updatedCount !== 1 ? 's' : ''}`);
			reportBulkErrors(resp.errors);
			bulkRoleId = '';
			selected.clear();
			await loadUsers();
		} catch {
			// error interceptor
		} finally {
			bulkWorking = false;
		}
	}

	async function confirmBulkDelete() {
		bulkWorking = true;
		try {
			const resp = await rpcClient.user.adminBulkDeleteUsers({ userIds: [...selected] });
			toast.success(`${resp.deletedCount} user${resp.deletedCount !== 1 ? 's' : ''} deleted`);
			reportBulkErrors(resp.errors);
			selected.clear();
			bulkDeleteDialogOpen = false;
			await loadUsers();
		} catch {
			// error interceptor
		} finally {
			bulkWorking = false;
		}
	}

	function openCreate() {
		createUsername = '';
		createDisplayName = '';
		createEmail = '';
		createPassword = '';
		createSelectedRoles = [];
		createMustChange = true;
		createPanelOpen = true;
	}

	function generatePassword() {
		const chars = 'ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnopqrstuvwxyz23456789!@#$%^&*';
		const bytes = crypto.getRandomValues(new Uint8Array(20));
		createPassword = Array.from(bytes, (b) => chars[b % chars.length]).join('');
	}

	async function saveCreate() {
		if (!createUsername || !createPassword) {
			toast.error('Username and password are required');
			return;
		}
		if (createPassword.length < 8) {
			toast.error('Password must be at least 8 characters');
			return;
		}
		createSaving = true;
		try {
			await rpcClient.user.adminCreateUser({
				username: createUsername,
				password: createPassword,
				email: createEmail,
				displayName: createDisplayName,
				roleIds: createSelectedRoles,
				mustChangePassword: createMustChange
			});
			toast.success(`User ${createUsername} created`);
			createPanelOpen = false;
			await loadUsers();
		} catch {
			// error interceptor
		} finally {
			createSaving = false;
		}
	}

	function openEdit(user: User) {
		editUser = user;
		editSelectedRoles = user.roles.map((r) => r.id);
		editEmail = user.email;
		editActive = user.isActive;
		editPanelOpen = true;
	}

	async function saveEdit() {
		if (!editUser) return;
		editSaving = true;
		try {
			await rpcClient.user.adminUpdateUser({
				userId: editUser.id,
				email: editEmail || undefined,
				isActive: editActive,
				roleIds: editSelectedRoles
			});
			toast.success('User updated');
			editPanelOpen = false;
			await loadUsers();
		} catch {
			// error interceptor
		} finally {
			editSaving = false;
		}
	}

	function openDelete(user: User) {
		deleteUser = user;
		deleteDialogOpen = true;
	}

	async function confirmDelete() {
		if (!deleteUser) return;
		deleting = true;
		try {
			await rpcClient.user.adminDeleteUser({ userId: deleteUser.id });
			toast.success('User deleted');
			deleteDialogOpen = false;
			await loadUsers();
		} catch {
			// error interceptor
		} finally {
			deleting = false;
		}
	}

	const canBulkSelect = $derived(authStore.canUpdateUsers || authStore.canDeleteUsers);

	onMount(() => {
		if (!authStore.hasPermission('users', 'read')) { goto(resolve('/admin')); return; }
		loadUsers();
	});
</script>

<div class="space-y-4">
	<div class="section-header">
		<div>
			<h2 class="section-title">Users</h2>
			<p class="section-subtitle">
				{#if pager.totalCount > 0}{pager.totalCount} registered user{pager.totalCount !== 1 ? 's' : ''}{:else}Manage user accounts{/if}
			</p>
		</div>
		<div class="flex items-center gap-2">
			<div class="w-96">
				<QueryFilterBar {filter} placeholder="Search users..." onchange={filterChanged} />
			</div>
			<PermissionGate resource="users" action="create">
				<Button class="h-9" onclick={openCreate}>
					<UserPlus class="h-4 w-4 mr-1.5" />
					Create User
				</Button>
			</PermissionGate>
		</div>
	</div>

	{#if !loaded}
		<div class="space-y-2">
			{#each { length: 4 }, i (i)}
				<Skeleton class="h-12 w-full rounded-lg" />
			{/each}
		</div>
	{:else}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table>
				<TableHeader>
					<TableRow class="bg-muted/30 hover:bg-muted/30">
						<PermissionGate allowed={canBulkSelect}>
							<TableHead class="th w-10">
								<Checkbox
									checked={allOnPageSelected}
									indeterminate={someOnPageSelected && !allOnPageSelected}
									onCheckedChange={toggleSelectPage}
									aria-label="Select all on page"
								/>
							</TableHead>
						</PermissionGate>
						<TableHead class="th">User</TableHead>
						<TableHead class="th">Email</TableHead>
						<TableHead class="th">Provider</TableHead>
						<TableHead class="th">Roles</TableHead>
						<TableHead class="th">Status</TableHead>
						<TableHead class="th">Joined</TableHead>
						<PermissionGate allowed={canBulkSelect}>
							<TableHead class="th w-20"></TableHead>
						</PermissionGate>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each users as user (user.id)}
						<TableRow class={selected.has(user.id) ? 'bg-primary/5 hover:bg-primary/5' : ''}>
							<PermissionGate allowed={canBulkSelect}>
								<TableCell class="py-3 px-3">
									<Checkbox
										checked={selected.has(user.id)}
										onCheckedChange={() => toggleSelect(user.id)}
										aria-label={`Select ${user.username}`}
									/>
								</TableCell>
							</PermissionGate>
							<TableCell class="py-3 px-3">
								<div class="flex items-center gap-2.5">
									<Avatar class="h-7 w-7">
										<AvatarFallback class="text-[10px] bg-primary/10 text-primary font-medium">
											{getInitials(user)}
										</AvatarFallback>
									</Avatar>
									<a href={resolve(`/${user.username}`)} class="font-medium text-sm hover:text-primary transition-colors">
										{user.username}
									</a>
									{#if user.mustChangePassword}
										<Badge variant="outline" class="text-[10px] px-1.5 py-0 gap-1" title="Must change password at next login">
											<KeyRound class="h-2.5 w-2.5" />
											Reset pending
										</Badge>
									{/if}
								</div>
							</TableCell>
							<TableCell class="text-sm text-muted-foreground py-3 px-3">{user.email || '-'}</TableCell>
							<TableCell class="py-3 px-3">
								{#if user.authProvider && user.authProvider !== 'local'}
									<Badge variant="secondary" class="text-[10px] px-1.5 py-0">{user.authProvider}</Badge>
								{:else}
									<span class="text-sm text-muted-foreground">local</span>
								{/if}
							</TableCell>
							<TableCell class="py-3 px-3">
								<div class="flex gap-1 flex-wrap">
									{#each user.roles as role (role.id)}
										<Badge variant="outline" class="text-xs">{role.name}</Badge>
									{/each}
								</div>
							</TableCell>
							<TableCell class="py-3 px-3">
								<div class="flex items-center gap-1.5">
									<span class="status-dot {user.isActive ? 'status-dot-active' : 'status-dot-inactive'}"></span>
									<span class="text-sm">{user.isActive ? 'Active' : 'Inactive'}</span>
								</div>
							</TableCell>
							<TableCell class="text-sm text-muted-foreground py-3 px-3">
								{#if user.createdAt}{relativeTime(timestampDate(user.createdAt))}{:else}-{/if}
							</TableCell>
							<PermissionGate allowed={canBulkSelect}>
								<TableCell class="text-right py-3 px-3">
									<div class="flex gap-1 justify-end">
										<PermissionGate resource="users" action="update">
											<Button variant="ghost" size="icon" class="h-7 w-7" onclick={() => openEdit(user)}>
												<Pencil class="h-3.5 w-3.5" />
											</Button>
										</PermissionGate>
										<PermissionGate resource="users" action="delete">
											<Button variant="ghost" size="icon" class="h-7 w-7 text-destructive hover:text-destructive" onclick={() => openDelete(user)}>
												<Trash2 class="h-3.5 w-3.5" />
											</Button>
										</PermissionGate>
									</div>
								</TableCell>
							</PermissionGate>
						</TableRow>
					{/each}
				</TableBody>
			</Table>
		</div>

		<DataPagination
			page={pager.page} pageSize={pager.pageSize} totalCount={pager.totalCount}
			onPrev={() => { if (pager.prev()) loadUsers(); }}
			onNext={() => { if (pager.next()) loadUsers(); }}
		/>
	{/if}
</div>

<!-- Bulk Actions -->
<BulkActionBar count={selected.size} onClear={() => selected.clear()}>
	<PermissionGate resource="users" action="update">
		<Button variant="ghost" size="sm" class="h-7" disabled={bulkWorking} onclick={() => bulkSetActive(true)}>
			<UserCheck class="h-3.5 w-3.5 mr-1" />
			Activate
		</Button>
		<Button variant="ghost" size="sm" class="h-7" disabled={bulkWorking} onclick={() => bulkSetActive(false)}>
			<UserX class="h-3.5 w-3.5 mr-1" />
			Deactivate
		</Button>
		<div class="h-4 w-px bg-border"></div>
		<div class="w-44">
			<AsyncSelect
				bind:selected={bulkRoleId}
				disabled={bulkWorking}
				placeholder="Role..."
				searchPlaceholder="Search roles..."
				triggerClass="min-h-7 py-0.5 border-0 shadow-none hover:bg-muted"
				fetchPage={fetchRolePage}
			/>
		</div>
		<Button
			variant="ghost"
			size="icon"
			class="h-7 w-7"
			disabled={bulkWorking || !bulkRoleId}
			title="Add role to selected users"
			onclick={() => bulkRole(true)}
		>
			<Plus class="h-3.5 w-3.5" />
		</Button>
		<Button
			variant="ghost"
			size="icon"
			class="h-7 w-7"
			disabled={bulkWorking || !bulkRoleId}
			title="Remove role from selected users"
			onclick={() => bulkRole(false)}
		>
			<Minus class="h-3.5 w-3.5" />
		</Button>
		<div class="h-4 w-px bg-border"></div>
	</PermissionGate>
	<PermissionGate resource="users" action="delete">
		<Button
			variant="ghost"
			size="sm"
			class="h-7 text-destructive hover:text-destructive"
			disabled={bulkWorking}
			onclick={() => (bulkDeleteDialogOpen = true)}
		>
			<Trash2 class="h-3.5 w-3.5 mr-1" />
			Delete
		</Button>
	</PermissionGate>
</BulkActionBar>

<!-- Create User Panel -->
<FormPanel
	bind:open={createPanelOpen}
	title="Create User"
	description="Provision a local account directly. Share the credentials with the user."
	icon={UserPlus}
>
	<div class="space-y-6">
		<FormSection title="Account">
			<div class="space-y-3">
				<FormField label="Username" id="create-username" help="Lowercase letters, numbers, and . _ - only.">
					<Input id="create-username" bind:value={createUsername} placeholder="jdoe" autocomplete="off" />
				</FormField>
				<FormField label="Display Name" id="create-display-name">
					<Input id="create-display-name" bind:value={createDisplayName} placeholder="Jane Doe" autocomplete="off" />
				</FormField>
				<FormField label="Email" id="create-email">
					<Input id="create-email" bind:value={createEmail} placeholder="user@example.com" autocomplete="off" />
				</FormField>
			</div>
		</FormSection>

		<FormSection title="Credentials">
			<div class="space-y-3">
				<FormField label="Password" id="create-password" help="At least 8 characters. The user signs in with this password.">
					<div class="flex gap-2">
						<div class="flex-1">
							<PasswordInput id="create-password" bind:value={createPassword} placeholder="Set a password" autocomplete="new-password" />
						</div>
						<Button variant="outline" class="shrink-0" onclick={generatePassword} title="Generate a random password">
							<Sparkles class="h-3.5 w-3.5 mr-1.5" />
							Generate
						</Button>
					</div>
					<PasswordStrength password={createPassword} />
				</FormField>

				<FormField label="Require password change" help="Prompt the user to set a new password at first login." horizontal>
					<Switch bind:checked={createMustChange} />
				</FormField>
			</div>
		</FormSection>

		<FormSection title="Roles" description="Leave empty to assign the default role(s).">
			<AsyncSelect
				multiple
				bind:selected={createSelectedRoles}
				placeholder="Select roles..."
				searchPlaceholder="Search roles..."
				fetchPage={fetchRolePage}
			/>
		</FormSection>
	</div>

	{#snippet footer()}
		<Button variant="outline" onclick={() => (createPanelOpen = false)}>Cancel</Button>
		<Button onclick={saveCreate} disabled={createSaving || !createUsername || !createPassword}>
			{createSaving ? 'Creating...' : 'Create User'}
		</Button>
	{/snippet}
</FormPanel>

<!-- Edit User Panel -->
<FormPanel
	bind:open={editPanelOpen}
	title="Edit User"
	description={editUser ? `Manage ${editUser.username}'s account settings, roles, and access.` : ''}
	icon={UserCog}
>
	{#if editUser}
		<div class="space-y-6">
			<!-- User Identity -->
			<div class="flex items-center gap-3 p-4 rounded-xl border border-border/60 bg-muted/20">
				<Avatar class="h-11 w-11">
					<AvatarFallback class="text-sm bg-primary/10 text-primary font-medium">
						{getInitials(editUser)}
					</AvatarFallback>
				</Avatar>
				<div class="min-w-0">
					<p class="font-semibold">{editUser.username}</p>
					<p class="text-[13px] text-muted-foreground">
						{editUser.authProvider === 'local' ? 'Local account' : editUser.authProvider}
						{#if editUser.createdAt}
							&middot; Joined {relativeTime(timestampDate(editUser.createdAt))}
						{/if}
					</p>
				</div>
			</div>

			<!-- Account Settings -->
			<FormSection title="Account Settings">
				<div class="space-y-3">
					<FormField label="Email" id="edit-email" help="The user's contact email address.">
						<Input id="edit-email" bind:value={editEmail} placeholder="user@example.com" />
					</FormField>

					<FormField label="Account Active" help="Inactive users cannot sign in or access the API." horizontal>
						<Switch bind:checked={editActive} />
					</FormField>
				</div>
			</FormSection>

			<!-- Roles -->
			<FormSection title="Roles" description="Assign roles that determine this user's permissions across the system.">
				<AsyncSelect
					multiple
					bind:selected={editSelectedRoles}
					initialSelected={editUser.roles.map((r) => ({ value: r.id, label: r.name }))}
					placeholder="Select roles..."
					searchPlaceholder="Search roles..."
					fetchPage={fetchRolePage}
				/>
			</FormSection>
		</div>
	{/if}

	{#snippet footer()}
		<Button variant="outline" onclick={() => (editPanelOpen = false)}>Cancel</Button>
		<Button onclick={saveEdit} disabled={editSaving}>
			{editSaving ? 'Saving...' : 'Save Changes'}
		</Button>
	{/snippet}
</FormPanel>

<!-- Delete Confirmation -->
<ConfirmDialog
	bind:open={deleteDialogOpen}
	title="Delete User"
	confirmLabel="Delete"
	onConfirm={confirmDelete}
	loading={deleting}
	icon={Trash2}
>
	{#snippet description()}
		Are you sure you want to delete <strong>{deleteUser?.username}</strong>? This will remove all
		their sessions, tokens, and organization memberships.
	{/snippet}
</ConfirmDialog>

<!-- Bulk Delete Confirmation -->
<ConfirmDialog
	bind:open={bulkDeleteDialogOpen}
	title="Delete Users"
	confirmLabel="Delete"
	onConfirm={confirmBulkDelete}
	loading={bulkWorking}
	icon={Trash2}
>
	{#snippet description()}
		Are you sure you want to delete <strong>{selected.size} user{selected.size !== 1 ? 's' : ''}</strong>?
		This will remove all their sessions, tokens, and organization memberships.
	{/snippet}
</ConfirmDialog>
