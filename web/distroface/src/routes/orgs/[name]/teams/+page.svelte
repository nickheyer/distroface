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
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import TeamPermissionGrid, {
		defaultUnitLevels, fromUnitLevels, type UnitLevels
	} from '$lib/components/git/team-permission-grid.svelte';
	import { UsersRound, Plus, GitBranch, Users } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { Pager } from '$lib/pager.svelte';
	import { TeamUnitLevel, type Team } from '$lib/proto/distroface/v1/organization_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgId = $derived(ctx.org?.id ?? '');
	const orgName = $derived(page.params.name ?? '');

	let teams = $state<Team[]>([]);
	const pager = new Pager(20);
	let loading = $state(true);
	let loaded = $state(false);

	let createOpen = $state(false);
	let newName = $state('');
	let newDescription = $state('');
	let newIncludesAll = $state(false);
	let newCanCreate = $state(false);
	let newAdmin = $state(false);
	let newUnits = $state<UnitLevels>(defaultUnitLevels(TeamUnitLevel.READ));
	let creating = $state(false);

	async function loadTeams() {
		loading = true;
		try {
			const resp = await rpcClient.organization.listTeams({ page: pager.request(), orgId });
			teams = resp.teams;
			pager.apply(resp.page);
		} catch {
			teams = [];
		} finally {
			loading = false;
			loaded = true;
		}
	}

	function openCreate() {
		newName = '';
		newDescription = '';
		newIncludesAll = false;
		newCanCreate = false;
		newAdmin = false;
		newUnits = defaultUnitLevels(TeamUnitLevel.READ);
		createOpen = true;
	}

	async function createTeam() {
		if (!newName.trim()) return;
		creating = true;
		try {
			const resp = await rpcClient.organization.createTeam({
				orgId,
				name: newName.trim(),
				description: newDescription.trim(),
				canCreateRepos: newCanCreate,
				includesAllRepos: newIncludesAll,
				adminAccess: newAdmin,
				units: fromUnitLevels(newUnits)
			});
			toast.success('Team created');
			createOpen = false;
			if (resp.team) {
				goto(resolve('/orgs/[name]/teams/[team]', { name: orgName, team: resp.team.id }));
				return;
			}
			await loadTeams();
		} catch {
			// error interceptor
		} finally {
			creating = false;
		}
	}

	onMount(loadTeams);
</script>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Teams</h2>
			<p class="section-subtitle">Groups of members with repository permissions.</p>
		</div>
		{#if ctx.canAdmin}
			<Button size="sm" class="shrink-0" onclick={openCreate}>
				<Plus class="h-4 w-4 mr-1.5" />
				New Team
			</Button>
		{/if}
	</div>

	{#if !loaded}
		<div class="space-y-2">
			{#each { length: 3 }, i (i)}
				<Skeleton class="h-14 w-full rounded-lg" />
			{/each}
		</div>
	{:else if teams.length === 0}
		<EmptyState
			icon={UsersRound}
			message="No teams"
			description="Teams grant members access to repositories by unit."
		/>
	{:else}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table>
				<TableHeader>
					<TableRow class="bg-muted/30 hover:bg-muted/30">
						<TableHead class="th">Team</TableHead>
						<TableHead class="th hidden sm:table-cell">Access</TableHead>
						<TableHead class="th w-28">Members</TableHead>
						<TableHead class="th w-32 hidden md:table-cell">Git repositories</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each teams as team (team.id)}
						<TableRow>
							<TableCell class="py-3 px-3">
								<a
									href={resolve('/orgs/[name]/teams/[team]', { name: orgName, team: team.id })}
									class="font-medium hover:text-primary transition-colors"
								>
									{team.name}
								</a>
								{#if team.description}
									<p class="text-[13px] text-muted-foreground mt-0.5 truncate max-w-md">{team.description}</p>
								{/if}
							</TableCell>
							<TableCell class="py-3 px-3 hidden sm:table-cell">
								<div class="flex flex-wrap gap-1">
									{#if team.isDefault}
										<Badge variant="default" class="text-[11px]">Default</Badge>
									{/if}
									{#if team.adminAccess}
										<Badge variant="secondary" class="text-[11px]">Admin</Badge>
									{/if}
									{#if team.includesAllRepos}
										<Badge variant="outline" class="text-[11px]">All repositories</Badge>
									{/if}
									{#if team.canCreateRepos}
										<Badge variant="outline" class="text-[11px]">Creates repositories</Badge>
									{/if}
								</div>
							</TableCell>
							<TableCell class="text-sm text-muted-foreground py-3 px-3">
								<span class="inline-flex items-center gap-1.5">
									<Users class="h-3.5 w-3.5" />
									{team.isDefault ? ctx.org?.memberCount ?? team.memberCount : team.memberCount}
								</span>
							</TableCell>
							<TableCell class="text-sm text-muted-foreground py-3 px-3 hidden md:table-cell">
								<span class="inline-flex items-center gap-1.5">
									<GitBranch class="h-3.5 w-3.5" />
									{team.includesAllRepos ? 'All' : team.repoCount}
								</span>
							</TableCell>
						</TableRow>
					{/each}
				</TableBody>
			</Table>
			<DataPagination attached {pager} onChange={loadTeams} />
		</div>
	{/if}
</div>

<FormPanel
	bind:open={createOpen}
	title="New Team"
	description="Members of a team get the unit levels below on its repositories."
	icon={UsersRound}
	wide
>
	<div class="space-y-6">
		<FormSection title="Details">
			<div class="space-y-3">
				<FormField label="Name" id="team-name" required>
					<Input id="team-name" bind:value={newName} placeholder="developers" />
				</FormField>
				<FormField label="Description" id="team-desc">
					<Textarea id="team-desc" bind:value={newDescription} placeholder="What this team works on" rows={2} />
				</FormField>
			</div>
		</FormSection>

		<FormSection title="Git repositories">
			<div class="space-y-2">
				<FormField label="Includes all repositories" horizontal help="Otherwise repositories are added one by one">
					<Switch bind:checked={newIncludesAll} />
				</FormField>
				<FormField label="Can create repositories" horizontal help="New repositories are added to the team">
					<Switch bind:checked={newCanCreate} />
				</FormField>
				<FormField label="Admin access" horizontal help="Repository admin on included repositories">
					<Switch bind:checked={newAdmin} />
				</FormField>
			</div>
		</FormSection>

		<FormSection title="Unit permissions" description="Level per repository unit for included repositories.">
			<TeamPermissionGrid bind:value={newUnits} idPrefix="new" />
		</FormSection>
	</div>

	{#snippet footer()}
		<Button variant="outline" onclick={() => (createOpen = false)}>Cancel</Button>
		<Button onclick={createTeam} disabled={creating || !newName.trim()}>
			{creating ? 'Creating...' : 'Create Team'}
		</Button>
	{/snippet}
</FormPanel>
