<script lang="ts">
	import { resolve } from '$app/paths';
	import { Tag, Rocket, FileArchive, Package, Container, ChevronDown, ChevronUp, Download, GitCommit } from '@lucide/svelte';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import Markdown from './markdown.svelte';
	import CommitBadge from './commit-badge.svelte';
	import type { Release } from '$lib/proto/distroface/v1/release_pb';
	import { pullLine, primeDownload } from '$lib/releases';
	import { formatBytes, relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { SvelteSet } from 'svelte/reactivity';

	let {
		releases,
		namespace,
		name,
		latestTag = '',
		loading = false,
		emptyMessage = 'No releases yet'
	}: {
		releases: Release[];
		namespace: string;
		name: string;
		latestTag?: string;
		loading?: boolean;
		emptyMessage?: string;
	} = $props();

	const expanded = new SvelteSet<string>();

	function when(r: Release): Date | null {
		const ts = r.publishedAt ?? r.createdAt;
		return ts ? timestampDate(ts) : null;
	}

	function isLong(r: Release): boolean {
		return r.body.length > 700 || r.body.split('\n').length > 14;
	}

	function toggle(id: string) {
		if (expanded.has(id)) expanded.delete(id);
		else expanded.add(id);
	}

</script>

{#if loading && releases.length === 0}
	<div class="space-y-4">
		{#each { length: 3 }, i (i)}
			<Skeleton class="h-40 w-full rounded-xl" />
		{/each}
	</div>
{:else if releases.length === 0}
	<EmptyState icon={Rocket} message={emptyMessage} description="Releases bundle a tagged version with notes, assets, and images." />
{:else}
	<div class="space-y-4 transition-opacity {loading ? 'opacity-60' : ''}">
		{#each releases as r (r.id)}
			{@const date = when(r)}
			{@const isLatest = !!latestTag && r.tag === latestTag}
			{@const long = isLong(r)}
			{@const open = expanded.has(r.id)}
			<article class="rounded-xl border bg-card overflow-hidden {isLatest ? 'border-emerald-500/40' : 'border-border/60'}">
				<header class="flex items-start gap-3 px-5 py-4 border-b border-border/40 bg-muted/20">
					<div class="h-9 w-9 rounded-lg bg-primary/10 flex items-center justify-center shrink-0">
						<Tag class="h-4 w-4 text-primary" />
					</div>
					<div class="min-w-0 flex-1">
						<div class="flex items-center gap-2 flex-wrap">
							<a href={resolve('/[namespace]/[name]/releases/[tag]', { namespace, name, tag: encodeURIComponent(r.tag) })} class="text-[15px] font-semibold hover:text-primary truncate min-w-0">{r.name || r.tag}</a>
							{#if isLatest}
								<Badge variant="outline" class="text-[10px] h-4.5 py-0 border-emerald-500/30 text-emerald-600 dark:text-emerald-400">Latest</Badge>
							{/if}
							{#if r.draft}
								<Badge variant="outline" class="text-[10px] h-4.5 py-0 text-muted-foreground">Draft</Badge>
							{/if}
							{#if r.prerelease}
								<Badge variant="outline" class="text-[10px] h-4.5 py-0 border-amber-500/30 text-amber-600 dark:text-amber-400">Pre-release</Badge>
							{/if}
						</div>
						<p class="text-[12.5px] text-muted-foreground mt-1 flex items-center gap-2 flex-wrap">
							<a href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', { namespace, name, ref: encodeURIComponent(r.tag), path: '' })} class="font-mono hover:text-primary">{r.tag}</a>
							{#if r.targetSha}
								<CommitBadge sha={r.targetSha} {namespace} {name} copy={false} />
							{/if}
							{#if r.author}
								<span>by <a href={resolve('/[namespace]', { namespace: r.author })} class="font-medium text-foreground/80 hover:text-primary">{r.author}</a></span>
							{/if}
							{#if date}
								<span title={date.toLocaleString()}>{r.draft ? 'saved' : 'published'} {relativeTime(date)}</span>
							{/if}
						</p>
					</div>
				</header>

				{#if r.body.trim()}
					<div class="px-5 py-4 min-w-0 overflow-x-auto">
						<div class="relative {long && !open ? 'max-h-56 overflow-hidden' : ''}">
							<Markdown source={r.body} class="text-[13.5px]" />
							{#if long && !open}
								<div class="absolute inset-x-0 bottom-0 h-16 bg-gradient-to-t from-card to-transparent pointer-events-none"></div>
							{/if}
						</div>
						{#if long}
							<button type="button" class="mt-2 inline-flex items-center gap-1 text-[12.5px] font-medium text-primary hover:underline" onclick={() => toggle(r.id)}>
								{#if open}
									<ChevronUp class="h-3.5 w-3.5" />Show less
								{:else}
									<ChevronDown class="h-3.5 w-3.5" />Show more
								{/if}
							</button>
						{/if}
					</div>
				{/if}

				<div class="px-5 py-4 border-t border-border/40 grid gap-4 md:grid-cols-2">
					{#if r.assets.length > 0}
						<section class="space-y-1.5 min-w-0">
							<h4 class="text-[11.5px] font-semibold uppercase tracking-wide text-muted-foreground flex items-center gap-1.5">
								<Package class="h-3.5 w-3.5" />Assets <span class="tabular-nums font-normal">({r.assets.length})</span>
							</h4>
							<ul class="space-y-1">
								{#each r.assets as a (a.artifactId)}
									<li class="flex items-center gap-2 text-[13px] min-w-0">
										<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
										<a href={a.downloadUrl} download={a.name} onclick={primeDownload} class="inline-flex items-center gap-1.5 font-medium text-primary hover:underline truncate min-w-0">
											<Download class="h-3.5 w-3.5 shrink-0" />{a.name}
										</a>
										<span class="text-[12px] text-muted-foreground tabular-nums shrink-0">{formatBytes(Number(a.size))}</span>
									</li>
								{/each}
							</ul>
						</section>
					{/if}
					{#if r.images.length > 0}
						<section class="space-y-1.5 min-w-0">
							<h4 class="text-[11.5px] font-semibold uppercase tracking-wide text-muted-foreground flex items-center gap-1.5">
								<Container class="h-3.5 w-3.5" />Images <span class="tabular-nums font-normal">({r.images.length})</span>
							</h4>
							<ul class="space-y-1.5">
								{#each r.images as img (`${img.fullName}:${img.tag}`)}
									{@const line = pullLine(img)}
									<li class="rounded-md bg-muted/30 border border-border/40 pl-2.5 pr-1 py-1 flex items-center gap-2 min-w-0">
										<code class="text-[12px] font-mono text-muted-foreground flex-1 min-w-0 truncate select-all">{line}</code>
										<CopyButton text={line} label="Pull command copied!" />
									</li>
								{/each}
							</ul>
						</section>
					{/if}
					<section class="space-y-1.5 md:col-span-2">
						<h4 class="text-[11.5px] font-semibold uppercase tracking-wide text-muted-foreground flex items-center gap-1.5">
							<GitCommit class="h-3.5 w-3.5" />Source
						</h4>
						<div class="flex items-center gap-4 text-[12.5px] flex-wrap">
							<a href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', { namespace, name, ref: encodeURIComponent(r.tag), path: '' })} class="inline-flex items-center gap-1 font-medium text-primary hover:underline">
								<Tag class="h-3.5 w-3.5" />Browse {r.tag}
							</a>
							{#if r.zipUrl}
								<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
								<a href={r.zipUrl} class="inline-flex items-center gap-1 font-medium text-primary hover:underline">
									<FileArchive class="h-3.5 w-3.5" />Source code (zip)
								</a>
							{/if}
							{#if r.tarUrl}
								<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
								<a href={r.tarUrl} class="inline-flex items-center gap-1 font-medium text-primary hover:underline">
									<FileArchive class="h-3.5 w-3.5" />Source code (tar.gz)
								</a>
							{/if}
						</div>
					</section>
				</div>
			</article>
		{/each}
	</div>
{/if}
