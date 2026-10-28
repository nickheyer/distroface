<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { onMount, getContext } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import { Users, Plus } from '$lib/icons';
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
	const orgName = $derived(page.params.name ?? '');
	const newHref = $derived(resolve('/orgs/[name]/members/new', { name: orgName }));

	let members = $state<OrgMember[]>([]);
	const pager = new Pager(20);
	let loading = $state(true);
	let loaded = $state(false);
	const filter = new QueryFilter([
		{ key: 'username', label: 'Username' },
		{ key: 'role', label: 'Role' }
	]);

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
		<div class="min-w-0">
			<h2 class="section-title">Members</h2>
			<p class="section-subtitle">People who can push to this organization's namespace.</p>
		</div>
		{#if ctx.canAdmin}
			<Button class="shrink-0" href={newHref}>
				<Plus />
				Add member
			</Button>
		{/if}
	</div>

	<div class="max-w-md">
		<QueryFilterBar {filter} placeholder="Search members" onchange={filterChanged} />
	</div>

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 3 }, i (i)}
				<div class="flex items-center gap-3 px-4 py-3">
					<Skeleton class="size-7 rounded-full" />
					<div class="flex-1 space-y-2">
						<Skeleton class="h-4 w-32" />
						<Skeleton class="h-3 w-24" />
					</div>
					<Skeleton class="h-5 w-16 rounded-full" />
				</div>
			{/each}
		</div>
	{:else if members.length === 0}
		<EmptyState
			icon={Users}
			message={filter.active ? 'No matching members' : 'No members'}
			description={filter.active
				? 'Try a different search.'
				: 'Add members to collaborate on this organization.'}
		>
			{#snippet actions()}
				{#if ctx.canAdmin && !filter.active}
					<Button variant="outline" href={newHref}>
						<Plus />
						Add member
					</Button>
				{/if}
			{/snippet}
		</EmptyState>
	{:else}
		<div class="box divide-y transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			{#each members as member (member.userId)}
				<div class="flex items-center gap-3 px-4 py-3">
					<Avatar class="size-7">
						<AvatarFallback>{member.username.slice(0, 2)}</AvatarFallback>
					</Avatar>
					<div class="min-w-0 flex-1">
						<a
							href={resolve('/[namespace]', { namespace: member.username })}
							class="truncate text-sm font-semibold underline-offset-2 hover:text-primary hover:underline"
						>
							{member.username}
						</a>
						<p class="mt-0.5 text-xs text-muted-foreground">
							Joined {member.joinedAt ? relativeTime(timestampDate(member.joinedAt)) : '-'}
						</p>
					</div>
					<div class="flex shrink-0 items-center gap-2">
						{#if ctx.canAdmin && member.role !== OrgRole.OWNER}
							<Select
								type="single"
								value={String(member.role)}
								onValueChange={(v) => {
									if (v) changeMemberRole(member.userId, Number(v) as OrgRole);
								}}
							>
								<SelectTrigger class="w-32" aria-label="Role for {member.username}">
									{orgRoleLabel(member.role)}
								</SelectTrigger>
								<SelectContent>
									<SelectItem value={String(OrgRole.ADMIN)}>Admin</SelectItem>
									<SelectItem value={String(OrgRole.MEMBER)}>Member</SelectItem>
								</SelectContent>
							</Select>
						{:else}
							<Badge variant={member.role === OrgRole.OWNER ? 'default' : 'outline'}>
								{orgRoleLabel(member.role)}
							</Badge>
						{/if}
						{#if ctx.canAdmin && member.role !== OrgRole.OWNER}
							<Button
								variant="ghost"
								size="sm"
								class="text-destructive hover:text-destructive"
								onclick={() => openRemoveMember(member)}
							>
								Remove
							</Button>
						{/if}
					</div>
				</div>
			{/each}
		</div>
		<DataPagination {pager} onChange={loadMembers} />
	{/if}
</div>

<ConfirmDialog
	bind:open={removeMemberOpen}
	title="Remove member"
	confirmLabel="Remove"
	onConfirm={confirmRemoveMember}
	loading={removingMember}
>
	{#snippet description()}
		Remove <strong>{removeMemberName}</strong> from this organization?
	{/snippet}
</ConfirmDialog>
