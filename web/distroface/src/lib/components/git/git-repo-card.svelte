<script lang="ts">
	import {
		Lock,
		Eye,
		Star,
		GitFork,
		CircleDot,
		Clock,
		Archive,
		LayoutTemplate,
		HardDrive
	} from '$lib/icons';
	import { resolve } from '$app/paths';
	import { Badge } from '$lib/components/ui/badge';
	import MirrorBadge from '$lib/components/mirror-badge.svelte';
	import { importLabel, importTitle } from '$lib/git-import';
	import { GitRepoType, Visibility } from '$lib/proto/distroface/v1/types_pb';
	import type { GitRepository } from '$lib/proto/distroface/v1/git_pb';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { onMount } from 'svelte';
	import { relativeTime, formatBytes } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';

	let { repo, showNamespace = true }: { repo: GitRepository; showNamespace?: boolean } = $props();

	onMount(() => {
		if (repo.type === GitRepoType.MIRROR) mirrorSyncStore.ensure();
	});

	const isPrivate = $derived(repo.visibility === Visibility.PRIVATE);
	const pushed = $derived(
		repo.lastPushedAt
			? relativeTime(timestampDate(repo.lastPushedAt))
			: repo.updatedAt
				? relativeTime(timestampDate(repo.updatedAt))
				: null
	);
	const stars = $derived(Number(repo.starCount));
	const issues = $derived(Number(repo.openIssueCount));
</script>

<a
	href={resolve('/[namespace]/[name]', { namespace: repo.namespace, name: repo.name })}
	class="group block px-4 py-3 transition-colors hover:bg-muted/30"
>
	<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
		<span class="flex min-w-0 items-baseline text-sm font-semibold">
			{#if showNamespace}
				<span class="truncate font-normal text-muted-foreground">{repo.namespace}</span>
				<span class="mx-0.5 text-muted-foreground/50">/</span>
			{/if}
			<span class="truncate underline-offset-2 group-hover:text-primary group-hover:underline"
				>{repo.name}</span
			>
		</span>
		<Badge variant="outline">
			{#if isPrivate}
				<Lock />Private
			{:else}
				<Eye />Public
			{/if}
		</Badge>
		{#if repo.isArchived}
			<Badge variant="outline"><Archive />Archived</Badge>
		{/if}
		{#if repo.type === GitRepoType.MIRROR}
			<MirrorBadge
				label="Mirror"
				error={repo.mirrorLastError}
				title={repo.mirror?.upstream ?? ''}
				syncing={repo.mirrorSyncing || mirrorSyncStore.syncing('git', repo.id)}
			/>
		{:else if repo.type === GitRepoType.FORK}
			<Badge variant="outline" title={repo.forkOf}><GitFork />Fork</Badge>
		{:else if repo.type === GitRepoType.TEMPLATE}
			<Badge variant="done"><LayoutTemplate />Template</Badge>
		{/if}
		{#if repo.importStatus}
			<MirrorBadge
				label={importLabel(repo.importStatus)}
				syncingLabel="Importing"
				error={repo.importStatus.error}
				title={importTitle(repo.importStatus, mirrorSyncStore.message('import', repo.id))}
				syncing={repo.importStatus.running || mirrorSyncStore.syncing('import', repo.id)}
			/>
		{/if}
	</div>
	{#if repo.description}
		<p class="mt-0.5 line-clamp-1 text-sm text-muted-foreground">{repo.description}</p>
	{/if}
	{#if repo.topics.length > 0}
		<div class="mt-1.5 flex flex-wrap gap-1">
			{#each repo.topics.slice(0, 6) as topic (topic)}
				<Badge>{topic}</Badge>
			{/each}
		</div>
	{/if}
	<div class="mt-1.5 flex flex-wrap items-center gap-x-3.5 gap-y-1 text-xs text-muted-foreground">
		{#if stars > 0}
			<span class="flex items-center gap-1 tabular-nums">
				<Star
					class="size-3.5 {repo.isStarred ? 'fill-warning text-warning' : ''}"
				/>{stars.toLocaleString()}
			</span>
		{/if}
		{#if repo.forkCount > 0}
			<span class="flex items-center gap-1 tabular-nums"
				><GitFork class="size-3.5" />{repo.forkCount}</span
			>
		{/if}
		{#if issues > 0}
			<span class="flex items-center gap-1 tabular-nums"
				><CircleDot class="size-3.5" />{issues.toLocaleString()}</span
			>
		{/if}
		{#if Number(repo.sizeBytes) > 0}
			<span class="flex items-center gap-1"
				><HardDrive class="size-3.5" />{formatBytes(Number(repo.sizeBytes))}</span
			>
		{/if}
		{#if pushed}
			<span class="flex items-center gap-1"><Clock class="size-3.5" />Updated {pushed}</span>
		{/if}
	</div>
</a>
