<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount, getContext } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Switch } from '$lib/components/ui/switch';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import * as Tabs from '$lib/components/ui/tabs';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import FormCard from '$lib/components/form-card.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import TeamPermissionGrid, {
		toUnitLevels, fromUnitLevels, type UnitLevels
	} from '$lib/components/git/team-permission-grid.svelte';
	import {
		UsersRound, Users, GitBranch, Plus, UserPlus, Trash2, Save, ShieldCheck, ArrowLeft
	} from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { orgRoleLabel, relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import type { Team, TeamRepo } from '$lib/proto/distroface/v1/organization_pb';
	import type { OrgMember } from '$lib/proto/distroface/v1/types_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgId = $derived(ctx.org?.id ?? '');
	const orgName = $derived(page.params.name ?? '');
	const teamId = $derived(page.params.team ?? '');

	let team = $state<Team | null>(null);
	let loading = $state(true);
	let tab = $state('members');

	let members = $state<OrgMember[]>([]);
	const memberPager = new Pager(20);
	let membersLoaded = $state(false);
	let addMemberOpen = $state(false);
	let addUserId = $state('');
	let addingMember = $state(false);
	let removeMember = $state<OrgMember | null>(null);
	let removeMemberOpen = $state(false);
	let removingMember = $state(false);

	let repos = $state<TeamRepo[]>([]);
	const repoPager = new Pager(20);
	let reposLoaded = $state(false);
	let addRepoOpen = $state(false);
	let addRepoName = $state('');
	let addingRepo = $state(false);
	let removeRepo = $state<TeamRepo | null>(null);
	let removeRepoOpen = $state(false);
	let removingRepo = $state(false);

	let editName = $state('');
	let editDescription = $state('');
	let editIncludesAll = $state(false);
	let editCanCreate = $state(false);
	let editAdmin = $state(false);
	let editUnits = $state<UnitLevels>({});
	let saving = $state(false);

	let deleteOpen = $state(false);
	let deleting = $state(false);

	function seed(t: Team) {
		editName = t.name;
		editDescription = t.description;
		editIncludesAll = t.includesAllRepos;
		editCanCreate = t.canCreateRepos;
		editAdmin = t.adminAccess;
		editUnits = toUnitLevels(t.units);
	}

	async function loadTeam() {
		loading = true;
		try {
			const resp = await rpcClient.organization.getTeam({ orgId, id: teamId });
			team = resp.team ?? null;
			if (team) seed(team);
		} catch {
			team = null;
		} finally {
			loading = false;
		}
	}

	async function loadMembers() {
		try {
			const resp = await rpcClient.organization.listTeamMembers({
				page: memberPager.request(),
				orgId,
				teamId
			});
			members = resp.members;
			memberPager.apply(resp.page);
		} catch {
			members = [];
		} finally {
			membersLoaded = true;
		}
	}

	async function loadRepos() {
		try {
			const resp = await rpcClient.organization.listTeamRepos({
				page: repoPager.request(),
				orgId,
				teamId
			});
			repos = resp.repos;
			repoPager.apply(resp.page);
		} catch {
			repos = [];
		} finally {
			reposLoaded = true;
		}
	}

	async function addMember() {
		if (!addUserId) return;
		addingMember = true;
		try {
			await rpcClient.organization.addTeamMember({ orgId, teamId, userId: addUserId });
			toast.success('Member added');
			addMemberOpen = false;
			addUserId = '';
			await Promise.all([loadMembers(), loadTeam()]);
		} catch {
			// error interceptor
		} finally {
			addingMember = false;
		}
	}

	async function confirmRemoveMember() {
		if (!removeMember) return;
		removingMember = true;
		try {
			await rpcClient.organization.removeTeamMember({ orgId, teamId, userId: removeMember.userId });
			toast.success('Member removed');
			removeMemberOpen = false;
			await Promise.all([loadMembers(), loadTeam()]);
		} catch {
			// error interceptor
		} finally {
			removingMember = false;
		}
	}

	async function addRepo() {
		if (!addRepoName) return;
		addingRepo = true;
		try {
			await rpcClient.organization.addTeamRepo({ orgId, teamId, repoName: addRepoName });
			toast.success('Repository added');
			addRepoOpen = false;
			addRepoName = '';
			await Promise.all([loadRepos(), loadTeam()]);
		} catch {
			// error interceptor
		} finally {
			addingRepo = false;
		}
	}

	async function confirmRemoveRepo() {
		if (!removeRepo) return;
		removingRepo = true;
		try {
			await rpcClient.organization.removeTeamRepo({ orgId, teamId, repoName: removeRepo.name });
			toast.success('Repository removed');
			removeRepoOpen = false;
			await Promise.all([loadRepos(), loadTeam()]);
		} catch {
			// error interceptor
		} finally {
			removingRepo = false;
		}
	}

	async function savePermissions() {
		if (!editName.trim()) return;
		saving = true;
		try {
			const resp = await rpcClient.organization.updateTeam({
				orgId,
				id: teamId,
				name: editName.trim(),
				description: editDescription.trim(),
				canCreateRepos: editCanCreate,
				includesAllRepos: editIncludesAll,
				adminAccess: editAdmin,
				setUnits: true,
				units: fromUnitLevels(editUnits)
			});
			toast.success('Team updated');
			if (resp.team) {
				team = resp.team;
				seed(resp.team);
			}
		} catch {
			// error interceptor
		} finally {
			saving = false;
		}
	}

	async function confirmDelete() {
		deleting = true;
		try {
			await rpcClient.organization.deleteTeam({ orgId, id: teamId });
			toast.success('Team deleted');
			goto(resolve('/orgs/[name]/teams', { name: orgName }));
		} catch {
			// error interceptor
		} finally {
			deleting = false;
		}
	}

	onMount(async () => {
		await loadTeam();
		await Promise.all([loadMembers(), loadRepos()]);
	});
</script>

<div class="space-y-5">
	<a
		href={resolve('/orgs/[name]/teams', { name: orgName })}
		class="inline-flex items-center gap-1.5 text-sm text-muted-foreground hover:text-foreground transition-colors"
	>
		<ArrowLeft class="h-3.5 w-3.5" />
		Teams
	</a>

	{#if loading}
		<Skeleton class="h-10 w-64 rounded-lg" />
		<Skeleton class="h-64 w-full rounded-xl" />
	{:else if !team}
		<EmptyState icon={UsersRound} message="Team not found" description="It may have been deleted." />
	{:else}
		<div class="flex items-start gap-4">
			<div class="h-12 w-12 rounded-xl bg-primary/10 flex items-center justify-center shrink-0">
				<UsersRound class="h-6 w-6 text-primary" />
			</div>
			<div class="min-w-0 flex-1">
				<div class="flex items-center gap-2 flex-wrap">
					<h2 class="text-xl font-semibold tracking-tight">{team.name}</h2>
					{#if team.isDefault}
						<Badge class="text-[11px]">Default</Badge>
					{/if}
					{#if team.adminAccess}
						<Badge variant="secondary" class="text-[11px]">Admin</Badge>
					{/if}
				</div>
				<p class="text-[13px] text-muted-foreground mt-0.5">
					{team.description || 'No description'}
				</p>
				<div class="flex flex-wrap items-center gap-x-4 gap-y-1 text-[13px] text-muted-foreground mt-2">
					<span class="inline-flex items-center gap-1.5">
						<Users class="h-3.5 w-3.5" />
						{team.isDefault ? 'Every member' : `${team.memberCount} member${team.memberCount !== 1 ? 's' : ''}`}
					</span>
					<span class="inline-flex items-center gap-1.5">
						<GitBranch class="h-3.5 w-3.5" />
						{team.includesAllRepos ? 'All repositories' : `${team.repoCount} repositor${team.repoCount !== 1 ? 'ies' : 'y'}`}
					</span>
				</div>
			</div>
		</div>

		<Tabs.Root bind:value={tab}>
			<Tabs.List>
				<Tabs.Trigger value="members">Members</Tabs.Trigger>
				<Tabs.Trigger value="repos">Git repositories</Tabs.Trigger>
				{#if ctx.canAdmin}
					<Tabs.Trigger value="permissions">Permissions</Tabs.Trigger>
				{/if}
			</Tabs.List>

			<Tabs.Content value="members" class="pt-3 space-y-4">
				{#if team.isDefault}
					<p class="text-[13px] text-muted-foreground rounded-lg border border-border/60 bg-muted/20 px-4 py-3">
						Every organization member belongs to the default team. Manage membership from the
						<a href={resolve('/orgs/[name]/members', { name: orgName })} class="text-primary hover:underline">Members</a> page.
					</p>
				{:else}
					{#if ctx.canAdmin}
						<div class="flex justify-end">
							<Button size="sm" onclick={() => (addMemberOpen = true)}>
								<Plus class="h-4 w-4 mr-1.5" />
								Add Member
							</Button>
						</div>
					{/if}
					{#if !membersLoaded}
						<Skeleton class="h-32 w-full rounded-lg" />
					{:else if members.length === 0}
						<EmptyState icon={Users} message="No members" description="Add organization members to this team." />
					{:else}
						<div class="data-table">
							<Table>
								<TableHeader>
									<TableRow class="bg-muted/30 hover:bg-muted/30">
										<TableHead class="th">Username</TableHead>
										<TableHead class="th">Org role</TableHead>
										<TableHead class="th hidden sm:table-cell">Joined</TableHead>
										{#if ctx.canAdmin}
											<TableHead class="th w-20"></TableHead>
										{/if}
									</TableRow>
								</TableHeader>
								<TableBody>
									{#each members as member (member.userId)}
										<TableRow>
											<TableCell class="font-medium py-3 px-3">
												<a href={resolve('/[namespace]', { namespace: member.username })} class="hover:text-primary transition-colors">{member.username}</a>
											</TableCell>
											<TableCell class="py-3 px-3">
												<Badge variant="outline" class="text-xs">{orgRoleLabel(member.role)}</Badge>
											</TableCell>
											<TableCell class="text-muted-foreground text-sm py-3 px-3 hidden sm:table-cell">
												{member.joinedAt ? relativeTime(timestampDate(member.joinedAt)) : '-'}
											</TableCell>
											{#if ctx.canAdmin}
												<TableCell class="text-right py-3 px-3">
													<Button
														variant="ghost"
														size="sm"
														class="h-8 text-destructive hover:text-destructive"
														onclick={() => { removeMember = member; removeMemberOpen = true; }}
													>
														Remove
													</Button>
												</TableCell>
											{/if}
										</TableRow>
									{/each}
								</TableBody>
							</Table>
							<DataPagination attached pager={memberPager} onChange={loadMembers} />
						</div>
					{/if}
				{/if}
			</Tabs.Content>

			<Tabs.Content value="repos" class="pt-3 space-y-4">
				{#if team.includesAllRepos}
					<p class="text-[13px] text-muted-foreground rounded-lg border border-border/60 bg-muted/20 px-4 py-3">
						This team includes every repository in the organization.
					</p>
				{:else}
					{#if ctx.canAdmin}
						<div class="flex justify-end">
							<Button size="sm" onclick={() => (addRepoOpen = true)}>
								<Plus class="h-4 w-4 mr-1.5" />
								Add Repository
							</Button>
						</div>
					{/if}
					{#if !reposLoaded}
						<Skeleton class="h-32 w-full rounded-lg" />
					{:else if repos.length === 0}
						<EmptyState icon={GitBranch} message="No repositories" description="Add repositories this team can access." />
					{:else}
						<div class="data-table">
							<Table>
								<TableHeader>
									<TableRow class="bg-muted/30 hover:bg-muted/30">
										<TableHead class="th">Git repository</TableHead>
										{#if ctx.canAdmin}
											<TableHead class="th w-20"></TableHead>
										{/if}
									</TableRow>
								</TableHeader>
								<TableBody>
									{#each repos as repo (repo.repoId)}
										<TableRow>
											<TableCell class="font-medium py-3 px-3">
												<a href={resolve('/[namespace]/[name]', { namespace: repo.namespace, name: repo.name })} class="hover:text-primary transition-colors">
													{repo.namespace}/{repo.name}
												</a>
											</TableCell>
											{#if ctx.canAdmin}
												<TableCell class="text-right py-3 px-3">
													<Button
														variant="ghost"
														size="sm"
														class="h-8 text-destructive hover:text-destructive"
														onclick={() => { removeRepo = repo; removeRepoOpen = true; }}
													>
														Remove
													</Button>
												</TableCell>
											{/if}
										</TableRow>
									{/each}
								</TableBody>
							</Table>
							<DataPagination attached pager={repoPager} onChange={loadRepos} />
						</div>
					{/if}
				{/if}
			</Tabs.Content>

			{#if ctx.canAdmin}
				<Tabs.Content value="permissions" class="pt-3 space-y-4">
					<FormCard title="Team" description="Name, scope, repository flags, and unit levels." icon={ShieldCheck}>
						<div class="space-y-6">
							<FormSection title="Details">
								<div class="space-y-3">
									<FormField label="Name" id="edit-team-name" required>
										<Input id="edit-team-name" bind:value={editName} />
									</FormField>
									<FormField label="Description" id="edit-team-desc">
										<Textarea id="edit-team-desc" bind:value={editDescription} rows={2} />
									</FormField>
								</div>
							</FormSection>
							<FormSection title="Git repositories">
								<div class="space-y-2">
									<FormField label="Includes all repositories" horizontal help="Otherwise repositories are added one by one">
										<Switch bind:checked={editIncludesAll} />
									</FormField>
									<FormField label="Can create repositories" horizontal help="New repositories are added to the team">
										<Switch bind:checked={editCanCreate} />
									</FormField>
									<FormField label="Admin access" horizontal help="Repository admin on included repositories">
										<Switch bind:checked={editAdmin} />
									</FormField>
								</div>
							</FormSection>
							<FormSection title="Unit permissions" description="Level per repository unit for included repositories.">
								<TeamPermissionGrid bind:value={editUnits} idPrefix="edit" />
							</FormSection>
						</div>
						{#snippet footer()}
							<Button onclick={savePermissions} disabled={saving || !editName.trim()} class="gap-2">
								<Save class="h-4 w-4" />
								{saving ? 'Saving...' : 'Save Changes'}
							</Button>
						{/snippet}
					</FormCard>

					{#if !team.isDefault}
						<div class="rounded-xl border border-destructive/40 overflow-hidden">
							<div class="px-6 py-4 border-b border-destructive/30 bg-destructive/5">
								<h3 class="text-sm font-semibold text-destructive">Danger Zone</h3>
							</div>
							<div class="flex flex-col sm:flex-row sm:items-center sm:justify-between gap-3 px-6 py-4">
								<div class="min-w-0">
									<p class="text-sm font-medium">Delete this team</p>
									<p class="text-[13px] text-muted-foreground mt-0.5">Members lose the access this team granted.</p>
								</div>
								<Button
									variant="outline"
									class="shrink-0 self-start sm:self-auto text-destructive border-destructive/40 hover:bg-destructive/10 hover:text-destructive"
									onclick={() => (deleteOpen = true)}
								>
									<Trash2 class="h-4 w-4 mr-1.5" />
									Delete
								</Button>
							</div>
						</div>
					{/if}
				</Tabs.Content>
			{/if}
		</Tabs.Root>
	{/if}
</div>

<FormPanel bind:open={addMemberOpen} title="Add Member" description="Only organization members can join a team." icon={UserPlus}>
	<FormSection title="Member">
		<FormField label="Username" id="team-add-user" required>
			<AsyncSelect
				bind:selected={addUserId}
				placeholder="Select a member..."
				searchPlaceholder="Search members..."
				fetchPage={async (query, pageToken) => {
					const resp = await rpcClient.organization.listOrgMembers({
						page: { query: { text: query, filters: [] }, pageToken, pageSize: 20 },
						orgId
					});
					return {
						items: resp.members.map((m) => ({ value: m.userId, label: m.username })),
						nextPageToken: resp.page?.nextPageToken ?? ''
					};
				}}
			/>
		</FormField>
	</FormSection>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (addMemberOpen = false)}>Cancel</Button>
		<Button onclick={addMember} disabled={addingMember || !addUserId}>
			{addingMember ? 'Adding...' : 'Add Member'}
		</Button>
	{/snippet}
</FormPanel>

<FormPanel bind:open={addRepoOpen} title="Add Git Repository" description="Git repositories in this organization." icon={GitBranch}>
	<FormSection title="Git repository">
		<FormField label="Repository" id="team-add-repo" required>
			<AsyncSelect
				bind:selected={addRepoName}
				placeholder="Select a repository..."
				searchPlaceholder="Search repositories..."
				fetchPage={async (query, pageToken) => {
					const resp = await rpcClient.git.listGitRepositories({
						page: { query: { text: query, filters: [] }, pageToken, pageSize: 20 },
						namespace: orgName,
						includeArchived: true
					});
					return {
						items: resp.repositories.map((r) => ({ value: r.name, label: r.name })),
						nextPageToken: resp.page?.nextPageToken ?? ''
					};
				}}
			/>
		</FormField>
	</FormSection>
	{#snippet footer()}
		<Button variant="outline" onclick={() => (addRepoOpen = false)}>Cancel</Button>
		<Button onclick={addRepo} disabled={addingRepo || !addRepoName}>
			{addingRepo ? 'Adding...' : 'Add Repository'}
		</Button>
	{/snippet}
</FormPanel>

<ConfirmDialog bind:open={removeMemberOpen} title="Remove Member" confirmLabel="Remove" onConfirm={confirmRemoveMember} loading={removingMember}>
	{#snippet description()}
		Remove <strong>{removeMember?.username}</strong> from this team?
	{/snippet}
</ConfirmDialog>

<ConfirmDialog bind:open={removeRepoOpen} title="Remove Git Repository" confirmLabel="Remove" onConfirm={confirmRemoveRepo} loading={removingRepo}>
	{#snippet description()}
		Remove <strong>{removeRepo?.namespace}/{removeRepo?.name}</strong> from this team?
	{/snippet}
</ConfirmDialog>

<ConfirmDialog bind:open={deleteOpen} title="Delete Team" confirmLabel="Delete" onConfirm={confirmDelete} loading={deleting} icon={Trash2}>
	{#snippet description()}
		Delete <strong>{team?.name}</strong>? Members lose the access it granted.
	{/snippet}
</ConfirmDialog>
