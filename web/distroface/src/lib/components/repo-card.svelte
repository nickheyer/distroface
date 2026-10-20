<script lang="ts">
	import { Lock, Eye, ArrowDown, Tags, HardDrive, Clock, Star } from '@lucide/svelte';
	import { Badge } from '$lib/components/ui/badge';
	import MirrorBadge from '$lib/components/mirror-badge.svelte';
	import { RepositoryType, Visibility } from '$lib/proto/distroface/v1/types_pb';
	import { authStore } from '$lib/stores/auth.svelte';
	import { mirrorSyncStore } from '$lib/stores/mirror-sync.svelte';
	import { onMount } from 'svelte';
	import { relativeTime, formatBytes } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import type { Repository } from '$lib/proto/distroface/v1/types_pb';
	import { resolve } from '$app/paths';

	let { repo }: { repo: Repository } = $props();

	onMount(() => {
		if (repo.type === RepositoryType.MIRROR) mirrorSyncStore.ensure();
	});

	const isPrivate = $derived(repo.visibility === Visibility.PRIVATE);
	const isOwner = $derived(!!authStore.user && repo.ownerId === authStore.user.id);
	const lastPushed = $derived(
		repo.lastPushedAt ? relativeTime(timestampDate(repo.lastPushedAt)) : null
	);
	const hasStats = $derived(
		repo.tagCount > 0 ||
			Number(repo.sizeBytes) > 0 ||
			repo.pullCount > 0n ||
			repo.starCount > 0n ||
			lastPushed
	);
</script>

<a
	href={resolve('/images/[namespace]/[...name]', { namespace: repo.namespace, name: repo.name })}
	class="group block px-4 py-3 transition-colors hover:bg-muted/30"
>
	<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
		<span class="flex min-w-0 items-baseline text-sm font-semibold">
			<span class="truncate font-normal text-muted-foreground">{repo.namespace}</span>
			<span class="mx-0.5 text-muted-foreground/50">/</span>
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
		{#if isOwner}
			<Badge variant="secondary">Owner</Badge>
		{/if}
		{#if repo.type === RepositoryType.MIRROR}
			<MirrorBadge
				label="Mirror"
				error={repo.mirrorLastError}
				title={repo.mirror?.upstream ?? ''}
				syncing={repo.mirrorSyncing || mirrorSyncStore.syncing('image', repo.id)}
			/>
		{/if}
	</div>
	{#if repo.description}
		<p class="mt-0.5 line-clamp-1 text-sm text-muted-foreground">{repo.description}</p>
	{/if}
	{#if hasStats}
		<div class="mt-1.5 flex flex-wrap items-center gap-x-3.5 gap-y-1 text-xs text-muted-foreground">
			{#if repo.tagCount > 0}
				<span class="flex items-center gap-1 tabular-nums">
					<Tags class="size-3.5" />{repo.tagCount} tag{repo.tagCount !== 1 ? 's' : ''}
				</span>
			{/if}
			{#if Number(repo.sizeBytes) > 0}
				<span class="flex items-center gap-1"
					><HardDrive class="size-3.5" />{formatBytes(Number(repo.sizeBytes))}</span
				>
			{/if}
			{#if repo.pullCount > 0n}
				<span class="flex items-center gap-1 tabular-nums">
					<ArrowDown class="size-3.5" />{repo.pullCount.toLocaleString()} pull{repo.pullCount !== 1n
						? 's'
						: ''}
				</span>
			{/if}
			{#if repo.starCount > 0n}
				<span class="flex items-center gap-1 tabular-nums">
					<Star
						class="size-3.5 {repo.isStarred ? 'fill-warning text-warning' : ''}"
					/>{repo.starCount.toLocaleString()}
				</span>
			{/if}
			{#if lastPushed}
				<span class="hidden items-center gap-1 sm:flex"
					><Clock class="size-3.5" />Pushed {lastPushed}</span
				>
			{/if}
		</div>
	{/if}
</a>
