<script lang="ts">
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Input } from '$lib/components/ui/input';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import FormPanel from '$lib/components/form-panel.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import CheckboxGroup from '$lib/components/checkbox-group.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import { Ticket, Plus, Trash2, Clock, Lock, Link } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime, pageToToken } from '$lib/utils';
	import type { RegistrationInvite } from '$lib/proto/distroface/v1/auth_pb';

	let invites = $state<RegistrationInvite[]>([]);
	let loading = $state(true);
	let totalCount = $state(0);
	let currentPage = $state(1);
	const pageSize = 20;

	let createPanelOpen = $state(false);
	let inviteDescription = $state('');
	let inviteSelectedRoles = $state<string[]>(['user']);
	let invitePin = $state('');
	let inviteMaxUses = $state<number | undefined>(undefined);
	let inviteExpiryHours = $state<number | undefined>(undefined);
	let creating = $state(false);

	let deleteDialogOpen = $state(false);
	let deleteInvite = $state<RegistrationInvite | null>(null);
	let deleting = $state(false);

	let availableRoles = $state<{ value: string; label: string }[]>([]);

	async function loadInvites() {
		loading = true;
		try {
			const resp = await rpcClient.auth.listInvites({
				pageSize,
				pageToken: pageToToken(currentPage, pageSize)
			});
			invites = resp.invites;
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

	function resetCreateForm() {
		inviteDescription = '';
		inviteSelectedRoles = ['user'];
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
				roles: inviteSelectedRoles,
				pin: invitePin || undefined,
				maxUses: inviteMaxUses && inviteMaxUses > 0 ? inviteMaxUses : undefined,
				expiresInHours: inviteExpiryHours && inviteExpiryHours > 0 ? inviteExpiryHours : undefined
			});
			toast.success('Invite created');
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
			toast.success('Invite deleted');
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

	onMount(() => { loadInvites(); loadRoles(); });
</script>

<div class="space-y-4">
	<div class="section-header">
		<div>
			<h2 class="section-title">Registration Invites</h2>
			<p class="section-subtitle">Invite links allow users to register when public registration is disabled.</p>
		</div>
		{#if authStore.canCreateSettings}
			<Button size="sm" onclick={() => (createPanelOpen = true)}>
				<Plus class="h-4 w-4 mr-1" />
				Create Invite
			</Button>
		{/if}
	</div>

	{#if loading}
		<div class="space-y-2">
			{#each Array(3) as _}
				<Skeleton class="h-12 w-full rounded-lg" />
			{/each}
		</div>
	{:else if invites.length === 0}
		<EmptyState
			icon={Ticket}
			message="No invites yet"
			description="Create an invite to allow users to register."
		>
			{#snippet actions()}
				{#if authStore.canCreateSettings}
					<Button variant="outline" size="sm" onclick={() => (createPanelOpen = true)}>
						<Plus class="h-4 w-4 mr-1.5" />
						Create Invite
					</Button>
				{/if}
			{/snippet}
		</EmptyState>
	{:else}
		<div class="data-table">
			<Table>
				<TableHeader>
					<TableRow class="bg-muted/30 hover:bg-muted/30">
						<TableHead class="th">Description</TableHead>
						<TableHead class="th">Code</TableHead>
						<TableHead class="th">Roles</TableHead>
						<TableHead class="th">Uses</TableHead>
						<TableHead class="th">Security</TableHead>
						<TableHead class="th">Expires</TableHead>
						{#if authStore.canDeleteSettings}
							<TableHead class="th w-12"></TableHead>
						{/if}
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each invites as invite}
						<TableRow>
							<TableCell class="font-medium py-3 px-3">{invite.description}</TableCell>
							<TableCell class="py-3 px-3">
								<div class="flex items-center gap-1">
									<code class="font-mono text-xs text-muted-foreground bg-muted px-1.5 py-0.5 rounded">{invite.code}</code>
									<CopyButton text={getInviteUrl(invite.code)} label="Invite link copied!" />
								</div>
							</TableCell>
							<TableCell class="py-3 px-3">
								<div class="flex gap-1 flex-wrap">
									{#each invite.roles as role}
										<Badge variant="outline" class="text-xs">{role}</Badge>
									{/each}
								</div>
							</TableCell>
							<TableCell class="text-sm py-3 px-3">
								<span class="tabular-nums">{invite.useCount}{invite.maxUses > 0 ? `/${invite.maxUses}` : ''}</span>
							</TableCell>
							<TableCell class="py-3 px-3">
								{#if invite.hasPin}
									<Badge variant="outline" class="text-xs gap-0.5">
										<Lock class="h-2.5 w-2.5" />PIN
									</Badge>
								{:else}
									<span class="text-xs text-muted-foreground">None</span>
								{/if}
							</TableCell>
							<TableCell class="text-sm text-muted-foreground py-3 px-3">
								{#if invite.expiresAt}
									{@const expires = timestampDate(invite.expiresAt)}
									{@const isExpired = expires < new Date()}
									<Badge variant={isExpired ? 'destructive' : 'outline'} class="text-xs gap-0.5">
										<Clock class="h-2.5 w-2.5" />
										{isExpired ? 'Expired' : relativeTime(expires).replace(' ago', '')}
									</Badge>
								{:else}
									Never
								{/if}
							</TableCell>
							{#if authStore.canDeleteSettings}
								<TableCell class="text-right py-3 px-3">
									<Button variant="ghost" size="icon" class="h-7 w-7 text-destructive hover:text-destructive" onclick={() => openDelete(invite)}>
										<Trash2 class="h-3.5 w-3.5" />
									</Button>
								</TableCell>
							{/if}
						</TableRow>
					{/each}
				</TableBody>
			</Table>
		</div>

		<DataPagination
			page={currentPage} {pageSize} totalCount={totalCount}
			onPrev={() => { currentPage--; loadInvites(); }}
			onNext={() => { currentPage++; loadInvites(); }}
		/>
	{/if}
</div>

<!-- Create Invite Panel -->
<FormPanel
	bind:open={createPanelOpen}
	title="Create Registration Invite"
	description="Generate an invite link that allows new users to register with pre-assigned roles."
	icon={Link}
	wide
>
	<div class="space-y-6">
		<FormSection title="Details">
			<FormField label="Description" id="invite-desc" help="A label to identify this invite (e.g., 'Engineering team Q1')." required>
				<Input id="invite-desc" bind:value={inviteDescription} placeholder="e.g., Team onboarding Q1" />
			</FormField>
		</FormSection>

		<FormSection title="Roles" description="Roles automatically assigned to users who register with this invite.">
			<CheckboxGroup items={availableRoles} bind:selected={inviteSelectedRoles} columns={2} />
		</FormSection>

		<FormSection title="Security & Limits" description="Optional restrictions on how this invite can be used.">
			<div class="space-y-3">
				<FormField label="PIN Protection" id="invite-pin-input" help="Require a numeric PIN in addition to the invite code.">
					<Input id="invite-pin-input" bind:value={invitePin} placeholder="Leave empty for no PIN" />
				</FormField>
				<div class="grid grid-cols-2 gap-3">
					<FormField label="Max Uses" id="invite-max" help="How many times this invite can be used.">
						<Input id="invite-max" type="number" bind:value={inviteMaxUses} placeholder="Unlimited" min={1} />
					</FormField>
					<FormField label="Expires In (hours)" id="invite-expiry" help="Auto-expire after this many hours.">
						<Input id="invite-expiry" type="number" bind:value={inviteExpiryHours} placeholder="Never" min={1} />
					</FormField>
				</div>
			</div>
		</FormSection>
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
