<script lang="ts">
	import {
		Lock, Eye, Star, GitFork, CircleDot, Clock, Archive, LayoutTemplate, HardDrive
	} from '@lucide/svelte';
	import { resolve } from '$app/paths';
	import { Badge } from '$lib/components/ui/badge';
	import MirrorBadge from '$lib/components/mirror-badge.svelte';
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
		repo.lastPushedAt ? relativeTime(timestampDate(repo.lastPushedAt)) : repo.updatedAt ? relativeTime(timestampDate(repo.updatedAt)) : null
	);
	const initials = $derived(repo.name.slice(0, 2).toUpperCase());
	const stars = $derived(Number(repo.starCount));
	const issues = $derived(Number(repo.openIssueCount));
</script>

<a href={resolve('/[namespace]/[name]', { namespace: repo.namespace, name: repo.name })} class="block group">
	<div class="rounded-xl border border-border/60 bg-card px-4 py-3.5 transition-all hover:border-primary/20 hover:shadow-[0_2px_12px_-4px] hover:shadow-primary/8">
		<div class="flex items-start gap-3.5">
			<div class="h-10 w-10 rounded-lg bg-linear-to-br from-primary/12 to-primary/4 flex items-center justify-center shrink-0 border border-primary/8 mt-0.5">
				<span class="text-xs font-bold text-primary/70 uppercase tracking-wide">{initials}</span>
			</div>

			<div class="flex-1 min-w-0">
				<div class="flex items-center gap-2 flex-wrap">
					<div class="flex items-baseline min-w-0">
						{#if showNamespace}
							<span class="text-[13px] text-muted-foreground">{repo.namespace}</span>
							<span class="text-muted-foreground/30 mx-0.5">/</span>
						{/if}
						<span class="font-semibold text-sm group-hover:text-primary transition-colors">{repo.name}</span>
					</div>
					<Badge
						variant="outline"
						class="text-[10px] shrink-0 gap-0.5 py-0 h-4.5 {isPrivate
							? 'border-amber-500/30 text-amber-600 dark:text-amber-400'
							: 'border-primary/20 dark:text-primary/70'}"
					>
						{#if isPrivate}
							<Lock class="h-2.5 w-2.5" />Private
						{:else}
							<Eye class="h-2.5 w-2.5" />Public
						{/if}
					</Badge>
					{#if repo.isArchived}
						<Badge variant="outline" class="text-[10px] shrink-0 gap-0.5 py-0 h-4.5 text-muted-foreground">
							<Archive class="h-2.5 w-2.5" />Archived
						</Badge>
					{/if}
					{#if repo.type === GitRepoType.MIRROR}
						<MirrorBadge
							label="Mirror"
							error={repo.mirrorLastError}
							title={repo.mirror?.upstream ?? ''}
							syncing={repo.mirrorSyncing || mirrorSyncStore.syncing('git', repo.id)}
						/>
					{:else if repo.type === GitRepoType.FORK}
						<Badge variant="outline" class="text-[10px] shrink-0 gap-0.5 py-0 h-4.5 text-muted-foreground" title={repo.forkOf}>
							<GitFork class="h-2.5 w-2.5" />Fork
						</Badge>
					{:else if repo.type === GitRepoType.TEMPLATE}
						<Badge variant="outline" class="text-[10px] shrink-0 gap-0.5 py-0 h-4.5 border-violet-500/30 text-violet-600 dark:text-violet-400">
							<LayoutTemplate class="h-2.5 w-2.5" />Template
						</Badge>
					{/if}
				</div>

				{#if repo.description}
					<p class="text-[13px] text-muted-foreground/80 truncate mt-1">{repo.description}</p>
				{/if}

				{#if repo.topics.length > 0}
					<div class="flex flex-wrap gap-1 mt-1.5">
						{#each repo.topics.slice(0, 6) as topic (topic)}
							<span class="rounded-full bg-primary/8 text-primary/80 px-2 py-px text-[11px]">{topic}</span>
						{/each}
					</div>
				{/if}

				<div class="flex flex-wrap items-center gap-x-3.5 gap-y-1 mt-2 text-[12px] text-muted-foreground/60">
					{#if stars > 0}
						<span class="flex items-center gap-1 tabular-nums">
							<Star class="h-3 w-3 {repo.isStarred ? 'fill-amber-400 text-amber-400' : ''}" />{stars.toLocaleString()}
						</span>
					{/if}
					{#if repo.forkCount > 0}
						<span class="flex items-center gap-1 tabular-nums">
							<GitFork class="h-3 w-3" />{repo.forkCount}
						</span>
					{/if}
					{#if issues > 0}
						<span class="flex items-center gap-1 tabular-nums">
							<CircleDot class="h-3 w-3" />{issues.toLocaleString()}
						</span>
					{/if}
					{#if Number(repo.sizeBytes) > 0}
						<span class="flex items-center gap-1">
							<HardDrive class="h-3 w-3" />{formatBytes(Number(repo.sizeBytes))}
						</span>
					{/if}
					{#if pushed}
						<span class="flex items-center gap-1">
							<Clock class="h-3 w-3" />{pushed}
						</span>
					{/if}
				</div>
			</div>
		</div>
	</div>
</a>
