<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import { Input } from '$lib/components/ui/input';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import FormPanel from '$lib/components/form-panel.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import CheckboxGroup from '$lib/components/checkbox-group.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import { Pencil, Trash2, Search, UserCog } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { pageToToken, relativeTime } from '$lib/utils';
	import type { User } from '$lib/proto/distroface/v1/types_pb';

	let users = $state<User[]>([]);
	let loading = $state(true);
	let totalCount = $state(0);
	let currentPage = $state(1);
	const pageSize = 20;
	let searchQuery = $state('');
	let searchTimeout: ReturnType<typeof setTimeout> | undefined;

	let editPanelOpen = $state(false);
	let editUser = $state<User | null>(null);
	let editSelectedRoles = $state<string[]>([]);
	let editEmail = $state('');
	let editActive = $state(true);
	let editSaving = $state(false);

	let deleteDialogOpen = $state(false);
	let deleteUser = $state<User | null>(null);
	let deleting = $state(false);

	let availableRoles = $state<{ value: string; label: string }[]>([]);

	function getInitials(user: User): string {
		const name = user.displayName || user.username;
		return name.split(/[\s-]+/).map((w) => w[0]).join('').toUpperCase().slice(0, 2);
	}

	async function loadUsers() {
		loading = true;
		try {
			const resp = await rpcClient.user.listUsers({
				pageSize,
				pageToken: pageToToken(currentPage, pageSize),
				query: searchQuery
			});
			users = resp.users;
			totalCount = resp.totalCount;
		} catch {
			// error interceptor
		} finally {
			loading = false;
		}
	}

	async function loadRoles() {
		try {
			const resp = await rpcClient.role.listRoles({});
			availableRoles = resp.roles.map((r) => ({ value: r.name, label: r.name }));
		} catch {
			// non-critical
		}
	}

	function handleSearch() {
		clearTimeout(searchTimeout);
		searchTimeout = setTimeout(() => { currentPage = 1; loadUsers(); }, 300);
	}

	function openEdit(user: User) {
		editUser = user;
		editSelectedRoles = [...user.roles];
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
				roles: editSelectedRoles
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

	onMount(() => {
		if (!authStore.hasPermission('users', 'read')) { goto(resolve('/admin')); return; }
		loadUsers(); loadRoles();
	});
</script>

<div class="space-y-4">
	<div class="section-header">
		<div>
			<h2 class="section-title">Users</h2>
			<p class="section-subtitle">
				{#if totalCount > 0}{totalCount} registered user{totalCount !== 1 ? 's' : ''}{:else}Manage user accounts{/if}
			</p>
		</div>
		<div class="relative w-64">
			<Search class="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
			<Input placeholder="Search users..." class="pl-9 h-9" bind:value={searchQuery} oninput={handleSearch} />
		</div>
	</div>

	{#if loading}
		<div class="space-y-2">
			{#each { length: 4 }, i (i)}
				<Skeleton class="h-12 w-full rounded-lg" />
			{/each}
		</div>
	{:else}
		<div class="data-table">
			<Table>
				<TableHeader>
					<TableRow class="bg-muted/30 hover:bg-muted/30">
						<TableHead class="th">User</TableHead>
						<TableHead class="th">Email</TableHead>
						<TableHead class="th">Provider</TableHead>
						<TableHead class="th">Roles</TableHead>
						<TableHead class="th">Status</TableHead>
						<TableHead class="th">Joined</TableHead>
<PermissionGate allowed={authStore.canUpdateUsers || authStore.canDeleteUsers}>
							<TableHead class="th w-20"></TableHead>
						</PermissionGate>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each users as user (user.id)}
						<TableRow>
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
									{#each user.roles as role (role)}
										<Badge variant="outline" class="text-xs">{role}</Badge>
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
							<PermissionGate allowed={authStore.canUpdateUsers || authStore.canDeleteUsers}>
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
			page={currentPage} {pageSize} totalCount={totalCount}
			onPrev={() => { currentPage--; loadUsers(); }}
			onNext={() => { currentPage++; loadUsers(); }}
		/>
	{/if}
</div>

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
				<CheckboxGroup items={availableRoles} bind:selected={editSelectedRoles} columns={2} />
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
