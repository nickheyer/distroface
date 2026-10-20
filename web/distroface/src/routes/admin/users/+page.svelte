<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar';
	import {
		Table,
		TableBody,
		TableCell,
		TableHead,
		TableHeader,
		TableRow
	} from '$lib/components/ui/table';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import BulkActionBar from '$lib/components/bulk-action-bar.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import { Plus } from '@lucide/svelte';
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
	const newHref = resolve('/admin/users/new');

	let deleteDialogOpen = $state(false);
	let deleteUser = $state<User | null>(null);
	let deleting = $state(false);

	let bulkRoleId = $state('');

	const selected = new SvelteSet<string>();
	const pageIds = $derived(users.map((u) => u.id));
	const allOnPageSelected = $derived(pageIds.length > 0 && pageIds.every((id) => selected.has(id)));
	const someOnPageSelected = $derived(pageIds.some((id) => selected.has(id)));

	let bulkDeleteDialogOpen = $state(false);
	let bulkWorking = $state(false);

	function getInitials(user: User): string {
		const name = user.displayName || user.username;
		return name
			.split(/[\s-]+/)
			.map((w) => w[0])
			.join('')
			.toUpperCase()
			.slice(0, 2);
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
				: `${errors.length} failed (${who}: ${first.error}, and ${errors.length - 1} more)`
		);
	}

	async function bulkSetActive(active: boolean) {
		bulkWorking = true;
		try {
			const resp = await rpcClient.user.adminBulkUpdateUsers({
				userIds: [...selected],
				isActive: active
			});
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

	function openDelete(user: User) {
		deleteUser = user;
		deleteDialogOpen = true;
	}

	async function confirmDelete() {
		if (!deleteUser) return;
		deleting = true;
		try {
			await rpcClient.user.adminDeleteUser({ userId: deleteUser.id });
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
		if (!authStore.hasPermission('users', 'read')) {
			goto(resolve('/admin'));
			return;
		}
		loadUsers();
	});
</script>

<div class="space-y-4">
	<div class="section-header flex-wrap">
		<div class="min-w-0">
			<h2 class="section-title">Users</h2>
			<p class="section-subtitle">
				{#if pager.totalCount > 0}{pager.totalCount} registered user{pager.totalCount !== 1
						? 's'
						: ''}{:else}Manage user accounts{/if}
			</p>
		</div>
		<div class="flex shrink-0 items-center gap-2">
			<div class="w-96">
				<QueryFilterBar {filter} placeholder="Search users" onchange={filterChanged} />
			</div>
			<PermissionGate resource="users" action="create">
				<Button href={newHref}>
					<Plus />
					Create user
				</Button>
			</PermissionGate>
		</div>
	</div>

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 4 }, i (i)}
				<div class="flex items-center gap-3 px-4 py-3">
					<Skeleton class="size-7 rounded-full" />
					<Skeleton class="h-4 w-32" />
					<Skeleton class="h-4 w-48" />
					<Skeleton class="ml-auto h-4 w-16 rounded-full" />
				</div>
			{/each}
		</div>
	{:else}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table>
				<TableHeader>
					<TableRow>
						<PermissionGate allowed={canBulkSelect}>
							<TableHead class="w-10">
								<Checkbox
									checked={allOnPageSelected}
									indeterminate={someOnPageSelected && !allOnPageSelected}
									onCheckedChange={toggleSelectPage}
									aria-label="Select all on page"
								/>
							</TableHead>
						</PermissionGate>
						<TableHead>User</TableHead>
						<TableHead>Email</TableHead>
						<TableHead>Provider</TableHead>
						<TableHead>Roles</TableHead>
						<TableHead>Status</TableHead>
						<TableHead>Joined</TableHead>
						<PermissionGate allowed={canBulkSelect}>
							<TableHead class="w-28"></TableHead>
						</PermissionGate>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each users as user (user.id)}
						<TableRow data-state={selected.has(user.id) ? 'selected' : undefined}>
							<PermissionGate allowed={canBulkSelect}>
								<TableCell>
									<Checkbox
										checked={selected.has(user.id)}
										onCheckedChange={() => toggleSelect(user.id)}
										aria-label={`Select ${user.username}`}
									/>
								</TableCell>
							</PermissionGate>
							<TableCell>
								<div class="flex items-center gap-2.5">
									<Avatar class="size-7">
										<AvatarFallback>{getInitials(user)}</AvatarFallback>
									</Avatar>
									<a
										href={resolve(`/${user.username}`)}
										class="text-sm font-medium transition-colors hover:text-primary"
									>
										{user.username}
									</a>
									{#if user.mustChangePassword}
										<Badge variant="warning" title="Must change password at next login"
											>Reset pending</Badge
										>
									{/if}
								</div>
							</TableCell>
							<TableCell class="text-sm text-muted-foreground">{user.email || '-'}</TableCell>
							<TableCell>
								{#if user.authProvider && user.authProvider !== 'local'}
									<Badge variant="secondary">{user.authProvider}</Badge>
								{:else}
									<span class="text-sm text-muted-foreground">local</span>
								{/if}
							</TableCell>
							<TableCell>
								<div class="flex flex-wrap gap-1">
									{#each user.roles as role (role.id)}
										<Badge variant="outline">{role.name}</Badge>
									{/each}
								</div>
							</TableCell>
							<TableCell>
								<div class="flex items-center gap-1.5">
									<span
										class="status-dot {user.isActive ? 'status-dot-active' : 'status-dot-inactive'}"
									></span>
									<span class="text-sm">{user.isActive ? 'Active' : 'Inactive'}</span>
								</div>
							</TableCell>
							<TableCell class="text-sm text-muted-foreground">
								{#if user.createdAt}{relativeTime(timestampDate(user.createdAt))}{:else}-{/if}
							</TableCell>
							<PermissionGate allowed={canBulkSelect}>
								<TableCell class="text-right">
									<div class="flex justify-end gap-1">
										<PermissionGate resource="users" action="update">
											<Button
												variant="ghost"
												size="sm"
												href={resolve('/admin/users/[username]', { username: user.username })}
											>
												Edit
											</Button>
										</PermissionGate>
										<PermissionGate resource="users" action="delete">
											<Button
												variant="ghost"
												size="sm"
												class="text-destructive hover:text-destructive"
												onclick={() => openDelete(user)}
											>
												Delete
											</Button>
										</PermissionGate>
									</div>
								</TableCell>
							</PermissionGate>
						</TableRow>
					{/each}
				</TableBody>
			</Table>
			<DataPagination attached {pager} onChange={loadUsers} />
		</div>
	{/if}
</div>

<BulkActionBar count={selected.size} onClear={() => selected.clear()}>
	<PermissionGate resource="users" action="update">
		<Button variant="ghost" size="sm" disabled={bulkWorking} onclick={() => bulkSetActive(true)}>
			Activate
		</Button>
		<Button variant="ghost" size="sm" disabled={bulkWorking} onclick={() => bulkSetActive(false)}>
			Deactivate
		</Button>
		<div class="h-4 w-px bg-border"></div>
		<div class="w-44">
			<AsyncSelect
				bind:selected={bulkRoleId}
				disabled={bulkWorking}
				placeholder="Role"
				searchPlaceholder="Search roles"
				triggerClass="min-h-7 py-0.5 border-0 shadow-none hover:bg-muted"
				fetchPage={fetchRolePage}
			/>
		</div>
		<Button
			variant="ghost"
			size="sm"
			disabled={bulkWorking || !bulkRoleId}
			title="Add role to selected users"
			onclick={() => bulkRole(true)}
		>
			Add
		</Button>
		<Button
			variant="ghost"
			size="sm"
			disabled={bulkWorking || !bulkRoleId}
			title="Remove role from selected users"
			onclick={() => bulkRole(false)}
		>
			Remove
		</Button>
		<div class="h-4 w-px bg-border"></div>
	</PermissionGate>
	<PermissionGate resource="users" action="delete">
		<Button
			variant="ghost"
			size="sm"
			class="text-destructive hover:text-destructive"
			disabled={bulkWorking}
			onclick={() => (bulkDeleteDialogOpen = true)}
		>
			Delete
		</Button>
	</PermissionGate>
</BulkActionBar>

<ConfirmDialog
	bind:open={deleteDialogOpen}
	title="Delete user"
	confirmLabel="Delete"
	onConfirm={confirmDelete}
	loading={deleting}
>
	{#snippet description()}
		Are you sure you want to delete <strong>{deleteUser?.username}</strong>? This will remove all
		their sessions, tokens, and organization memberships.
	{/snippet}
</ConfirmDialog>

<ConfirmDialog
	bind:open={bulkDeleteDialogOpen}
	title="Delete users"
	confirmLabel="Delete"
	onConfirm={confirmBulkDelete}
	loading={bulkWorking}
>
	{#snippet description()}
		Are you sure you want to delete <strong
			>{selected.size} user{selected.size !== 1 ? 's' : ''}</strong
		>? This will remove all their sessions, tokens, and organization memberships.
	{/snippet}
</ConfirmDialog>
