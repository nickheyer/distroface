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
	import { Avatar, AvatarFallback } from '$lib/components/ui/avatar';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import * as Tabs from '$lib/components/ui/tabs';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import FormActions from '$lib/components/form-actions.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import TeamPermissionGrid, {
		toUnitLevels,
		fromUnitLevels,
		type UnitLevels
	} from '$lib/components/git/team-permission-grid.svelte';
	import {
		Users as UsersRound,
		Users,
		GitBranch,
		Plus,
		Trash,
		Save,
		ArrowLeft,
		Loader
	} from '$lib/icons';
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
	const tabs = ['members', 'repos', 'permissions'];
	const tabParam = page.url.searchParams.get('tab') ?? '';
	let tab = $state(tabs.includes(tabParam) ? tabParam : 'members');
	const addMemberHref = $derived(
		resolve('/orgs/[name]/teams/[team]/members/new', { name: orgName, team: teamId })
	);
	const addRepoHref = $derived(
		resolve('/orgs/[name]/teams/[team]/repos/new', { name: orgName, team: teamId })
	);

	let members = $state<OrgMember[]>([]);
	const memberPager = new Pager(20);
	let membersLoaded = $state(false);
	let removeMember = $state<OrgMember | null>(null);
	let removeMemberOpen = $state(false);
	let removingMember = $state(false);

	let repos = $state<TeamRepo[]>([]);
	const repoPager = new Pager(20);
	let reposLoaded = $state(false);
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

