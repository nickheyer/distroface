<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext, onMount } from 'svelte';
	import { Loader, Users } from '$lib/icons';
	import { toast } from 'svelte-sonner';
	import { rpcClient } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import AsyncSelect from '$lib/components/async-select.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import type { Team } from '$lib/proto/distroface/v1/organization_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgId = $derived(ctx.org?.id ?? '');
	const orgName = $derived(page.params.name ?? '');
	const teamId = $derived(page.params.team ?? '');
	const teamHref = $derived(resolve('/orgs/[name]/teams/[team]', { name: orgName, team: teamId }));

	let team = $state<Team | null>(null);
	let loading = $state(true);
	let userId = $state('');
	let adding = $state(false);

	onMount(async () => {
		try {
			const resp = await rpcClient.organization.getTeam({ orgId, id: teamId });
			team = resp.team ?? null;
		} catch {
			team = null;
		} finally {
			loading = false;
		}
	});

	async function addMember() {
		if (!userId || adding) return;
		adding = true;
		const ok = await rpcClient.organization.addTeamMember({ orgId, teamId, userId }).then(
			() => true,
			() => false
		);
		adding = false;
		if (!ok) return;
		toast.success('Member added');
		await goto(resolve('/orgs/[name]/teams/[team]', { name: orgName, team: teamId }));
	}
</script>

<svelte:head>
	<title>Add team member - {orgName}</title>
</svelte:head>

{#if loading}
	<div class="space-y-4">
		<Skeleton class="h-5 w-40" />
		<Skeleton class="h-48 w-full rounded-lg" />
	</div>
{:else if !team || !ctx.canAdmin}
	<EmptyState
		icon={Users}
		message={team ? 'You cannot add members' : 'Team not found'}
		description={team
			? 'Only organization owners and admins manage teams.'
			: 'It may have been deleted.'}
	>
		{#snippet actions()}
			<Button variant="outline" href={resolve('/orgs/[name]/teams', { name: orgName })}
				>Back to teams</Button
			>
		{/snippet}
	</EmptyState>
{:else}
	<FormPage
		level="section"
		title="Add member to {team.name}"
		description="Only organization members can join a team."
		backHref={teamHref}
		backLabel={team.name}
	>
		<FormSection
			title="Member"
			description="The team's unit levels apply to them on its repositories."
		>
			<FormField label="Username" id="team-add-user" required span="half">
				<AsyncSelect
					bind:selected={userId}
					placeholder="Select a member"
					searchPlaceholder="Search members"
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
			<Button onclick={addMember} disabled={adding || !userId}>
				{#if adding}<Loader class="animate-spin" />{/if}
				{adding ? 'Adding' : 'Add member'}
			</Button>
			<Button variant="ghost-muted" href={teamHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}
