<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import {
		Table,
		TableBody,
		TableCell,
		TableHead,
		TableHeader,
		TableRow
	} from '$lib/components/ui/table';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import BulkActionBar from '$lib/components/bulk-action-bar.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import { Plus, Ticket } from '$lib/icons';
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
	const newHref = resolve('/admin/invites/new');

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
				: `${errors.length} failed (${who}: ${first.error}, and ${errors.length - 1} more)`
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
		if (!authStore.hasPermission('settings', 'read')) {
			goto(resolve('/admin'));
			return;
		}
		loadInvites();
	});
</script>

<div class="space-y-4">
	<div class="section-header flex-wrap">
		<div class="min-w-0">
			<h2 class="section-title">Registration invites</h2>
			<p class="section-subtitle">Let users register when public registration is off</p>
		</div>
		<div class="flex shrink-0 items-center gap-2">
			<div class="w-96">
				<QueryFilterBar {filter} placeholder="Search invites" onchange={filterChanged} />
			</div>
			<PermissionGate resource="settings" action="create">
				<Button href={newHref}>
					<Plus />
					Create invite
				</Button>
			</PermissionGate>
		</div>
	</div>

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 3 }, i (i)}
				<div class="flex items-center gap-4 px-4 py-3">
					<Skeleton class="h-4 w-40" />
					<Skeleton class="h-4 w-28" />
					<Skeleton class="h-4 w-16 rounded-full" />
					<Skeleton class="ml-auto h-4 w-12" />
				</div>
			{/each}
		</div>
	{:else if invites.length === 0}
		<EmptyState
			icon={Ticket}
			message={filter.active ? 'No matching invites' : 'No invites yet'}
			description={filter.active
				? 'No results match the current filter.'
				: 'Create an invite to let someone register.'}
		>
			{#snippet actions()}
				<PermissionGate resource="settings" action="create">
					<Button variant="outline" href={newHref}>
						<Plus />
						Create invite
					</Button>
				</PermissionGate>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table>
				<TableHeader>
					<TableRow>
						<PermissionGate resource="settings" action="delete">
							<TableHead class="w-10">
								<Checkbox
									checked={allOnPageSelected}
									indeterminate={someOnPageSelected && !allOnPageSelected}
									onCheckedChange={toggleSelectPage}
									aria-label="Select all on page"
								/>
							</TableHead>
						</PermissionGate>
						<TableHead>Description</TableHead>
						<TableHead>Code</TableHead>
						<TableHead>Roles</TableHead>
						<TableHead>Uses</TableHead>
						<TableHead>Security</TableHead>
						<TableHead>Expires</TableHead>
						<PermissionGate resource="settings" action="delete">
							<TableHead class="w-12"></TableHead>
						</PermissionGate>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each invites as invite (invite.id)}
						<TableRow data-state={selected.has(invite.id) ? 'selected' : undefined}>
							<PermissionGate resource="settings" action="delete">
								<TableCell>
									<Checkbox
										checked={selected.has(invite.id)}
										onCheckedChange={() => toggleSelect(invite.id)}
										aria-label={`Select ${invite.description}`}
									/>
								</TableCell>
							</PermissionGate>
							<TableCell class="font-medium">{invite.description}</TableCell>
							<TableCell>
								<div class="flex items-center gap-1">
									<code
										class="rounded-md bg-muted px-1.5 py-0.5 font-mono text-xs text-muted-foreground"
										>{invite.code}</code
									>
									<CopyButton text={getInviteUrl(invite.code)} label="Invite link copied" />
								</div>
							</TableCell>
							<TableCell>
								<div class="flex flex-wrap gap-1">
									{#each invite.roles as role (role.id)}
										<Badge variant="outline">{role.name}</Badge>
									{/each}
								</div>
							</TableCell>
							<TableCell class="text-sm">
								<span class="tabular-nums"
									>{invite.useCount}{invite.maxUses > 0 ? `/${invite.maxUses}` : ''}</span
								>
							</TableCell>
							<TableCell>
								{#if invite.hasPin}
									<Badge variant="outline">PIN</Badge>
								{:else}
									<span class="text-xs text-muted-foreground">None</span>
								{/if}
							</TableCell>
							<TableCell class="text-sm text-muted-foreground">
								{#if invite.expiresAt}
									{@const expires = timestampDate(invite.expiresAt)}
									{@const isExpired = expires < new Date()}
									<Badge variant={isExpired ? 'destructive' : 'outline'}>
										{isExpired ? 'Expired' : relativeTime(expires).replace(' ago', '')}
									</Badge>
								{:else}
									Never
								{/if}
							</TableCell>
							<PermissionGate resource="settings" action="delete">
								<TableCell class="text-right">
									<Button
										variant="ghost"
										size="sm"
										class="text-destructive hover:text-destructive"
										onclick={() => openDelete(invite)}
									>
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

<BulkActionBar count={selected.size} onClear={() => selected.clear()}>
	<PermissionGate resource="settings" action="delete">
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
	title="Delete invite"
	confirmLabel="Delete"
	onConfirm={confirmDelete}
	loading={deleting}
>
	{#snippet description()}
		Are you sure you want to delete this invite? The link will stop working immediately.
	{/snippet}
</ConfirmDialog>

<ConfirmDialog
	bind:open={bulkDeleteDialogOpen}
	title="Delete invites"
	confirmLabel="Delete"
	onConfirm={confirmBulkDelete}
	loading={bulkWorking}
>
	{#snippet description()}
		Are you sure you want to delete <strong
			>{selected.size} invite{selected.size !== 1 ? 's' : ''}</strong
		>? The links will stop working immediately.
	{/snippet}
</ConfirmDialog>