<div class="space-y-6">
	<a
		href={resolve('/orgs/[name]/teams', { name: orgName })}
		class="inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
	>
		<ArrowLeft class="size-3.5" />
		Teams
	</a>

	{#if loading}
		<div class="space-y-2">
			<Skeleton class="h-5 w-48" />
			<Skeleton class="h-4 w-64" />
		</div>
		<Skeleton class="h-64 w-full rounded-lg" />
	{:else if !team}
		<EmptyState
			icon={UsersRound}
			message="Team not found"
			description="It may have been deleted."
		/>
	{:else}
		<div>
			<div class="flex flex-wrap items-center gap-2">
				<h2 class="section-title">{team.name}</h2>
				{#if team.isDefault}
					<Badge>Default</Badge>
				{/if}
				{#if team.adminAccess}
					<Badge variant="secondary">Admin</Badge>
				{/if}
			</div>
			<p class="section-subtitle">{team.description || 'No description'}</p>
			<div class="mt-2 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground">
				<span class="flex items-center gap-1">
					<Users class="size-3.5" />
					{team.isDefault
						? 'Every member'
						: `${team.memberCount} member${team.memberCount !== 1 ? 's' : ''}`}
				</span>
				<span class="flex items-center gap-1">
					<GitBranch class="size-3.5" />
					{team.includesAllRepos
						? 'All repositories'
						: `${team.repoCount} repositor${team.repoCount !== 1 ? 'ies' : 'y'}`}
				</span>
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

			<Tabs.Content value="members" class="space-y-4 pt-3">
				{#if team.isDefault}
					<Alert>
						<AlertDescription>
							<p>
								Every organization member belongs to the default team. Manage membership from the
								<a href={resolve('/orgs/[name]/members', { name: orgName })} class="link">Members</a
								> page.
							</p>
						</AlertDescription>
					</Alert>
				{:else}
					{#if ctx.canAdmin}
						<div class="flex justify-end">
							<Button href={addMemberHref}>
								<Plus />
								Add member
							</Button>
						</div>
					{/if}
					{#if !membersLoaded}
						<div class="box divide-y">
							{#each { length: 2 }, i (i)}
								<div class="flex items-center gap-3 px-4 py-3">
									<Skeleton class="size-7 rounded-full" />
									<div class="flex-1 space-y-2">
										<Skeleton class="h-4 w-32" />
										<Skeleton class="h-3 w-24" />
									</div>
								</div>
							{/each}
						</div>
					{:else if members.length === 0}
						<EmptyState
							icon={Users}
							message="No members"
							description="Add organization members to this team."
						/>
					{:else}
						<div class="box divide-y">
							{#each members as member (member.userId)}
								<div class="flex items-center gap-3 px-4 py-3">
									<Avatar class="size-7">
										<AvatarFallback>{member.username.slice(0, 2)}</AvatarFallback>
									</Avatar>
									<div class="min-w-0 flex-1">
										<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
											<a
												href={resolve('/[namespace]', { namespace: member.username })}
												class="truncate text-sm font-semibold underline-offset-2 hover:text-primary hover:underline"
											>
												{member.username}
											</a>
											<Badge variant="outline">{orgRoleLabel(member.role)}</Badge>
										</div>
										<p class="mt-0.5 text-xs text-muted-foreground">
											Joined {member.joinedAt ? relativeTime(timestampDate(member.joinedAt)) : '-'}
										</p>
									</div>
									{#if ctx.canAdmin}
										<Button
											variant="ghost"
											size="sm"
											class="shrink-0 text-destructive hover:text-destructive"
											onclick={() => {
												removeMember = member;
												removeMemberOpen = true;
											}}
										>
											Remove
										</Button>
									{/if}
								</div>
							{/each}
						</div>
						<DataPagination pager={memberPager} onChange={loadMembers} />
					{/if}
				{/if}
			</Tabs.Content>

			<Tabs.Content value="repos" class="space-y-4 pt-3">
				{#if team.includesAllRepos}
					<Alert>
						<AlertDescription
							>This team includes every repository in the organization.</AlertDescription
						>
					</Alert>
				{:else}
					{#if ctx.canAdmin}
						<div class="flex justify-end">
							<Button href={addRepoHref}>
								<Plus />
								Add repository
							</Button>
						</div>
					{/if}
					{#if !reposLoaded}
						<div class="box divide-y">
							{#each { length: 2 }, i (i)}
								<div class="flex items-center gap-3 px-4 py-3">
									<Skeleton class="size-4 rounded-sm" />
									<Skeleton class="h-4 w-48" />
								</div>
							{/each}
						</div>
					{:else if repos.length === 0}
						<EmptyState
							icon={GitBranch}
							message="No repositories"
							description="Add repositories this team can access."
						/>
					{:else}
						<div class="box divide-y">
							{#each repos as repo (repo.repoId)}
								<div class="flex items-center gap-3 px-4 py-3">
									<GitBranch class="size-4 shrink-0 text-muted-foreground" />
									<a
										href={resolve('/[namespace]/[name]', {
											namespace: repo.namespace,
											name: repo.name
										})}
										class="min-w-0 flex-1 truncate text-sm font-semibold underline-offset-2 hover:text-primary hover:underline"
									>
										{repo.namespace}/{repo.name}
									</a>
									{#if ctx.canAdmin}
										<Button
											variant="ghost"
											size="sm"
											class="shrink-0 text-destructive hover:text-destructive"
											onclick={() => {
												removeRepo = repo;
												removeRepoOpen = true;
											}}
										>
											Remove
										</Button>
									{/if}
								</div>
							{/each}
						</div>
						<DataPagination pager={repoPager} onChange={loadRepos} />
					{/if}
				{/if}
			</Tabs.Content>

			{#if ctx.canAdmin}
				<Tabs.Content value="permissions" class="form-stack pt-6">
					<FormSection title="Details" description="Name and description shown in the team list.">
						<FormField label="Name" id="edit-team-name" required span="half">
							<Input id="edit-team-name" bind:value={editName} />
						</FormField>
						<FormField label="Description" id="edit-team-desc">
							<Textarea id="edit-team-desc" bind:value={editDescription} rows={2} />
						</FormField>
					</FormSection>
					<FormSection
						title="Git repositories"
						description="Which repositories the team covers and what it may do with them."
					>
						<FormField
							label="Includes all repositories"
							layout="toggle"
							help="Otherwise repositories are added one by one"
						>
							<Switch bind:checked={editIncludesAll} />
						</FormField>
						<FormField
							label="Can create repositories"
							layout="toggle"
							help="New repositories are added to the team"
						>
							<Switch bind:checked={editCanCreate} />
						</FormField>
						<FormField
							label="Admin access"
							layout="toggle"
							help="Repository admin on included repositories"
						>
							<Switch bind:checked={editAdmin} />
						</FormField>
					</FormSection>
					<FormSection
						title="Unit permissions"
						description="Level per repository unit for included repositories."
					>
						<TeamPermissionGrid bind:value={editUnits} idPrefix="edit" />
					</FormSection>
					<FormActions>
						<Button onclick={savePermissions} disabled={saving || !editName.trim()}>
							{#if saving}
								<Loader class="animate-spin" />
								Saving
							{:else}
								<Save />
								Save changes
							{/if}
						</Button>
					</FormActions>

					{#if !team.isDefault}
						<FormSection
							title="Danger zone"
							description="Members lose the access this team granted."
						>
							<div class="box border-destructive/40">
								<div class="flex items-center justify-between gap-4 px-4 py-3">
									<div class="min-w-0">
										<p class="text-sm font-medium">Delete this team</p>
										<p class="mt-0.5 text-sm text-muted-foreground">
											Removes the team and its repository grants.
										</p>
									</div>
									<Button
										variant="outline-destructive"
										class="shrink-0"
										onclick={() => (deleteOpen = true)}
									>
										<Trash />
										Delete
									</Button>
								</div>
							</div>
						</FormSection>
					{/if}
				</Tabs.Content>
			{/if}
		</Tabs.Root>
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
		Remove <strong>{removeMember?.username}</strong> from this team?
	{/snippet}
</ConfirmDialog>

<ConfirmDialog
	bind:open={removeRepoOpen}
	title="Remove git repository"
	confirmLabel="Remove"
	onConfirm={confirmRemoveRepo}
	loading={removingRepo}
>
	{#snippet description()}
		Remove <strong>{removeRepo?.namespace}/{removeRepo?.name}</strong> from this team?
	{/snippet}
</ConfirmDialog>

<ConfirmDialog
	bind:open={deleteOpen}
	title="Delete team"
	confirmLabel="Delete"
	onConfirm={confirmDelete}
	loading={deleting}
	icon={Trash}
>
	{#snippet description()}
		Delete <strong>{team?.name}</strong>? Members lose the access it granted.
	{/snippet}
</ConfirmDialog>
