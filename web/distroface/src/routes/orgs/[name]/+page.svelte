<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Tabs, TabsContent, TabsList, TabsTrigger } from '$lib/components/ui/tabs';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import {
		Select, SelectContent, SelectItem, SelectTrigger
	} from '$lib/components/ui/select';
	import FormPanel from '$lib/components/form-panel.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import FormCard from '$lib/components/form-card.svelte';
	import WebhookManager from '$lib/components/webhook-manager.svelte';
	import PortalManager from '$lib/components/portal-manager.svelte';
	import ArtifactRepoList from '$lib/components/artifact-repo-list.svelte';
	import OrgSettingsManager from '$lib/components/org-settings-manager.svelte';
	import UserSearch from '$lib/components/user-search.svelte';
	import RepoList from '$lib/components/repo-list.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import {
		Building2, Users, Plus, Pencil, Trash2, UserPlus, Save, ArrowRightLeft
	} from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import PermissionGate from '$lib/components/permission-gate.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { orgRoleLabel, relativeTime, pageToToken } from '$lib/utils';
	import { OrgRole, WebhookScope } from '$lib/proto/distroface/v1/types_pb';
	import type { Organization, OrgMember, Repository } from '$lib/proto/distroface/v1/types_pb';

	const orgName = $derived(page.params.name);

	let org = $state<Organization | null>(null);
	let members = $state<OrgMember[]>([]);
	let membersTotalCount = $state(0);
	let membersPage = $state(1);
	const membersPageSize = 20;
	let repos = $state<Repository[]>([]);
	let reposTotalCount = $state(0);
	let reposPage = $state(1);
	const reposPageSize = 20;
	let loading = $state(true);
	let membersLoading = $state(true);
	let reposLoading = $state(true);

	let editDisplayName = $state('');
	let editDescription = $state('');
	let savingOrg = $state(false);

	let transferOpen = $state(false);
	let transferUsername = $state('');
	let transferring = $state(false);
	let transferCandidates = $state<OrgMember[]>([]);

	let addMemberOpen = $state(false);
	let addUsername = $state('');
	let addRole = $state<OrgRole>(OrgRole.MEMBER);
	let addingMember = $state(false);

	let deleteOrgOpen = $state(false);
	let deletingOrg = $state(false);

	let removeMemberOpen = $state(false);
	let removeMemberName = $state('');
	let removingMember = $state(false);

	const existingUsernames = $derived(members.map((m) => m.username));

	const canUpdateOrg = $derived(
		authStore.hasPermission('organizations', 'update', orgName)
	);

	const canDeleteOrg = $derived(
		authStore.hasPermission('organizations', 'delete', orgName)
	);

	async function loadOrg() {
		loading = true;
		try {
			const resp = await rpcClient.organization.getOrganization({ name: orgName });
			org = resp.organization ?? null;
			if (org) {
				editDisplayName = org.displayName;
				editDescription = org.description;
			}
		} catch {
			// error interceptor
		} finally {
			loading = false;
		}
	}

	async function loadMembers() {
		membersLoading = true;
		try {
			const resp = await rpcClient.organization.listOrgMembers({
				orgName,
				pageSize: membersPageSize,
				pageToken: pageToToken(membersPage, membersPageSize)
			});
			members = resp.members;
			membersTotalCount = resp.totalCount;
		} catch {
			members = [];
		} finally {
			membersLoading = false;
		}
	}

	async function loadRepos() {
		reposLoading = true;
		try {
			const resp = await rpcClient.repository.listRepositories({
				namespace: orgName,
				pageSize: reposPageSize,
				pageToken: pageToToken(reposPage, reposPageSize)
			});
			repos = resp.repositories;
			reposTotalCount = resp.totalCount;
		} catch {
			repos = [];
		} finally {
			reposLoading = false;
		}
	}

	async function saveOrg() {
		savingOrg = true;
		try {
			const resp = await rpcClient.organization.updateOrganization({
				name: orgName,
				displayName: editDisplayName,
				description: editDescription
			});
			org = resp.organization ?? null;
			toast.success('Organization updated');
		} catch {
			// error interceptor
		} finally {
			savingOrg = false;
		}
	}

	async function openTransfer() {
		transferUsername = '';
		transferOpen = true;
		try {
			const resp = await rpcClient.organization.listOrgMembers({ orgName, pageSize: 100 });
			transferCandidates = resp.members.filter((m) => m.username !== authStore.user?.username);
		} catch {
			transferCandidates = [];
		}
	}

	async function confirmTransfer() {
		if (!transferUsername) return;
		transferring = true;
		try {
			await rpcClient.organization.transferOrgOwnership({ orgName, username: transferUsername });
			toast.success(`Ownership transferred to ${transferUsername}`);
			transferOpen = false;
			await loadMembers();
			await loadOrg();
		} catch {
			// error interceptor
		} finally {
			transferring = false;
		}
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
			await loadOrg();
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
			await loadOrg();
		} catch {
			// error interceptor
		} finally {
			removingMember = false;
		}
	}

	async function confirmDeleteOrg() {
		deletingOrg = true;
		try {
			await rpcClient.organization.deleteOrganization({ name: orgName });
			toast.success('Organization deleted');
			goto(resolve('/orgs'));
		} catch {
			// error interceptor
		} finally {
			deletingOrg = false;
		}
	}

	onMount(() => {
		loadOrg();
		loadMembers();
		loadRepos();
	});
