<script lang="ts">
	import { page } from '$app/state';
	import { resolve } from '$app/paths';
	import { getContext } from 'svelte';
	import { rpcClient, backgroundCallOptions } from '$lib/api/rpc-client';
	import { configStore } from '$lib/stores/config.svelte';
	import { authStore } from '$lib/stores/auth.svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import UserAvatar from '$lib/components/user-avatar.svelte';
	import ActivityFeedView from '$lib/components/activity-feed.svelte';
	import ProfileReadme from '$lib/components/profile-readme.svelte';
	import { Activity, GitBranch, Package, Star, Users } from '$lib/icons';
	import { ActivityFeed } from '$lib/proto/distroface/v1/activity_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';
	import type { GitRepository } from '$lib/proto/distroface/v1/git_pb';
	import type { OrgMember, Repository } from '$lib/proto/distroface/v1/types_pb';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');

	let topRepos = $state<GitRepository[]>([]);
	let recentImages = $state<Repository[]>([]);
	let members = $state<OrgMember[]>([]);
	let memberTotal = $state(0);
	let loaded = $state(false);
	let loadedFor = '';

	async function load() {
		const orgId = ctx.org?.id ?? '';
		const results = await Promise.allSettled([
			configStore.gitEnabled
				? rpcClient.git.listGitRepositories(
						{ namespace: orgName, page: { pageSize: 6, pageToken: '', orderBy: 'stars desc' } },
						backgroundCallOptions
					)
				: Promise.resolve(undefined),
			rpcClient.repository.listRepositories(
				{ namespace: orgName, page: { pageSize: 4, pageToken: '', orderBy: '' } },
				backgroundCallOptions
			),
			orgId
				? rpcClient.organization.listOrgMembers(
						{ orgId, page: { pageSize: 12, pageToken: '', orderBy: '' } },
						backgroundCallOptions
					)
				: Promise.resolve(undefined)
		]);
		topRepos = results[0].status === 'fulfilled' ? (results[0].value?.repositories ?? []) : [];
		recentImages = results[1].status === 'fulfilled' ? results[1].value.repositories : [];
		members = results[2].status === 'fulfilled' ? (results[2].value?.members ?? []) : [];
		memberTotal =
			results[2].status === 'fulfilled' ? Number(results[2].value?.page?.totalCount ?? 0n) : 0;
		loaded = true;
	}

	$effect(() => {
		const key = `${orgName}:${ctx.org?.id ?? ''}`;
		if (!ctx.org || key === loadedFor) return;
		loadedFor = key;
		void load();
	});

	const membersHref = $derived(resolve('/orgs/[name]/members', { name: orgName }));
</script>

<!-- eslint-disable svelte/no-navigation-without-resolve -->
<svelte:head>
	<title>{ctx.org?.displayName || orgName} - Distroface</title>
</svelte:head>

