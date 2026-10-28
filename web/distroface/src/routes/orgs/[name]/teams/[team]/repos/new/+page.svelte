<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { getContext, onMount } from 'svelte';
	import { GitBranch, Loader } from '$lib/icons';
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
	const teamHref = $derived(
		`${resolve('/orgs/[name]/teams/[team]', { name: orgName, team: teamId })}?tab=repos`
	);

	let team = $state<Team | null>(null);
	let loading = $state(true);
	let repoName = $state('');
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

	async function addRepo() {
		if (!repoName || adding) return;
		adding = true;
		const ok = await rpcClient.organization.addTeamRepo({ orgId, teamId, repoName }).then(
			() => true,
			() => false
		);
		adding = false;
		if (!ok) return;
		toast.success('Repository added');
		await goto(resolve(teamHref as `/${string}`));
	}
</script>

<svelte:head>
	<title>Add team repository - {orgName}</title>
</svelte:head>

{#if loading}
	<div class="space-y-4">
		<Skeleton class="h-5 w-40" />
		<Skeleton class="h-48 w-full rounded-lg" />
	</div>
{:else if !team || !ctx.canAdmin}
	<EmptyState
		icon={GitBranch}
		message={team ? 'You cannot add repositories' : 'Team not found'}
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
		title="Add repository to {team.name}"
		description="Git repositories in this organization the team gets access to."
		backHref={teamHref}
		backLabel={team.name}
	>
		<FormSection title="Git repository" description="Members get the team's unit levels on it.">
			<FormField label="Repository" id="team-add-repo" required span="half">
				<AsyncSelect
					bind:selected={repoName}
					placeholder="Select a repository"
					searchPlaceholder="Search repositories"
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
			<Button onclick={addRepo} disabled={adding || !repoName}>
				{#if adding}<Loader class="animate-spin" />{/if}
				{adding ? 'Adding' : 'Add repository'}
			</Button>
			<Button variant="ghost-muted" href={teamHref}>Cancel</Button>
		{/snippet}
	</FormPage>
{/if}
