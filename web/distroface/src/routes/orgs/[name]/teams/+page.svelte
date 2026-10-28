<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { onMount, getContext } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import { Users as UsersRound, Plus, GitBranch, Users, ChevronRight } from '$lib/icons';
	import { rpcClient } from '$lib/api/rpc-client';
	import { Pager } from '$lib/pager.svelte';
	import type { Team } from '$lib/proto/distroface/v1/organization_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgId = $derived(ctx.org?.id ?? '');
	const orgName = $derived(page.params.name ?? '');
	const newHref = $derived(resolve('/orgs/[name]/teams/new', { name: orgName }));

	let teams = $state<Team[]>([]);
	const pager = new Pager(20);
	let loading = $state(true);
	let loaded = $state(false);

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

	onMount(loadTeams);
</script>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Teams</h2>
			<p class="section-subtitle">Groups of members with repository permissions.</p>
		</div>
		{#if ctx.canAdmin}
			<Button class="shrink-0" href={newHref}>
				<Plus />
				New team
			</Button>
		{/if}
	</div>

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 3 }, i (i)}
				<div class="flex items-center gap-3 px-4 py-3">
					<Skeleton class="size-9 rounded-md" />
					<div class="flex-1 space-y-2">
						<Skeleton class="h-4 w-40" />
						<Skeleton class="h-3 w-64" />
					</div>
				</div>
			{/each}
		</div>
	{:else if teams.length === 0}
		<EmptyState
			icon={UsersRound}
			message="No teams"
			description="Teams grant members access to repositories by unit."
		>
			{#snippet actions()}
				{#if ctx.canAdmin}
					<Button variant="outline" href={newHref}>
						<Plus />
						New team
					</Button>
				{/if}
			{/snippet}
		</EmptyState>
	{:else}
		<div class="box divide-y transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			{#each teams as team (team.id)}
				<a
					href={resolve('/orgs/[name]/teams/[team]', { name: orgName, team: team.id })}
					class="group flex items-center gap-3 px-4 py-3 transition-colors hover:bg-muted/30"
				>
					<div
						class="flex size-9 shrink-0 items-center justify-center rounded-md border bg-muted text-muted-foreground"
					>
						<UsersRound class="size-4" />
					</div>
					<div class="min-w-0 flex-1">
						<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
							<span
								class="truncate text-sm font-semibold underline-offset-2 group-hover:text-primary group-hover:underline"
							>
								{team.name}
							</span>
							{#if team.isDefault}
								<Badge variant="default">Default</Badge>
							{/if}
							{#if team.adminAccess}
								<Badge variant="secondary">Admin</Badge>
							{/if}
							{#if team.includesAllRepos}
								<Badge variant="outline">All repositories</Badge>
							{/if}
							{#if team.canCreateRepos}
								<Badge variant="outline">Creates repositories</Badge>
							{/if}
						</div>
						{#if team.description}
							<p class="mt-0.5 line-clamp-1 text-sm text-muted-foreground">{team.description}</p>
						{/if}
						<div
							class="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground"
						>
							<span class="flex items-center gap-1 tabular-nums">
								<Users class="size-3.5" />
								{team.isDefault ? (ctx.org?.memberCount ?? team.memberCount) : team.memberCount}
							</span>
							<span class="flex items-center gap-1 tabular-nums">
								<GitBranch class="size-3.5" />
								{team.includesAllRepos ? 'All' : team.repoCount}
							</span>
						</div>
					</div>
					<ChevronRight
						class="size-4 shrink-0 text-muted-foreground/50 transition-colors group-hover:text-muted-foreground"
					/>
				</a>
			{/each}
		</div>
		<DataPagination {pager} onChange={loadTeams} />
	{/if}
</div>