<div class="grid gap-6 xl:grid-cols-[minmax(0,1fr)_17rem]">
	<div class="min-w-0 space-y-6">
		{#if configStore.gitEnabled}
			<ProfileReadme namespace={orgName} isOwn={ctx.canAdmin} ownerLabel="organization" />
		{/if}

		{#if !loaded}
			<Skeleton class="h-40 w-full rounded-lg" />
		{:else}
			{#if topRepos.length > 0}
				<section class="space-y-2">
					<div class="flex items-center justify-between">
						<h2 class="text-sm font-semibold">Repositories</h2>
						<a
							href={resolve('/orgs/[name]/repos', { name: orgName })}
							class="text-xs text-muted-foreground hover:text-primary">All</a
						>
					</div>
					<div class="grid gap-3 sm:grid-cols-2">
						{#each topRepos as r (r.id)}
							<a
								href={resolve('/[namespace]/[name]', { namespace: r.namespace, name: r.name })}
								class="card-interactive block p-3"
							>
								<div class="flex items-center justify-between gap-2">
									<span class="flex min-w-0 items-center gap-1.5 text-sm font-semibold">
										<GitBranch class="size-3.5 shrink-0 text-muted-foreground" />
										<span class="truncate">{r.name}</span>
									</span>
									<Badge variant="outline" class="shrink-0"
										>{r.visibility === 2 ? 'Private' : 'Public'}</Badge
									>
								</div>
								<p class="mt-1 line-clamp-2 min-h-[2.5rem] text-xs text-muted-foreground">
									{r.description || 'No description'}
								</p>
								<div class="mt-2 flex items-center gap-3 text-xs text-muted-foreground">
									{#if Number(r.starCount) > 0}
										<span class="flex items-center gap-1 tabular-nums"
											><Star class="size-3" />{Number(r.starCount)}</span
										>
									{/if}
									{#if r.updatedAt}
										<span>Updated {relativeTime(timestampDate(r.updatedAt))}</span>
									{/if}
								</div>
							</a>
						{/each}
					</div>
				</section>
			{/if}

			{#if recentImages.length > 0}
				<section class="space-y-2">
					<div class="flex items-center justify-between">
						<h2 class="text-sm font-semibold">Images</h2>
						<a
							href={resolve('/orgs/[name]/images', { name: orgName })}
							class="text-xs text-muted-foreground hover:text-primary">All</a
						>
					</div>
					<div class="grid gap-3 sm:grid-cols-2">
						{#each recentImages as r (r.id)}
							<a
								href={resolve('/images/[namespace]/[...name]', {
									namespace: r.namespace,
									name: r.name
								})}
								class="card-interactive flex items-center gap-3 p-3"
							>
								<Package class="size-4 shrink-0 text-muted-foreground" />
								<span class="min-w-0 flex-1">
									<span class="block truncate text-sm font-semibold">{r.name}</span>
									<span class="block truncate text-xs text-muted-foreground">
										{r.tagCount} tag{r.tagCount === 1 ? '' : 's'}{#if r.lastPushedAt}, pushed {relativeTime(
												timestampDate(r.lastPushedAt)
											)}{/if}
									</span>
								</span>
							</a>
						{/each}
					</div>
				</section>
			{/if}
		{/if}

		<section class="space-y-2">
			<h2 class="flex items-center gap-1.5 text-sm font-semibold">
				<Activity class="size-4 text-muted-foreground" />Activity
			</h2>
			<ActivityFeedView
				feed={ActivityFeed.NAMESPACE}
				namespace={orgName}
				pageSize={15}
				emptyMessage="Nothing has happened under {orgName} yet"
				emptyDescription="Pushes, releases, issues, stars, and uploads under this organization land here."
			/>
		</section>
	</div>

	<aside class="space-y-5 xl:sticky xl:top-20 xl:self-start">
		{#if authStore.isAuthenticated}
			<section class="space-y-2">
				<div class="flex items-center justify-between">
					<h2 class="eyebrow flex items-center gap-1.5"><Users class="size-3.5" />People</h2>
					{#if memberTotal > 0}
						<a href={membersHref} class="text-xs text-muted-foreground hover:text-primary"
							>{memberTotal}</a
						>
					{/if}
				</div>
				{#if !loaded}
					<Skeleton class="h-10 w-full" />
				{:else if members.length === 0}
					<p class="text-xs text-muted-foreground">No members listed.</p>
				{:else}
					<div class="flex flex-wrap gap-1.5">
						{#each members as m (m.userId)}
							<a href={resolve('/[namespace]', { namespace: m.username })} title={m.username}>
								<UserAvatar username={m.username} class="size-8 ring-1 ring-border" />
							</a>
						{/each}
						{#if memberTotal > members.length}
							<a
								href={membersHref}
								class="flex size-8 items-center justify-center rounded-full border bg-muted text-2xs text-muted-foreground"
								>+{memberTotal - members.length}</a
							>
						{/if}
					</div>
				{/if}
			</section>
		{/if}
		{#if ctx.org?.createdAt}
			<p class="text-xs text-muted-foreground">
				Created {relativeTime(timestampDate(ctx.org.createdAt))}
			</p>
		{/if}
	</aside>
</div>
<!-- eslint-enable svelte/no-navigation-without-resolve -->
