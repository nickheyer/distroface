<script lang="ts">
	import type { Descriptor, HistoryEntry } from '$lib/proto/distroface/v1/types_pb';
	import { formatBytes, truncateDigest } from '$lib/utils';
	import CopyButton from './copy-button.svelte';
	import { Badge, type BadgeVariant } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { ChevronRight } from '$lib/icons';

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

	function kindVariant(kind: string): BadgeVariant {
		switch (kind) {
			case 'Index':
				return 'default';
			case 'Manifest':
				return 'done';
			case 'Config':
				return 'warning';
			case 'Layer':
				return 'success';
			default:
				return 'outline';
		}
	}
</script>

{#if loading}
	<div class="space-y-4 px-6 py-5">
		<Skeleton class="h-5 w-20" />
		<Skeleton class="h-4 w-full" />
		<Skeleton class="h-4 w-3/4" />
		<Skeleton class="h-4 w-1/2" />
		<Skeleton class="h-24 w-full" />
	</div>
{:else if descriptor}
	{@const kind = descriptorKind(descriptor.mediaType)}
	{@const cfg = descriptor.imageConfig}
	<div class="overflow-x-hidden">
		<div class="space-y-4 px-6 py-5">
			<Badge variant={kindVariant(kind)}>{kind}</Badge>

			<div class="divide-y">
				<div class="detail-row">
					<span class="detail-label">Digest</span>
					<div class="detail-value flex min-w-0 items-center gap-1">
						<code class="truncate font-mono">{descriptor.digest}</code>
						<CopyButton text={descriptor.digest} label="Digest copied" />
					</div>
				</div>

				<div class="detail-row">
					<span class="detail-label">Media type</span>
					<code class="detail-value truncate font-mono text-muted-foreground"
						>{descriptor.mediaType}</code
					>
				</div>

				<div class="detail-row">
					<span class="detail-label"
						>{kind === 'Manifest' || kind === 'Layer' ? 'Compressed' : 'Size'}</span
					>
					<span class="detail-value tabular-nums">{formatBytes(Number(descriptor.sizeBytes))}</span>
				</div>

				{#if descriptor.platform?.architecture}
					<div class="detail-row">
						<span class="detail-label">Platform</span>
						<span class="detail-value font-mono">
							{descriptor.platform.os ??
								''}{#if descriptor.platform.os && descriptor.platform.architecture}/{/if}{descriptor
								.platform.architecture}{#if descriptor.platform.variant}/{descriptor.platform
									.variant}{/if}
						</span>
					</div>
				{/if}

				{#if descriptor.artifactType}
					<div class="detail-row">
						<span class="detail-label">Artifact type</span>
						<code class="detail-value truncate font-mono text-muted-foreground"
							>{descriptor.artifactType}</code
						>
					</div>
				{/if}
			</div>

			{#if descriptor.annotations && Object.keys(descriptor.annotations).length > 0}
				<div class="box">
					<div class="box-header">Annotations</div>
					<div class="divide-y overflow-x-auto">
						{#each Object.entries(descriptor.annotations) as [key, value] (key)}
							<div class="flex gap-3 px-4 py-2 text-xs">
								<code class="shrink-0 font-mono text-muted-foreground">{key}</code>
								<code class="font-mono whitespace-nowrap">{value}</code>
							</div>
						{/each}
					</div>
				</div>
			{/if}
		</div>

		{#if historyEntry && kind === 'Layer'}
			<div class="space-y-3 border-t px-6 py-4">
				<p class="eyebrow">Layer details</p>
				<div class="detail-row items-start">
					<span class="detail-label pt-0.5">Created by</span>
					<code class="detail-value font-mono text-xs leading-relaxed break-all"
						>{historyEntry.createdBy}</code
					>
				</div>
			</div>
		{/if}

		{#if cfg && cfg.history.length > 0 && kind === 'Manifest'}
			<div class="border-t">
				<div class="flex items-center justify-between px-6 py-3">
					<p class="eyebrow">Layers</p>
					<Badge variant="secondary">{cfg.history.length}</Badge>
				</div>
				<div class="divide-y">
					{#each cfg.history as entry, i (i)}
						<div class="flex items-start gap-3 px-6 py-2.5 {entry.emptyLayer ? 'opacity-50' : ''}">
							<span
								class="w-6 shrink-0 pt-0.5 text-right text-xs text-muted-foreground tabular-nums"
								>{i}</span
							>
							<code class="min-w-0 flex-1 font-mono text-xs leading-relaxed break-all"
								>{entry.createdBy}</code
							>
							<span class="shrink-0 pt-0.5 text-xs text-muted-foreground tabular-nums">
								{entry.emptyLayer ? '0 B' : formatBytes(Number(entry.sizeBytes))}
							</span>
						</div>
					{/each}
				</div>
			</div>
		{/if}

		{#if cfg && (cfg.cmd.length > 0 || cfg.entrypoint.length > 0 || cfg.env.length > 0)}
			<div class="space-y-3 border-t px-6 py-4">
				<p class="eyebrow">Configuration</p>
				<div class="divide-y">
					{#if cfg.entrypoint.length > 0}
						<div class="detail-row">
							<span class="detail-label">Entrypoint</span>
							<code class="detail-value truncate font-mono">{JSON.stringify(cfg.entrypoint)}</code>
						</div>
					{/if}
					{#if cfg.cmd.length > 0}
						<div class="detail-row">
							<span class="detail-label">Cmd</span>
							<code class="detail-value truncate font-mono">{JSON.stringify(cfg.cmd)}</code>
						</div>
					{/if}
					{#if cfg.workingDir}
						<div class="detail-row">
							<span class="detail-label">Working dir</span>
							<code class="detail-value font-mono">{cfg.workingDir}</code>
						</div>
					{/if}
					{#if cfg.env.length > 0}
						<div class="detail-row items-start">
							<span class="detail-label pt-0.5">Env</span>
							<div class="detail-value space-y-0.5">
								{#each cfg.env as e (e)}
									<code class="block truncate font-mono text-xs">{e}</code>
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
					<div class="box">
						<div class="box-header">Labels</div>
						<div class="divide-y">
							{#each Object.entries(cfg.labels) as [key, value] (key)}
								<div class="flex gap-3 px-4 py-2 text-xs">
									<code class="max-w-[40%] shrink-0 truncate font-mono text-muted-foreground"
										>{key}</code
									>
									<code class="flex-1 truncate font-mono">{value}</code>
								</div>
							{/each}
						</div>
					</div>
				{/if}
			</div>
		{/if}

		{#if descriptor.children.length > 0}
			<div class="border-t">
				<div class="flex items-center justify-between px-6 py-3">
					<p class="eyebrow">References</p>
					<Badge variant="secondary">{descriptor.children.length}</Badge>
				</div>
				<div class="divide-y border-t">
					{#each descriptor.children as child (child.digest)}
						{@const ck = descriptorKind(child.mediaType)}
						<button
							type="button"
							class="flex w-full items-center gap-3 px-6 py-3 text-left transition-colors {selectedDigest ===
							child.digest
								? 'border-l-2 border-l-primary bg-muted'
								: 'hover:bg-muted/30'}"
							onclick={() => onSelectChild?.(child)}
						>
							<Badge variant={kindVariant(ck)} class="shrink-0">{ck}</Badge>
							<span class="min-w-0 flex-1 truncate font-mono text-sm text-muted-foreground">
								{truncateDigest(child.digest, 16)}
							</span>

							{#if child.platform?.architecture}
								<Badge variant="outline" class="shrink-0 font-mono">
									{child.platform.os ??
										''}{#if child.platform.os && child.platform.architecture}/{/if}{child.platform
										.architecture}{#if child.platform.variant}/{child.platform.variant}{/if}
								</Badge>
							{/if}

							<span class="shrink-0 text-xs text-muted-foreground tabular-nums">
								{formatBytes(Number(child.sizeBytes))}
							</span>
							<ChevronRight class="size-4 shrink-0 text-muted-foreground/50" />
						</button>
					{/each}
				</div>
			</div>
		{/if}
	</div>
{/if}
