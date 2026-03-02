<script lang="ts">
	import { Package, Lock, Eye, ArrowDown } from '@lucide/svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Visibility } from '$lib/proto/distroface/v1/types_pb';
	import { authStore } from '$lib/stores/auth.svelte';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import type { Repository } from '$lib/proto/distroface/v1/types_pb';

	let { repo }: { repo: Repository } = $props();

	const isPrivate = $derived(repo.visibility === Visibility.PRIVATE);
	const isOwner = $derived(!!authStore.user && repo.ownerId === authStore.user.id);
	const lastPushed = $derived(
		repo.lastPushedAt ? relativeTime(timestampDate(repo.lastPushedAt)) : null
	);
</script>

<a href="/{repo.namespace}/{repo.name}" class="block group">
	<div class="flex items-center gap-4 rounded-xl border border-border/60 bg-card px-4 py-3.5 transition-all hover:border-primary/20 hover:shadow-sm">
		<div class="h-9 w-9 rounded-lg bg-muted/80 flex items-center justify-center shrink-0">
			<Package class="h-4.5 w-4.5 text-muted-foreground" />
		</div>
		<div class="flex-1 min-w-0">
			<div class="flex items-center gap-2">
				<span class="font-medium text-sm truncate group-hover:text-primary transition-colors">{repo.fullName}</span>
				<Badge
					variant="outline"
					class="text-[10px] shrink-0 gap-0.5 {isPrivate ? 'border-amber-500/30 text-amber-600 dark:text-amber-400' : ''}"
				>
					{#if isPrivate}
						<Lock class="h-2.5 w-2.5" />Private
					{:else}
						<Eye class="h-2.5 w-2.5" />Public
					{/if}
				</Badge>
				{#if isOwner}
					<Badge variant="outline" class="text-[10px] shrink-0 gap-0.5">Owner</Badge>
				{/if}
			</div>
			{#if repo.description}
				<p class="text-[13px] text-muted-foreground truncate mt-0.5">{repo.description}</p>
			{/if}
		</div>
		<div class="flex items-center gap-4 text-xs text-muted-foreground shrink-0">
			{#if repo.pullCount > 0}
				<span class="flex items-center gap-1 tabular-nums">
					<ArrowDown class="h-3 w-3" />{repo.pullCount}
				</span>
			{/if}
			{#if lastPushed}
				<span class="hidden sm:inline">{lastPushed}</span>
			{/if}
		</div>
	</div>
</a>
