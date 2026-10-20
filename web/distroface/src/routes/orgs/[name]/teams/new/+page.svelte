<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import { UsersRound, Loader2 } from '@lucide/svelte';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import { Switch } from '$lib/components/ui/switch';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import TeamPermissionGrid, {
		defaultUnitLevels,
		fromUnitLevels,
		type UnitLevels
	} from '$lib/components/git/team-permission-grid.svelte';
	import { TeamUnitLevel } from '$lib/proto/distroface/v1/organization_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgId = $derived(ctx.org?.id ?? '');
	const orgName = $derived(page.params.name ?? '');
	const listHref = $derived(resolve('/orgs/[name]/teams', { name: orgName }));

	let newName = $state('');
	let newDescription = $state('');
	let newIncludesAll = $state(false);
	let newCanCreate = $state(false);
	let newAdmin = $state(false);
	let newUnits = $state<UnitLevels>(defaultUnitLevels(TeamUnitLevel.READ));
	let creating = $state(false);

	async function createTeam() {
		if (!newName.trim() || creating) return;
		creating = true;
		const resp = await rpcClient.organization
			.createTeam({
				orgId,
				name: newName.trim(),
				description: newDescription.trim(),
				canCreateRepos: newCanCreate,
				includesAllRepos: newIncludesAll,
				adminAccess: newAdmin,
				units: fromUnitLevels(newUnits)
			})
			.catch(() => undefined);
		creating = false;
		if (!resp) return;
		toast.success('Team created');
		if (resp.team) {
			await goto(resolve('/orgs/[name]/teams/[team]', { name: orgName, team: resp.team.id }));
			return;
		}
		await goto(resolve('/orgs/[name]/teams', { name: orgName }));
	}
</script>

<svelte:head>
	<title>New team - {orgName}</title>
</svelte:head>

{#if !ctx.canAdmin}
	<EmptyState
		icon={UsersRound}
		message="You cannot create teams"
		description="Only organization owners and admins manage teams."
	>
		{#snippet actions()}
			<Button variant="outline" href={listHref}>Back to teams</Button>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		level="section"
		title="New team"
		description="Members of a team get the unit levels below on its repositories."
		backHref={listHref}
		backLabel="Teams"
	>
		<FormSection title="Details" description="Name and description shown in the team list.">
			<FormField label="Name" id="team-name" required span="half">
				<Input id="team-name" bind:value={newName} placeholder="developers" />
			</FormField>
			<FormField label="Description" id="team-desc">
				<Textarea
					id="team-desc"
					bind:value={newDescription}
					placeholder="What this team works on"
					rows={2}
				/>
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
				<Switch bind:checked={newIncludesAll} />
			</FormField>
			<FormField
				label="Can create repositories"
				layout="toggle"
				help="New repositories are added to the team"
			>
				<Switch bind:checked={newCanCreate} />
			</FormField>
			<FormField
				label="Admin access"
				layout="toggle"
				help="Repository admin on included repositories"
			>
				<Switch bind:checked={newAdmin} />
			</FormField>
		</FormSection>

		<FormSection
			title="Unit permissions"
			description="Level per repository unit for included repositories."
		>
			<TeamPermissionGrid bind:value={newUnits} idPrefix="new" />
		</FormSection>

		{#snippet footer()}
			<Button onclick={createTeam} disabled={creating || !newName.trim()}>
				{#if creating}<Loader2 class="animate-spin" />{/if}
				{creating ? 'Creating' : 'Create team'}
			</Button>
			<Button variant="ghost-muted" href={listHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}
