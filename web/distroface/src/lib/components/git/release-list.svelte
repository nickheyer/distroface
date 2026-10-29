<script lang="ts">
	import { resolve } from '$app/paths';
	import {
		Tag,
		Rocket,
		FileArchive,
		Package,
		Container,
		ChevronDown,
		ChevronUp,
		Download,
		GitCommit
	} from '$lib/icons';
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
			<div class="box">
				<div class="flex items-center gap-3 border-b bg-muted/40 px-4 py-2.5">
					<Skeleton class="size-4" />
					<Skeleton class="h-4 w-40" />
					<Skeleton class="ml-auto h-3.5 w-20" />
				</div>
				<div class="space-y-2.5 px-4 py-4">
					<Skeleton class="h-4 w-full" />
					<Skeleton class="h-4 w-5/6" />
					<Skeleton class="h-4 w-2/3" />
				</div>
			</div>
		{/each}
	</div>
{:else if releases.length === 0}
	<EmptyState
		icon={Rocket}
		message={emptyMessage}
		description="Releases bundle a tagged version with notes, assets, and images."
	/>
{:else}
	<div class="space-y-4 transition-opacity {loading ? 'opacity-60' : ''}">
		{#each releases as r (r.id)}
			{@const date = when(r)}
			{@const isLatest = !!latestTag && r.tag === latestTag}
			{@const long = isLong(r)}
			{@const open = expanded.has(r.id)}
			<article class="box">
				<header class="box-header">
					<div class="min-w-0 flex-1">
						<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
							<Tag class="size-4 shrink-0 text-muted-foreground" />
							<a
								href={resolve('/[namespace]/[name]/releases/[tag]', {
									namespace,
									name,
									tag: encodeURIComponent(r.tag)
								})}
								class="min-w-0 truncate hover:text-primary">{r.name || r.tag}</a
							>
							{#if isLatest}
								<Badge variant="success">Latest</Badge>
							{/if}
							{#if r.draft}
								<Badge variant="outline">Draft</Badge>
							{/if}
							{#if r.prerelease}
								<Badge variant="warning">Pre-release</Badge>
							{/if}
						</div>
						<div
							class="mt-1 flex flex-wrap items-center gap-2 text-xs font-normal text-muted-foreground"
						>
							<a
								href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', {
									namespace,
									name,
									ref: encodeURIComponent(r.tag),
									path: ''
								})}
								class="font-mono hover:text-primary">{r.tag}</a
							>
							{#if r.targetSha}
								<CommitBadge sha={r.targetSha} {namespace} {name} copy={false} />
							{/if}
							{#if r.author}
								<span
									>by <a
										href={resolve('/[namespace]', { namespace: r.author })}
										class="font-medium text-foreground hover:text-primary">{r.author}</a
									></span
								>
							{/if}
						</div>
					</div>
					{#if date}
						<span
							class="shrink-0 text-xs font-normal text-muted-foreground tabular-nums"
							title={date.toLocaleString()}
							>{r.draft ? 'Saved' : 'Published'} {relativeTime(date)}</span
						>
					{/if}
				</header>

				{#if r.body.trim()}
					<div class="min-w-0 overflow-x-auto px-4 py-4">
						<div class={long && !open ? 'max-h-56 overflow-hidden' : ''}>
							<Markdown source={r.body} />
						</div>
						{#if long}
							<button
								type="button"
								class="link mt-2 inline-flex items-center gap-1 text-xs font-medium"
								onclick={() => toggle(r.id)}
							>
								{#if open}
									<ChevronUp class="size-3.5" />Show less
								{:else}
									<ChevronDown class="size-3.5" />Show more
								{/if}
							</button>
						{/if}
					</div>
				{/if}

				<div class="grid gap-4 border-t px-4 py-4 md:grid-cols-2">
					{#if r.assets.length > 0}
						<section class="min-w-0 space-y-1.5">
							<h4 class="eyebrow flex items-center gap-1.5">
								<Package class="size-3.5" />Assets
								<span class="tabular-nums">({r.assets.length})</span>
							</h4>
							<ul class="divide-y">
								{#each r.assets as a (a.id)}
									<li class="flex min-w-0 items-center gap-2 py-1.5 text-sm">
										<!-- eslint-disable svelte/no-navigation-without-resolve -->
										<a
											href={a.downloadUrl}
											download={a.name}
											onclick={() => primeDownload(namespace, name)}
											class="link inline-flex min-w-0 items-center gap-1.5 truncate font-medium"
										>
											<Download class="size-3.5 shrink-0" />{a.label || a.name}
										</a>
										<!-- eslint-enable svelte/no-navigation-without-resolve -->
										<span class="shrink-0 text-xs text-muted-foreground tabular-nums"
											>{formatBytes(Number(a.size))}</span
										>
									</li>
								{/each}
							</ul>
						</section>
					{/if}
					{#if r.images.length > 0}
						<section class="min-w-0 space-y-1.5">
							<h4 class="eyebrow flex items-center gap-1.5">
								<Container class="size-3.5" />Images
								<span class="tabular-nums">({r.images.length})</span>
							</h4>
							<ul class="space-y-1.5">
								{#each r.images as img (`${img.fullName}:${img.tag}`)}
									{@const line = pullLine(img)}
									<li
										class="flex min-w-0 items-center gap-2 rounded-md border bg-muted py-0.5 pr-1 pl-2.5"
									>
										<code
											class="min-w-0 flex-1 truncate font-mono text-xs text-muted-foreground select-all"
											>{line}</code
										>
										<CopyButton text={line} label="Pull command copied" />
									</li>
								{/each}
							</ul>
						</section>
					{/if}
					<section class="space-y-1.5 md:col-span-2">
						<h4 class="eyebrow flex items-center gap-1.5">
							<GitCommit class="size-3.5" />Source
						</h4>
						<div class="flex flex-wrap items-center gap-4 text-xs">
							<a
								href={resolve('/[namespace]/[name]/tree/[ref]/[...path]', {
									namespace,
									name,
									ref: encodeURIComponent(r.tag),
									path: ''
								})}
								class="link inline-flex items-center gap-1 font-medium"
							>
								<Tag class="size-3.5" />Browse {r.tag}
							</a>
							{#if r.zipUrl}
								<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
								<a href={r.zipUrl} class="link inline-flex items-center gap-1 font-medium">
									<FileArchive class="size-3.5" />Source code (zip)
								</a>
							{/if}
							{#if r.tarUrl}
								<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
								<a href={r.tarUrl} class="link inline-flex items-center gap-1 font-medium">
									<FileArchive class="size-3.5" />Source code (tar.gz)
								</a>
							{/if}
						</div>
					</section>
				</div>
			</article>
		{/each}
	</div>
{/if}
