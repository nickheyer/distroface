<script lang="ts">
	import type { Descriptor, HistoryEntry } from '$lib/proto/distroface/v1/types_pb';
	import { formatBytes, truncateDigest } from '$lib/utils';
	import CopyButton from './copy-button.svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { ChevronRight } from '@lucide/svelte';

	let {
		descriptor,
		loading = false,
		selectedDigest,
		onSelectChild,
		historyEntry
	}: {
		descriptor?: Descriptor;
		loading?: boolean;
		selectedDigest?: string;
		onSelectChild?: (child: Descriptor) => void;
		historyEntry?: HistoryEntry;
	} = $props();

	function descriptorKind(mediaType: string): string {
		if (!mediaType) return 'Blob';
		if (mediaType.includes('manifest.list') || mediaType.includes('image.index')) return 'Index';
		if (mediaType.includes('manifest')) return 'Manifest';
		if (mediaType.includes('container.image.v1') || mediaType.includes('config')) return 'Config';
		if (mediaType.includes('layer') || mediaType.includes('diff.tar')) return 'Layer';
		return 'Blob';
	}

	function kindColor(kind: string): string {
		switch (kind) {
			case 'Index': return 'border-blue-500/30 text-blue-600 dark:text-blue-400';
			case 'Manifest': return 'border-purple-500/30 text-purple-600 dark:text-purple-400';
			case 'Config': return 'border-amber-500/30 text-amber-600 dark:text-amber-400';
			case 'Layer': return 'border-green-500/30 text-green-600 dark:text-green-400';
			default: return '';
		}
	}
</script>

