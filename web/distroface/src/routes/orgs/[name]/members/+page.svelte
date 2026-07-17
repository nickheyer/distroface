<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { onMount, getContext } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
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
	import UserSearch from '$lib/components/user-search.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import { Users, Plus, UserPlus, Search } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { orgRoleLabel, relativeTime, pageToToken } from '$lib/utils';
	import { OrgRole } from '$lib/proto/distroface/v1/types_pb';
	import type { OrgMember } from '$lib/proto/distroface/v1/types_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');

	let members = $state<OrgMember[]>([]);
	let totalCount = $state(0);
	let currentPage = $state(1);
	const pageSize = 20;
	let loading = $state(true);
	let searchQuery = $state('');
	let searchTimeout: ReturnType<typeof setTimeout> | undefined;

	let addMemberOpen = $state(false);
	let addUsername = $state('');
	let addRole = $state<OrgRole>(OrgRole.MEMBER);
	let addingMember = $state(false);

	let removeMemberOpen = $state(false);
	let removeMemberName = $state('');
	let removingMember = $state(false);

	const existingUsernames = $derived(members.map((m) => m.username));

	async function loadMembers() {
		loading = true;
		try {
			const resp = await rpcClient.organization.listOrgMembers({
				orgName,
				search: searchQuery.trim(),
				pageSize,
				pageToken: pageToToken(currentPage, pageSize)
			});
			members = resp.members;
			totalCount = resp.totalCount;
		} catch {
			members = [];
		} finally {
			loading = false;
		}
	}

	function handleSearchInput() {
		clearTimeout(searchTimeout);
		searchTimeout = setTimeout(() => {
			currentPage = 1;
			loadMembers();
		}, 300);
	}

	async function addMember() {
		if (!addUsername.trim()) return;
		addingMember = true;
		try {
			await rpcClient.organization.addOrgMember({
				orgName,
				username: addUsername.trim(),
				role: addRole
			});
			toast.success('Member added');
			addMemberOpen = false;
			addUsername = '';
			addRole = OrgRole.MEMBER;
			await loadMembers();
			await ctx.refresh();
		} catch {
			// error interceptor
		} finally {
			addingMember = false;
		}
	}

	async function changeMemberRole(username: string, role: OrgRole) {
		try {
			await rpcClient.organization.updateOrgMemberRole({ orgName, username, role });
			toast.success('Role updated');
			await loadMembers();
		} catch {
			// error interceptor
		}
	}

	function openRemoveMember(username: string) {
		removeMemberName = username;
		removeMemberOpen = true;
	}

	async function confirmRemoveMember() {
		removingMember = true;
		try {
			await rpcClient.organization.removeOrgMember({ orgName, username: removeMemberName });
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

	<div class="relative max-w-md">
		<Search class="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground/50" />
		<Input
			placeholder="Search members..."
			class="pl-9 h-9 bg-muted/30 border-border/50 focus-visible:bg-background"
			bind:value={searchQuery}
			oninput={handleSearchInput}
		/>
	</div>

	{#if loading}
		<div class="space-y-2">
			{#each { length: 3 }, i (i)}
				<Skeleton class="h-14 w-full rounded-lg" />
			{/each}
		</div>
	{:else if members.length === 0}
		<EmptyState
			icon={Users}
			message={searchQuery ? 'No matching members' : 'No members'}
			description={searchQuery ? 'Try a different search.' : 'Add members to collaborate on this organization.'}
		/>
	{:else}
		<div class="data-table">
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
										onValueChange={(v) => { if (v) changeMemberRole(member.username, Number(v) as OrgRole); }}
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
											onclick={() => openRemoveMember(member.username)}
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
		</div>

		<DataPagination
			page={currentPage} {pageSize} {totalCount}
			onPrev={() => { currentPage--; loadMembers(); }}
			onNext={() => { currentPage++; loadMembers(); }}
		/>
	{/if}
</div>

<FormPanel
	bind:open={addMemberOpen}
	title="Add Member"
	description="Add a user to this organization. Members can push images to the shared namespace."
	icon={UserPlus}
>
	<div class="space-y-6">
		<FormSection title="User">
			<FormField label="Username" id="add-username" required help="Search for an existing user to add.">
				<UserSearch
					bind:value={addUsername}
					excludeUsernames={existingUsernames}
					placeholder="Search for a user..."
				/>
			</FormField>
		</FormSection>

		<FormSection title="Role" description="Admins can manage members and settings. Members can push images.">
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
		<Button onclick={addMember} disabled={addingMember || !addUsername.trim()}>
			{addingMember ? 'Adding...' : 'Add Member'}
		</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={removeMemberOpen} title="Remove Member" confirmLabel="Remove" onConfirm={confirmRemoveMember} loading={removingMember}>
	{#snippet description()}
		Are you sure you want to remove <strong>{removeMemberName}</strong> from this organization?
	{/snippet}
</ConfirmDialog>
