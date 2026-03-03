<script lang="ts">
	import { Lock, Eye, ArrowDown, Tags, HardDrive, Clock } from '@lucide/svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Visibility } from '$lib/proto/distroface/v1/types_pb';
	import { authStore } from '$lib/stores/auth.svelte';
	import { relativeTime, formatBytes } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import type { Repository } from '$lib/proto/distroface/v1/types_pb';

	let { repo }: { repo: Repository } = $props();

	const isPrivate = $derived(repo.visibility === Visibility.PRIVATE);
	const isOwner = $derived(!!authStore.user && repo.ownerId === authStore.user.id);
	const lastPushed = $derived(
		repo.lastPushedAt ? relativeTime(timestampDate(repo.lastPushedAt)) : null
	);
	const initials = $derived(repo.namespace.slice(0, 2).toUpperCase());
	const hasStats = $derived(
		repo.tagCount > 0 || Number(repo.sizeBytes) > 0 || repo.pullCount > 0n || lastPushed
	);
</script>

<a href="/{repo.namespace}/{repo.name}" class="block group">
	<div class="rounded-xl border border-border/60 bg-card px-4 py-3.5 transition-all hover:border-primary/20 hover:shadow-[0_2px_12px_-4px] hover:shadow-primary/8">
		<div class="flex items-start gap-3.5">
			<div class="h-10 w-10 rounded-lg bg-linear-to-br from-primary/12 to-primary/4 flex items-center justify-center shrink-0 border border-primary/8 mt-0.5">
				<span class="text-xs font-bold text-primary/70 uppercase tracking-wide">{initials}</span>
			</div>

			<div class="flex-1 min-w-0">
				<div class="flex items-center gap-2 flex-wrap">
					<div class="flex items-baseline min-w-0">
						<span class="text-[13px] text-muted-foreground">{repo.namespace}</span>
						<span class="text-muted-foreground/30 mx-0.5">/</span>
						<span class="font-semibold text-sm group-hover:text-primary transition-colors">{repo.name}</span>
					</div>
					<Badge
						variant="outline"
						class="text-[10px] shrink-0 gap-0.5 py-0 h-4.5sPrivate
							? 'border-amber-500/30 text-amber-600 dark:text-amber-400'
							: 'border-primary/20 dark:text-primary/70'}"
					>
						{#if isPrivate}
							<Lock class="h-2.5 w-2.5" />Private
						{:else}
							<Eye class="h-2.5 w-2.5" />Public
						{/if}
					</Badge>
					{#if isOwner}
						<Badge variant="secondary" class="text-[10px] shrink-0 py-0 h-4.5">Owner</Badge>
					{/if}
				</div>

				{#if repo.description}
					<p class="text-[13px] text-muted-foreground/80 truncate mt-1">{repo.description}</p>
				{/if}

				{#if hasStats}
					<div class="flex items-center gap-3.5 mt-2 text-[12px] text-muted-foreground/60">
						{#if repo.tagCount > 0}
							<span class="flex items-center gap-1">
								<Tags class="h-3 w-3" />
								{repo.tagCount} tag{repo.tagCount !== 1 ? 's' : ''}
							</span>
						{/if}
						{#if Number(repo.sizeBytes) > 0}
							<span class="flex items-center gap-1">
								<HardDrive class="h-3 w-3" />
								{formatBytes(Number(repo.sizeBytes))}
							</span>
						{/if}
						{#if repo.pullCount > 0n}
							<span class="flex items-center gap-1 tabular-nums">
								<ArrowDown class="h-3 w-3" />
								{repo.pullCount.toLocaleString()} pull{repo.pullCount !== 1n ? 's' : ''}
							</span>
						{/if}
						{#if lastPushed}
							<span class="items-center gap-1 hidden sm:flex">
								<Clock class="h-3 w-3" />{lastPushed}
							</span>
						{/if}
					</div>
				{/if}
			</div>
		</div>
	</div>
</a>
