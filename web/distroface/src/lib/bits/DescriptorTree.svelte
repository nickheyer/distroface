<script lang="ts">
	import DescriptorTree from '$lib/bits/DescriptorTree.svelte';
	import type { Descriptor } from '$lib/proto/distroface/v1/types_pb';
	import { digestShort, fmtBytes } from '$lib/fmt';
	import Copy from '$lib/bits/Copy.svelte';

	let { d, depth = 0 }: { d: Descriptor; depth?: number } = $props();

	// Trailing word of the media type reads as the kind
	const kind = $derived.by(() => {
		const mt = d.mediaType;
		if (mt.includes('image.index') || mt.includes('manifest.list')) return 'index';
		if (mt.includes('manifest')) return 'manifest';
		if (mt.includes('config')) return 'config';
		if (mt.includes('layer') || mt.includes('tar')) return 'layer';
		return 'blob';
	});

	const platform = $derived(
		d.platform ? [d.platform.os, d.platform.architecture, d.platform.variant].filter(Boolean).join('/') : ''
	);
</script>

<div class="entry" style="margin-left: {depth * 1.4}rem">
	<span class="caps" class:soft={kind === 'layer' || kind === 'blob' || kind === 'config'}>{kind}</span>
	<span class="mono" title={d.digest}>{digestShort(d.digest)}</span>
	<Copy text={d.digest} />
	{#if platform}
		<span class="mono faint">{platform}</span>
	{/if}
	<span class="mono faint">{fmtBytes(d.sizeBytes)}</span>
	{#if d.artifactType}
		<span class="mono faint" title="artifact type">{d.artifactType}</span>
	{/if}
</div>
{#each d.children as child (child.digest)}
	<DescriptorTree d={child} depth={depth + 1} />
{/each}

<style>
	.entry {
		display: flex;
		gap: 0.8rem;
		align-items: baseline;
		padding: 0.28rem 0;
		border-bottom: 1px solid var(--hairline);
		flex-wrap: wrap;
	}
</style>