{#if loading}
	<div class="px-6 py-5 space-y-4">
		<Skeleton class="h-6 w-32" />
		<Skeleton class="h-5 w-full" />
		<Skeleton class="h-5 w-3/4" />
		<Skeleton class="h-5 w-1/2" />
		<Skeleton class="h-24 w-full mt-4" />
	</div>
{:else if descriptor}
	{@const kind = descriptorKind(descriptor.mediaType)}
	{@const cfg = descriptor.imageConfig}
	<div class="flex flex-col h-full overflow-x-hidden">
		<div class="px-6 py-5 space-y-4 shrink-0">
			<Badge variant="outline" class="text-xs {kindColor(kind)}">
				{kind}
			</Badge>

			<div class="divide-y divide-border/30">
				<div class="detail-row">
					<span class="detail-label">Digest</span>
					<div class="detail-value flex items-center gap-1 min-w-0">
						<code class="font-mono truncate">{descriptor.digest}</code>
						<CopyButton text={descriptor.digest} label="Copied!" />
					</div>
				</div>

				<div class="detail-row">
					<span class="detail-label">Media Type</span>
					<code class="detail-value font-mono text-muted-foreground truncate">{descriptor.mediaType}</code>
				</div>

				<div class="detail-row">
					<span class="detail-label">{kind === 'Manifest' || kind === 'Layer' ? 'Compressed' : 'Size'}</span>
					<span class="detail-value tabular-nums">{formatBytes(Number(descriptor.sizeBytes))}</span>
				</div>

				{#if descriptor.platform?.architecture}
					<div class="detail-row">
						<span class="detail-label">Platform</span>
						<span class="detail-value font-mono">
							{descriptor.platform.os ?? ''}{#if descriptor.platform.os && descriptor.platform.architecture}/{/if}{descriptor.platform.architecture}{#if descriptor.platform.variant}/{descriptor.platform.variant}{/if}
						</span>
					</div>
				{/if}

				{#if descriptor.artifactType}
					<div class="detail-row">
						<span class="detail-label">Artifact Type</span>
						<code class="detail-value font-mono text-muted-foreground truncate">{descriptor.artifactType}</code>
					</div>
				{/if}
			</div>

			{#if descriptor.annotations && Object.keys(descriptor.annotations).length > 0}
				<div class="rounded-xl border border-border/60 overflow-hidden">
					<div class="px-4 py-2.5 bg-muted/30 border-b border-border/40">
						<span class="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Annotations</span>
					</div>
					<div class="divide-y divide-border/20 overflow-x-auto">
						{#each Object.entries(descriptor.annotations) as [key, value]}
							<div class="px-4 py-2 flex gap-3 text-sm">
								<code class="font-mono text-muted-foreground/60 shrink-0">{key}</code>
								<code class="font-mono whitespace-nowrap">{value}</code>
							</div>
						{/each}
					</div>
				</div>
			{/if}
		</div>

		<!-- Layer context from parent manifest's build history -->
		{#if historyEntry && kind === 'Layer'}
			<div class="border-t border-border/40 shrink-0 px-6 py-4 space-y-3">
				<span class="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Layer Details</span>
				<div class="detail-row items-start">
					<span class="detail-label pt-0.5">Created By</span>
					<code class="detail-value font-mono break-all leading-relaxed">{historyEntry.createdBy}</code>
				</div>
			</div>
		{/if}

		<!-- Build history / layers -->
		{#if cfg && cfg.history.length > 0 && kind === 'Manifest'}
			<div class="border-t border-border/40 shrink-0">
				<div class="flex items-center justify-between px-6 py-3">
					<span class="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Layers</span>
					<Badge variant="secondary" class="text-xs">{cfg.history.length}</Badge>
				</div>
				<div class="divide-y divide-border/20">
					{#each cfg.history as entry, i}
						<div class="px-6 py-2.5 flex items-start gap-3 {entry.emptyLayer ? 'opacity-50' : ''}">
							<span class="text-sm tabular-nums text-muted-foreground/60 w-6 shrink-0 text-right pt-0.5">{i}</span>
							<code class="text-sm font-mono flex-1 min-w-0 break-all leading-relaxed">{entry.createdBy}</code>
							<span class="text-sm tabular-nums text-muted-foreground/60 shrink-0 pt-0.5">
								{entry.emptyLayer ? '0 B' : formatBytes(Number(entry.sizeBytes))}
							</span>
						</div>
					{/each}
				</div>
			</div>
		{/if}

		<!-- Runtime config -->
		{#if cfg && (cfg.cmd.length > 0 || cfg.entrypoint.length > 0 || cfg.env.length > 0)}
			<div class="border-t border-border/40 shrink-0 px-6 py-4 space-y-3">
				<span class="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Configuration</span>
				<div class="divide-y divide-border/30">
					{#if cfg.entrypoint.length > 0}
						<div class="detail-row">
							<span class="detail-label">Entrypoint</span>
							<code class="detail-value font-mono truncate">{JSON.stringify(cfg.entrypoint)}</code>
						</div>
					{/if}
					{#if cfg.cmd.length > 0}
						<div class="detail-row">
							<span class="detail-label">Cmd</span>
							<code class="detail-value font-mono truncate">{JSON.stringify(cfg.cmd)}</code>
						</div>
					{/if}
					{#if cfg.workingDir}
						<div class="detail-row">
							<span class="detail-label">Working Dir</span>
							<code class="detail-value font-mono">{cfg.workingDir}</code>
						</div>
					{/if}
					{#if cfg.env.length > 0}
						<div class="detail-row items-start">
							<span class="detail-label pt-0.5">Env</span>
							<div class="detail-value space-y-0.5">
								{#each cfg.env as e}
									<code class="font-mono text-sm block truncate">{e}</code>
								{/each}
							</div>
						</div>
					{/if}
					{#if cfg.exposedPorts.length > 0}
						<div class="detail-row">
							<span class="detail-label">Ports</span>
							<code class="detail-value font-mono">{cfg.exposedPorts.join(', ')}</code>
						</div>
					{/if}
					{#if cfg.volumes.length > 0}
						<div class="detail-row">
							<span class="detail-label">Volumes</span>
							<code class="detail-value font-mono">{cfg.volumes.join(', ')}</code>
						</div>
					{/if}
				</div>
				{#if cfg.labels && Object.keys(cfg.labels).length > 0}
					<div class="rounded-xl border border-border/60 overflow-hidden">
						<div class="px-4 py-2.5 bg-muted/30 border-b border-border/40">
							<span class="text-xs font-semibold text-muted-foreground uppercase tracking-wider">Labels</span>
						</div>
						<div class="divide-y divide-border/20">
							{#each Object.entries(cfg.labels) as [key, value]}
								<div class="px-4 py-2 flex gap-3 text-sm">
									<code class="font-mono text-muted-foreground/60 truncate shrink-0 max-w-[40%]">{key}</code>
									<code class="font-mono truncate flex-1">{value}</code>
								</div>
							{/each}
						</div>
					</div>
				{/if}
			</div>
		{/if}

		<!-- Child references -->
		{#if descriptor.children.length > 0}
			<div class="border-t border-border/40 flex flex-col flex-1 min-h-0">
				<div class="flex items-center justify-between px-6 py-3 shrink-0">
					<span class="text-xs font-semibold text-muted-foreground uppercase tracking-wider">References</span>
					<Badge variant="secondary" class="text-xs">{descriptor.children.length}</Badge>
				</div>
				<div class="overflow-y-auto flex-1">
					{#each descriptor.children as child}
						{@const ck = descriptorKind(child.mediaType)}
						<!-- svelte-ignore a11y_click_events_have_key_events a11y_no_static_element_interactions -->
						<div
							class="px-6 py-3 flex items-center gap-3 cursor-pointer transition-colors border-t border-border/30
								{selectedDigest === child.digest ? 'bg-muted/50 border-l-2 border-l-primary' : 'hover:bg-muted/30'}"
							onclick={() => onSelectChild?.(child)}
						>
							<Badge variant="outline" class="text-xs shrink-0 {kindColor(ck)}">
								{ck}
							</Badge>
							<span class="font-mono text-sm truncate flex-1 text-muted-foreground min-w-0">{truncateDigest(child.digest, 16)}</span>

							{#if child.platform?.architecture}
								<Badge variant="outline" class="text-xs font-mono shrink-0">
									{child.platform.os ?? ''}{#if child.platform.os && child.platform.architecture}/{/if}{child.platform.architecture}{#if child.platform.variant}/{child.platform.variant}{/if}
								</Badge>
							{/if}

							<span class="text-sm tabular-nums text-muted-foreground/60 shrink-0">{formatBytes(Number(child.sizeBytes))}</span>
							<ChevronRight class="h-4 w-4 text-muted-foreground/30 shrink-0" />
						</div>
					{/each}
				</div>
			</div>
		{/if}
	</div>
{/if}