</script>

<div class="space-y-6">
	<nav class="flex items-center gap-1.5 text-sm text-muted-foreground">
		<a href={resolve('/orgs')} class="hover:text-foreground transition-colors">Organizations</a>
		<span>/</span>
		<span class="text-foreground font-medium">{orgName}</span>
	</nav>

	{#if loading}
		<div class="flex items-center gap-4">
			<Skeleton class="h-14 w-14 rounded-xl" />
			<div class="space-y-2">
				<Skeleton class="h-7 w-48" />
				<Skeleton class="h-4 w-32" />
			</div>
		</div>
	{:else if org}
		<div class="flex items-start gap-4">
			<div class="h-14 w-14 rounded-xl bg-primary/10 flex items-center justify-center shrink-0">
				<Building2 class="h-7 w-7 text-primary" />
			</div>
			<div class="flex-1 min-w-0 space-y-1">
				<h1 class="text-2xl font-bold tracking-tight">{org.displayName || org.name}</h1>
				<div class="flex items-center gap-3 text-sm text-muted-foreground">
					{#if org.description}
						<span>{org.description}</span>
					{/if}
					<span class="flex items-center gap-1">
						<Users class="h-3.5 w-3.5" />
						{org.memberCount} member{org.memberCount !== 1 ? 's' : ''}
					</span>
				</div>
			</div>
		</div>

		<Tabs value="members">
			<TabsList>
				<TabsTrigger value="members">Members</TabsTrigger>
				<TabsTrigger value="repositories">Repositories</TabsTrigger>
				<TabsTrigger value="artifacts">Artifacts</TabsTrigger>
				{#if canUpdateOrg}
					<TabsTrigger value="webhooks">Webhooks</TabsTrigger>
					<TabsTrigger value="portals">Portals</TabsTrigger>
					<TabsTrigger value="settings">Settings</TabsTrigger>
				{/if}
			</TabsList>

			<TabsContent value="members" class="space-y-4 mt-4">
				<div class="section-header">
					<h2 class="section-title">Members</h2>
					<PermissionGate allowed={canUpdateOrg}>
						<Button size="sm" onclick={() => (addMemberOpen = true)}>
							<Plus class="h-4 w-4 mr-1.5" />
							Add Member
						</Button>
					</PermissionGate>
				</div>

				{#if membersLoading}
					<div class="space-y-2">
						{#each { length: 3 }, i (i)}
							<Skeleton class="h-14 w-full rounded-lg" />
						{/each}
					</div>
				{:else}
					<div class="data-table">
						<Table>
							<TableHeader>
								<TableRow class="bg-muted/30 hover:bg-muted/30">
									<TableHead class="th">Username</TableHead>
									<TableHead class="th">Role</TableHead>
									<TableHead class="th">Joined</TableHead>
									<PermissionGate allowed={canUpdateOrg}>
										<TableHead class="th w-20"></TableHead>
									</PermissionGate>
								</TableRow>
							</TableHeader>
							<TableBody>
								{#each members as member (member.userId)}
									<TableRow>
										<TableCell class="font-medium py-3 px-3">
											<a href={resolve('/[username]', { username: member.username })} class="hover:text-primary transition-colors">{member.username}</a>
										</TableCell>
										<TableCell class="py-3 px-3">
											{#if canUpdateOrg && member.role !== OrgRole.OWNER}
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
										<PermissionGate allowed={canUpdateOrg}>
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
										</PermissionGate>
									</TableRow>
								{/each}
							</TableBody>
						</Table>
					</div>

					<DataPagination
						page={membersPage} pageSize={membersPageSize} totalCount={membersTotalCount}
						onPrev={() => { membersPage--; loadMembers(); }}
						onNext={() => { membersPage++; loadMembers(); }}
					/>
				{/if}
			</TabsContent>

			<TabsContent value="repositories" class="space-y-4 mt-4">
				<h2 class="section-title">Repositories</h2>

				<RepoList
					{repos}
					totalCount={reposTotalCount}
					loading={reposLoading}
					page={reposPage}
					pageSize={reposPageSize}
					onPageChange={(newPage) => { reposPage = newPage; loadRepos(); }}
					emptyMessage="No repositories yet"
					emptyDescription="Push images to this organization's namespace to create repositories."
				/>
			</TabsContent>

			<TabsContent value="artifacts" class="space-y-4 mt-4">
				<ArtifactRepoList namespace={orgName ?? ''} canCreate={canUpdateOrg} />
			</TabsContent>

			{#if org && canUpdateOrg}
				<TabsContent value="webhooks" class="space-y-4 mt-4">
					<WebhookManager
						scope={WebhookScope.ORGANIZATION}
						scopeId={org.id}
						emptyDescription="Add a webhook to get notified when images are pushed, pulled, or deleted in any repository under this organization."
						createDescription="Receive HTTP POST notifications for all repositories in this organization."
					/>
				</TabsContent>
			{/if}

			{#if org && canUpdateOrg}
				<TabsContent value="portals" class="space-y-4 mt-4">
					<PortalManager orgName={org.name} />
				</TabsContent>
			{/if}

			{#if org && canUpdateOrg}
				<TabsContent value="settings" class="space-y-4 mt-4">
					<h2 class="section-title">Organization Settings</h2>

					<FormCard title="Details" description="Public name and description for this organization." icon={Pencil}>
						<div class="space-y-3">
							<FormField label="Display Name" id="edit-org-display" help="The public name shown in the UI.">
								<Input id="edit-org-display" bind:value={editDisplayName} placeholder="Display name" />
							</FormField>
							<FormField label="Description" id="edit-org-desc" help="Tell people what this organization is about.">
								<Textarea id="edit-org-desc" bind:value={editDescription} placeholder="What does this organization do?" rows={3} />
							</FormField>
						</div>
						{#snippet footer()}
							<Button onclick={saveOrg} disabled={savingOrg} class="gap-2">
								<Save class="h-4 w-4" />
								{savingOrg ? 'Saving...' : 'Save Changes'}
							</Button>
						{/snippet}
					</FormCard>

					<OrgSettingsManager orgName={org.name} />

					<div class="rounded-xl border border-destructive/40 overflow-hidden">
						<div class="px-6 py-4 border-b border-destructive/30 bg-destructive/5">
							<h3 class="text-sm font-semibold text-destructive">Danger Zone</h3>
						</div>
						<div class="divide-y divide-border/40">
							<div class="flex items-center justify-between gap-4 px-6 py-4">
								<div class="min-w-0">
									<p class="text-sm font-medium">Transfer ownership</p>
									<p class="text-[13px] text-muted-foreground mt-0.5">
										Make another member the owner of this organization. You will be demoted to admin.
									</p>
								</div>
								<Button
									variant="outline"
									class="shrink-0 text-destructive border-destructive/40 hover:bg-destructive/10 hover:text-destructive"
									onclick={openTransfer}
								>
									<ArrowRightLeft class="h-4 w-4 mr-1.5" />
									Transfer
								</Button>
							</div>
							{#if canDeleteOrg}
								<div class="flex items-center justify-between gap-4 px-6 py-4">
									<div class="min-w-0">
										<p class="text-sm font-medium">Delete this organization</p>
										<p class="text-[13px] text-muted-foreground mt-0.5">
											All repositories under this namespace will be deleted. This cannot be undone.
										</p>
									</div>
									<Button
										variant="outline"
										class="shrink-0 text-destructive border-destructive/40 hover:bg-destructive/10 hover:text-destructive"
										onclick={() => (deleteOrgOpen = true)}
									>
										<Trash2 class="h-4 w-4 mr-1.5" />
										Delete
									</Button>
								</div>
							{/if}
						</div>
					</div>
				</TabsContent>
			{/if}
		</Tabs>
	{:else}
		<div class="text-center py-12">
			<div class="h-12 w-12 rounded-xl bg-muted/50 flex items-center justify-center mx-auto mb-4">
				<Building2 class="h-6 w-6 text-muted-foreground/50" />
			</div>
			<h2 class="text-lg font-semibold">Organization not found</h2>
			<p class="text-[13px] text-muted-foreground mt-1">
				{orgName} does not exist or you don't have access.
			</p>
			<Button variant="outline" class="mt-4" onclick={() => goto(resolve('/orgs'))}>
				Back to Organizations
			</Button>
		</div>
	{/if}
</div>

<!-- Transfer Ownership Panel -->
<FormPanel
	bind:open={transferOpen}
	title="Transfer Ownership"
	description="Hand this organization to another member. You will be demoted to admin."
	icon={ArrowRightLeft}
>
	<div class="space-y-6">
		<FormSection title="New Owner">
			<FormField label="Member" id="transfer-member" required help="Only current members can receive ownership.">
				<Select
					type="single"
					value={transferUsername}
					onValueChange={(v) => { if (v) transferUsername = v; }}
				>
					<SelectTrigger id="transfer-member" class="w-full">
						{transferUsername || 'Select a member...'}
					</SelectTrigger>
					<SelectContent>
						{#each transferCandidates as m (m.userId)}
							<SelectItem value={m.username}>{m.username} ({orgRoleLabel(m.role)})</SelectItem>
						{/each}
					</SelectContent>
				</Select>
			</FormField>
		</FormSection>
	</div>

	{#snippet footer()}
		<Button variant="outline" onclick={() => (transferOpen = false)}>Cancel</Button>
		<Button variant="destructive" onclick={confirmTransfer} disabled={transferring || !transferUsername}>
			{transferring ? 'Transferring...' : 'Transfer Ownership'}
		</Button>
	{/snippet}
</FormPanel>

<!-- Add Member Panel -->
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

<!-- Remove Member -->
<ConfirmDialog bind:open={removeMemberOpen} title="Remove Member" confirmLabel="Remove" onConfirm={confirmRemoveMember} loading={removingMember}>
	{#snippet description()}
		Are you sure you want to remove <strong>{removeMemberName}</strong> from this organization?
	{/snippet}
</ConfirmDialog>

<!-- Delete Org -->
<ConfirmDialog bind:open={deleteOrgOpen} title="Delete Organization" confirmLabel="Delete" onConfirm={confirmDeleteOrg} loading={deletingOrg} icon={Trash2}>
	{#snippet description()}
		Are you sure you want to delete <strong>{orgName}</strong>? All repositories under this
		namespace will also be deleted. This action cannot be undone.
	{/snippet}
</ConfirmDialog>

