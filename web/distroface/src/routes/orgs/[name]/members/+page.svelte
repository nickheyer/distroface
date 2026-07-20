<script lang="ts">
	import { resolve } from '$app/paths';
	import { onMount, getContext } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import {
		Select, SelectContent, SelectItem, SelectTrigger
	} from '$lib/components/ui/select';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import { Users, Plus, UserPlus } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { orgRoleLabel, relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { OrgRole } from '$lib/proto/distroface/v1/types_pb';
	import type { OrgMember } from '$lib/proto/distroface/v1/types_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgId = $derived(ctx.org?.id ?? '');

	let members = $state<OrgMember[]>([]);
	const pager = new Pager(20);
	let loading = $state(true);
	let loaded = $state(false);
	const filter = new QueryFilter([
		{ key: 'username', label: 'Username' },
		{ key: 'role', label: 'Role' }
	]);

	let addMemberOpen = $state(false);
	let addUserId = $state('');
	let addRole = $state<OrgRole>(OrgRole.MEMBER);
	let addingMember = $state(false);

	let removeMemberOpen = $state(false);
	let removeMemberId = $state('');
	let removeMemberName = $state('');
	let removingMember = $state(false);

	async function loadMembers() {
		loading = true;
		try {
			const resp = await rpcClient.organization.listOrgMembers({
				page: pager.request(filter.request()),
				orgId
			});
			members = resp.members;
			pager.apply(resp.page);
		} catch {
			members = [];
		} finally {
			loading = false;
			loaded = true;
		}
	}

	function filterChanged() {
		pager.reset();
		loadMembers();
	}

	async function addMember() {
		if (!addUserId) return;
		addingMember = true;
		try {
			await rpcClient.organization.addOrgMember({
				orgId,
				userId: addUserId,
				role: addRole
			});
			toast.success('Member added');
			addMemberOpen = false;
			addUserId = '';
			addRole = OrgRole.MEMBER;
			await loadMembers();
			await ctx.refresh();
		} catch {
			// error interceptor
		} finally {
			addingMember = false;
		}
	}

	async function changeMemberRole(userId: string, role: OrgRole) {
		try {
			await rpcClient.organization.updateOrgMemberRole({ orgId, userId, role });
			toast.success('Role updated');
			await loadMembers();
		} catch {
			// error interceptor
		}
	}

	function openRemoveMember(member: OrgMember) {
		removeMemberId = member.userId;
		removeMemberName = member.username;
		removeMemberOpen = true;
	}

	async function confirmRemoveMember() {
		removingMember = true;
		try {
			await rpcClient.organization.removeOrgMember({ orgId, userId: removeMemberId });
			toast.success('Member removed');
			removeMemberOpen = false;
			await loadMembers();
			await ctx.refresh();
		} catch {
			// error interceptor
		} finally {
			removingMember = false;
		}
	}

	onMount(loadMembers);
</script>

<div class="space-y-4">
	<div class="section-header">
		<h2 class="section-title">Members</h2>
		{#if ctx.canAdmin}
			<Button size="sm" onclick={() => (addMemberOpen = true)}>
				<Plus class="h-4 w-4 mr-1.5" />
				Add Member
			</Button>
		{/if}
	</div>

	<div class="max-w-md">
		<QueryFilterBar {filter} placeholder="Search members..." onchange={filterChanged} />
	</div>

	{#if !loaded}
		<div class="space-y-2">
			{#each { length: 3 }, i (i)}
				<Skeleton class="h-14 w-full rounded-lg" />
			{/each}
		</div>
	{:else if members.length === 0}
		<EmptyState
			icon={Users}
			message={filter.active ? 'No matching members' : 'No members'}
			description={filter.active ? 'Try a different search.' : 'Add members to collaborate on this organization.'}
		/>
	{:else}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table>
				<TableHeader>
					<TableRow class="bg-muted/30 hover:bg-muted/30">
						<TableHead class="th">Username</TableHead>
						<TableHead class="th">Role</TableHead>
						<TableHead class="th">Joined</TableHead>
						{#if ctx.canAdmin}
							<TableHead class="th w-20"></TableHead>
						{/if}
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each members as member (member.userId)}
						<TableRow>
							<TableCell class="font-medium py-3 px-3">
								<a href={resolve('/[username]', { username: member.username })} class="hover:text-primary transition-colors">{member.username}</a>
							</TableCell>
							<TableCell class="py-3 px-3">
								{#if ctx.canAdmin && member.role !== OrgRole.OWNER}
									<Select
										type="single"
										value={String(member.role)}
										onValueChange={(v) => { if (v) changeMemberRole(member.userId, Number(v) as OrgRole); }}
									>
										<SelectTrigger class="w-32 h-8">{orgRoleLabel(member.role)}</SelectTrigger>
										<SelectContent>
											<SelectItem value={String(OrgRole.ADMIN)}>Admin</SelectItem>
											<SelectItem value={String(OrgRole.MEMBER)}>Member</SelectItem>
										</SelectContent>
									</Select>
								{:else}
									<Badge variant={member.role === OrgRole.OWNER ? 'default' : 'outline'} class="text-xs">
										{orgRoleLabel(member.role)}
									</Badge>
								{/if}
							</TableCell>
							<TableCell class="text-muted-foreground text-sm py-3 px-3">
								{member.joinedAt ? relativeTime(timestampDate(member.joinedAt)) : '-'}
							</TableCell>
							{#if ctx.canAdmin}
								<TableCell class="text-right py-3 px-3">
									{#if member.role !== OrgRole.OWNER}
										<Button
											variant="ghost"
											size="sm"
											class="text-destructive hover:text-destructive"
											onclick={() => openRemoveMember(member)}
										>
											Remove
										</Button>
									{/if}
								</TableCell>
							{/if}
						</TableRow>
					{/each}
				</TableBody>
			</Table>
			<DataPagination attached {pager} onChange={loadMembers} />
		</div>
	{/if}
</div>

<FormPanel
	bind:open={addMemberOpen}
	title="Add Member"
	description="Members can push images to the shared namespace."
	icon={UserPlus}
>
	<div class="space-y-6">
		<FormSection title="User">
			<FormField label="User" id="add-user" required>
				<AsyncSelect
					bind:selected={addUserId}
					placeholder="Select a user..."
					searchPlaceholder="Search users..."
					fetchPage={async (query, pageToken) => {
						const resp = await rpcClient.user.listUsers({
							page: { query: { text: query, filters: [] }, pageToken, pageSize: 20 }
						});
						return {
							items: resp.users.map((u) => ({ value: u.id, label: u.username })),
							nextPageToken: resp.page?.nextPageToken ?? ''
						};
					}}
				/>
			</FormField>
		</FormSection>

		<FormSection title="Role" description="Admins manage members and settings, members push images.">
			<Select
				type="single"
				value={String(addRole)}
				onValueChange={(v) => { if (v) addRole = Number(v) as OrgRole; }}
			>
				<SelectTrigger class="w-full">{orgRoleLabel(addRole)}</SelectTrigger>
				<SelectContent>
					<SelectItem value={String(OrgRole.ADMIN)}>Admin</SelectItem>
					<SelectItem value={String(OrgRole.MEMBER)}>Member</SelectItem>
				</SelectContent>
			</Select>
		</FormSection>
	</div>

	{#snippet footer()}
		<Button variant="outline" onclick={() => (addMemberOpen = false)}>Cancel</Button>
		<Button onclick={addMember} disabled={addingMember || !addUserId}>
			{addingMember ? 'Adding...' : 'Add Member'}
		</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={removeMemberOpen} title="Remove Member" confirmLabel="Remove" onConfirm={confirmRemoveMember} loading={removingMember}>
	{#snippet description()}
		Remove <strong>{removeMemberName}</strong> from this organization?
	{/snippet}
</ConfirmDialog>
