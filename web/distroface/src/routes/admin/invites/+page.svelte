<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Input } from '$lib/components/ui/input';
	import UnitInput from '$lib/components/unit-input.svelte';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import FormPanel from '$lib/components/form-panel.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import BulkActionBar from '$lib/components/bulk-action-bar.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import { Ticket } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import type { RegistrationInvite } from '$lib/proto/distroface/v1/auth_pb';
	import type { BulkOperationError } from '$lib/proto/distroface/v1/types_pb';

	let invites = $state<RegistrationInvite[]>([]);
	let loading = $state(true);
	let loaded = $state(false);
	const pager = new Pager(20);
	const filter = new QueryFilter([
		{ key: 'code', label: 'Code' },
		{ key: 'description', label: 'Description' },
		{ key: 'created_by', label: 'Created By' }
	]);

	let createPanelOpen = $state(false);
	let inviteDescription = $state('');
	let inviteSelectedRoles = $state<string[]>([]);
	let invitePin = $state('');
	let inviteMaxUses = $state<number | undefined>(undefined);
	let inviteExpiryHours = $state<number | undefined>(undefined);
	let creating = $state(false);

	let deleteDialogOpen = $state(false);
	let deleteInvite = $state<RegistrationInvite | null>(null);
	let deleting = $state(false);

	const selected = new SvelteSet<string>();
	const pageIds = $derived(invites.map((i) => i.id));
	const allOnPageSelected = $derived(pageIds.length > 0 && pageIds.every((id) => selected.has(id)));
	const someOnPageSelected = $derived(pageIds.some((id) => selected.has(id)));

	let bulkDeleteDialogOpen = $state(false);
	let bulkWorking = $state(false);

	async function loadInvites() {
		loading = true;
		try {
			const resp = await rpcClient.auth.listInvites({ page: pager.request(filter.request()) });
			invites = resp.invites;
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
		loadInvites();
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
		const lookup = new Map(invites.map((i) => [i.id, i.description]));
		const first = errors[0];
		const who = lookup.get(first.id) ?? first.id;
		toast.error(
			errors.length === 1
				? `${who}: ${first.error}`
				: `${errors.length} failed (${who}: ${first.error}, ...)`
		);
	}

	async function confirmBulkDelete() {
		bulkWorking = true;
		try {
			const resp = await rpcClient.auth.bulkDeleteInvites({ ids: [...selected] });
			reportBulkErrors(resp.errors);
			selected.clear();
			bulkDeleteDialogOpen = false;
			await loadInvites();
		} catch {
			// error interceptor
		} finally {
			bulkWorking = false;
		}
	}

	function resetCreateForm() {
		inviteDescription = '';
		inviteSelectedRoles = [];
		invitePin = '';
		inviteMaxUses = undefined;
		inviteExpiryHours = undefined;
	}

	async function createInvite() {
		if (!inviteDescription.trim()) return;
		creating = true;
		try {
			await rpcClient.auth.createInvite({
				description: inviteDescription.trim(),
				roleIds: inviteSelectedRoles,
				pin: invitePin || undefined,
				maxUses: inviteMaxUses && inviteMaxUses > 0 ? inviteMaxUses : undefined,
				expiresInHours: inviteExpiryHours && inviteExpiryHours > 0 ? inviteExpiryHours : undefined
			});
			createPanelOpen = false;
			resetCreateForm();
			await loadInvites();
		} catch {
			// error interceptor
		} finally {
			creating = false;
		}
	}

	function openDelete(invite: RegistrationInvite) {
		deleteInvite = invite;
		deleteDialogOpen = true;
	}

	async function confirmDelete() {
		if (!deleteInvite) return;
		deleting = true;
		try {
			await rpcClient.auth.deleteInvite({ id: deleteInvite.id });
			deleteDialogOpen = false;
			await loadInvites();
		} catch {
			// error interceptor
		} finally {
			deleting = false;
		}
	}

	function getInviteUrl(code: string): string {
		return `${window.location.origin}/login?invite=${code}`;
	}

	onMount(() => {
		if (!authStore.hasPermission('settings', 'read')) { goto(resolve('/admin')); return; }
		loadInvites();
	});
</script>

<div class="space-y-4">
	<div class="section-header">
		<div>
			<h2 class="section-title">Registration Invites</h2>
			<p class="section-subtitle">Let users register when public registration is off</p>
		</div>
		<div class="flex items-center gap-2">
			<div class="w-96">
				<QueryFilterBar {filter} placeholder="Search invites..." onchange={filterChanged} />
			</div>
			<PermissionGate resource="settings" action="create">
				<Button size="sm" onclick={() => (createPanelOpen = true)}>Create Invite</Button>
			</PermissionGate>
		</div>
	</div>

	{#if !loaded}
		<div class="space-y-2">
			{#each { length: 3 }, i (i)}
				<Skeleton class="h-12 w-full rounded-lg" />
			{/each}
		</div>
	{:else if invites.length === 0}
		<EmptyState
			icon={Ticket}
			message={filter.active ? 'No invites match the current filter' : 'No invites yet'}
		>
			{#snippet actions()}
				<PermissionGate resource="settings" action="create">
					<Button variant="outline" size="sm" onclick={() => (createPanelOpen = true)}>Create Invite</Button>
				</PermissionGate>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table>
				<TableHeader>
					<TableRow class="bg-muted/30 hover:bg-muted/30">
						<PermissionGate resource="settings" action="delete">
							<TableHead class="th w-10">
								<Checkbox
									checked={allOnPageSelected}
									indeterminate={someOnPageSelected && !allOnPageSelected}
									onCheckedChange={toggleSelectPage}
									aria-label="Select all on page"
								/>
							</TableHead>
						</PermissionGate>
						<TableHead class="th">Description</TableHead>
						<TableHead class="th">Code</TableHead>
						<TableHead class="th">Roles</TableHead>
						<TableHead class="th">Uses</TableHead>
						<TableHead class="th">Security</TableHead>
						<TableHead class="th">Expires</TableHead>
						<PermissionGate resource="settings" action="delete">
							<TableHead class="th w-12"></TableHead>
						</PermissionGate>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each invites as invite (invite.id)}
						<TableRow class={selected.has(invite.id) ? 'bg-primary/5 hover:bg-primary/5' : ''}>
							<PermissionGate resource="settings" action="delete">
								<TableCell class="py-3 px-3">
									<Checkbox
										checked={selected.has(invite.id)}
										onCheckedChange={() => toggleSelect(invite.id)}
										aria-label={`Select ${invite.description}`}
									/>
								</TableCell>
							</PermissionGate>
							<TableCell class="font-medium py-3 px-3">{invite.description}</TableCell>
							<TableCell class="py-3 px-3">
								<div class="flex items-center gap-1">
									<code class="font-mono text-xs text-muted-foreground bg-muted px-1.5 py-0.5 rounded">{invite.code}</code>
									<CopyButton text={getInviteUrl(invite.code)} label="Invite link copied!" />
								</div>
							</TableCell>
							<TableCell class="py-3 px-3">
								<div class="flex gap-1 flex-wrap">
									{#each invite.roles as role (role.id)}
										<Badge variant="outline" class="text-xs">{role.name}</Badge>
									{/each}
								</div>
							</TableCell>
							<TableCell class="text-sm py-3 px-3">
								<span class="tabular-nums">{invite.useCount}{invite.maxUses > 0 ? `/${invite.maxUses}` : ''}</span>
							</TableCell>
							<TableCell class="py-3 px-3">
								{#if invite.hasPin}
									<Badge variant="outline" class="text-xs">PIN</Badge>
								{:else}
									<span class="text-xs text-muted-foreground">None</span>
								{/if}
							</TableCell>
							<TableCell class="text-sm text-muted-foreground py-3 px-3">
								{#if invite.expiresAt}
									{@const expires = timestampDate(invite.expiresAt)}
									{@const isExpired = expires < new Date()}
									<Badge variant={isExpired ? 'destructive' : 'outline'} class="text-xs">
										{isExpired ? 'Expired' : relativeTime(expires).replace(' ago', '')}
									</Badge>
								{:else}
									Never
								{/if}
							</TableCell>
							<PermissionGate resource="settings" action="delete">
								<TableCell class="text-right py-3 px-3">
									<Button variant="ghost" size="sm" class="h-7 px-2 text-xs text-destructive hover:text-destructive" onclick={() => openDelete(invite)}>
										Delete
									</Button>
								</TableCell>
							</PermissionGate>
						</TableRow>
					{/each}
				</TableBody>
			</Table>
			<DataPagination attached {pager} onChange={loadInvites} />
		</div>
	{/if}
</div>

<!-- Bulk Actions -->
<BulkActionBar count={selected.size} onClear={() => selected.clear()}>
	<PermissionGate resource="settings" action="delete">
		<Button
			variant="ghost"
			size="sm"
			class="h-7 text-destructive hover:text-destructive"
			disabled={bulkWorking}
			onclick={() => (bulkDeleteDialogOpen = true)}
		>
			Delete
		</Button>
	</PermissionGate>
</BulkActionBar>

<!-- Create Invite Panel -->
<FormPanel bind:open={createPanelOpen} title="Create Registration Invite" wide>
	<div class="space-y-3">
		<FormField label="Description" id="invite-desc" required>
			<Input id="invite-desc" bind:value={inviteDescription} placeholder="Team onboarding Q1" />
		</FormField>
		<FormField label="Roles" help="Granted on registration">
			<AsyncSelect
				multiple
				bind:selected={inviteSelectedRoles}
				placeholder="Select roles..."
				searchPlaceholder="Search roles..."
				fetchPage={async (query, pageToken) => {
					const resp = await rpcClient.role.listRoles({
						page: { query: { text: query, filters: [] }, pageToken, pageSize: 20 }
					});
					return {
						items: resp.roles.map((r) => ({ value: r.id, label: r.name, description: r.description })),
						nextPageToken: resp.page?.nextPageToken ?? ''
					};
				}}
			/>
		</FormField>
		<FormField label="PIN" id="invite-pin-input" help="Also required to register">
			<Input id="invite-pin-input" bind:value={invitePin} placeholder="None" autocomplete="new-password" data-1p-ignore data-lpignore="true" data-bwignore />
		</FormField>
		<div class="grid grid-cols-2 gap-3">
			<FormField label="Max uses" id="invite-max">
				<Input id="invite-max" type="number" bind:value={inviteMaxUses} placeholder="Unlimited" min={1} />
			</FormField>
			<FormField label="Expires" id="invite-expiry">
				<UnitInput id="invite-expiry" unit="hrs" bind:value={inviteExpiryHours} placeholder="Never" min={1} />
			</FormField>
		</div>
	</div>

	{#snippet footer()}
		<Button variant="outline" onclick={() => (createPanelOpen = false)}>Cancel</Button>
		<Button onclick={createInvite} disabled={creating || !inviteDescription.trim()}>
			{creating ? 'Creating...' : 'Create Invite'}
		</Button>
	{/snippet}
</FormPanel>

<!-- Delete Confirmation -->
<ConfirmDialog
	bind:open={deleteDialogOpen}
	title="Delete Invite"
	confirmLabel="Delete"
	onConfirm={confirmDelete}
	loading={deleting}
>
	{#snippet description()}
		Are you sure you want to delete this invite? The link will stop working immediately.
	{/snippet}
</ConfirmDialog>

<!-- Bulk Delete Confirmation -->
<ConfirmDialog
	bind:open={bulkDeleteDialogOpen}
	title="Delete Invites"
	confirmLabel="Delete"
	onConfirm={confirmBulkDelete}
	loading={bulkWorking}
>
	{#snippet description()}
		Are you sure you want to delete <strong>{selected.size} invite{selected.size !== 1 ? 's' : ''}</strong>?
		The links will stop working immediately.
	{/snippet}
</ConfirmDialog>
