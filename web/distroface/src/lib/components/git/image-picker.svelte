<script lang="ts">
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Container, Pin, Link2, Unlink, Loader2, Search } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import EmptyState from '$lib/components/empty-state.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { LinkedRepositoryKind, type LinkedRepository } from '$lib/proto/distroface/v1/git_pb';
	import type { Tag as ImageTag, Platform } from '$lib/proto/distroface/v1/types_pb';
	import type { ReleaseImage } from '$lib/proto/distroface/v1/release_pb';
	import { truncateDigest } from '$lib/utils';
	import { toast } from 'svelte-sonner';

	let {
		namespace,
		name,
		tag,
		images = [],
		disabled = false,
		onAttached,
		onDetached
	}: {
		namespace: string;
		name: string;
		tag: string;
		images?: ReleaseImage[];
		disabled?: boolean;
		onAttached?: (image: ReleaseImage) => void;
		onDetached?: (image: ReleaseImage) => void;
	} = $props();

	let linked = $state<LinkedRepository[]>([]);
	let loadingLinked = $state(true);
	let selected = $state('');
	let tags = $state<ImageTag[]>([]);
	let loadingTags = $state(false);
	let query = $state('');
	let busyKey = $state('');
	let debounce: ReturnType<typeof setTimeout> | undefined;
	let seq = 0;

	const current = $derived(linked.find((l) => l.name === selected));
	const attached = $derived(new Set(images.map((i) => keyOf(i.name, i.tag))));

	function keyOf(imageName: string, imageTag: string): string {
		return `${imageName}:${imageTag}`;
	}

	async function loadLinked() {
		loadingLinked = true;
		try {
			const resp = await rpcClient.git.listLinkedRepositories({ namespace, name }, silentCallOptions);
			linked = resp.repositories.filter((r) => r.kind === LinkedRepositoryKind.IMAGE);
			if (!selected && linked[0]) {
				selected = linked[0].name;
				await loadTags();
			}
		} catch {
			linked = [];
		} finally {
			loadingLinked = false;
		}
	}

	async function loadTags() {
		const repo = current;
		if (!repo) {
			tags = [];
			return;
		}
		const mine = ++seq;
		loadingTags = true;
		try {
			const resp = await rpcClient.repository.listTags(
				{
					namespace: repo.namespace,
					name: repo.name,
					page: { pageSize: 50, pageToken: '', query: { text: query.trim(), filters: [] }, orderBy: '' }
				},
				silentCallOptions
			);
			if (mine === seq) tags = resp.tags;
		} catch {
			if (mine === seq) tags = [];
		} finally {
			if (mine === seq) loadingTags = false;
		}
	}

	function pick(value: string) {
		selected = value;
		query = '';
		void loadTags();
	}

	function onQuery() {
		clearTimeout(debounce);
		debounce = setTimeout(loadTags, 250);
	}

	async function attach(t: ImageTag) {
		const repo = current;
		if (!repo) return;
		busyKey = keyOf(repo.name, t.name);
		try {
			const resp = await rpcClient.release.attachReleaseImage({ namespace, name, tag, imageName: repo.name, imageTag: t.name });
			toast.success('Image pinned');
			if (resp.image) onAttached?.(resp.image);
		} catch {
			// Error interceptor already toasted
		} finally {
			busyKey = '';
		}
	}

	async function detach(img: ReleaseImage) {
		busyKey = keyOf(img.name, img.tag);
		try {
			await rpcClient.release.detachReleaseImage({ namespace, name, tag, imageName: img.name, imageTag: img.tag });
			toast.success('Image unpinned');
			onDetached?.(img);
		} catch {
			// Error interceptor already toasted
		} finally {
			busyKey = '';
		}
	}

	function platformLabel(platforms: Platform[]): string {
		return platforms.map((p) => [p.os, p.architecture, p.variant].filter(Boolean).join('/')).join(', ');
	}

	onMount(loadLinked);
</script>

<div class="space-y-4">
	{#if images.length > 0}
		<section class="space-y-1.5">
			<span class="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">Attached images</span>
			<ul class="rounded-xl border border-border/60 divide-y divide-border/40 overflow-hidden">
				{#each images as img (keyOf(img.name, img.tag))}
					<li class="flex items-center gap-3 px-4 py-2.5 text-[13px] min-w-0">
						<Container class="h-3.5 w-3.5 text-muted-foreground shrink-0" />
						<div class="min-w-0 flex-1">
							<div class="flex items-center gap-2 min-w-0 flex-wrap">
								<a href={resolve('/images/[namespace]/[...name]', { namespace: img.fullName.split('/')[0], name: img.fullName.split('/').slice(1).join('/') })} class="font-mono font-medium truncate hover:text-primary">{img.fullName}:{img.tag}</a>
								{#if img.pinned}
									<Badge variant="outline" class="text-[10px] h-4.5 py-0 gap-0.5"><Pin class="h-2.5 w-2.5" />Pinned</Badge>
								{:else}
									<Badge variant="outline" class="text-[10px] h-4.5 py-0 text-muted-foreground">Tag match</Badge>
								{/if}
							</div>
							{#if img.digest}
								<p class="text-[11.5px] font-mono text-muted-foreground truncate" title={img.digest}>{truncateDigest(img.digest, 24)}</p>
							{/if}
						</div>
						{#if img.pinned && !disabled}
							<Button variant="ghost" size="sm" class="h-8 px-2 text-destructive hover:text-destructive shrink-0" disabled={busyKey === keyOf(img.name, img.tag)} onclick={() => detach(img)} title="Unpin image" aria-label="Unpin image">
								<Unlink class="h-3.5 w-3.5" />
							</Button>
						{/if}
					</li>
				{/each}
			</ul>
		</section>
	{/if}

	{#if loadingLinked}
		<Skeleton class="h-24 w-full rounded-xl" />
	{:else if linked.length === 0}
		<EmptyState icon={Container} message="No linked images" description="Push an image under {namespace}/{name} or link one from the Packages tab." />
	{:else}
		<div class="space-y-2">
			<div class="flex flex-wrap items-center gap-2">
				<Select type="single" value={selected} onValueChange={(v) => { if (v) pick(v); }}>
					<SelectTrigger class="w-full sm:w-64 h-8 text-[13px] font-mono" size="sm" aria-label="Image repository">{current?.fullName ?? 'Choose an image'}</SelectTrigger>
					<SelectContent>
						{#each linked as l (l.fullName)}
							<SelectItem value={l.name} class="font-mono text-[13px]">{l.fullName}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
				<div class="relative w-full sm:w-56">
					<Search class="h-3.5 w-3.5 text-muted-foreground absolute left-2.5 top-1/2 -translate-y-1/2" />
					<Input bind:value={query} oninput={onQuery} placeholder="Filter tags" class="h-8 pl-8" aria-label="Filter tags" />
				</div>
			</div>

			{#if loadingTags && tags.length === 0}
				<Skeleton class="h-32 w-full rounded-xl" />
			{:else if tags.length === 0}
				<p class="text-[12.5px] text-muted-foreground px-1">{query ? 'No tags match.' : 'No tags pushed yet.'}</p>
			{:else}
				<ul class="rounded-xl border border-border/60 divide-y divide-border/40 overflow-hidden transition-opacity {loadingTags ? 'opacity-60' : ''}">
					{#each tags as t (t.name)}
						{@const key = keyOf(current?.name ?? '', t.name)}
						{@const done = attached.has(key)}
						<li class="flex items-center gap-3 px-4 py-2.5 text-[13px] min-w-0">
							<div class="min-w-0 flex-1">
								<div class="flex items-center gap-2 min-w-0 flex-wrap">
									<span class="font-mono font-medium truncate">{t.name}</span>
									{#if t.name === tag}
										<Badge variant="outline" class="text-[10px] h-4.5 py-0 border-emerald-500/30 text-emerald-600 dark:text-emerald-400">Matches release</Badge>
									{/if}
								</div>
								<p class="text-[11.5px] text-muted-foreground truncate">
									<span class="font-mono" title={t.digest}>{truncateDigest(t.digest, 20)}</span>
									{#if t.platforms.length > 0}
										<span class="mx-1">on</span>{platformLabel(t.platforms)}
									{/if}
								</p>
							</div>
							<Button size="sm" variant={done ? 'outline' : 'default'} class="h-8 shrink-0" disabled={disabled || done || busyKey === key} onclick={() => attach(t)}>
								{#if busyKey === key}
									<Loader2 class="h-3.5 w-3.5 animate-spin" />
								{:else if done}
									Attached
								{:else}
									<Link2 class="h-3.5 w-3.5 mr-1" />Attach
								{/if}
							</Button>
						</li>
					{/each}
				</ul>
			{/if}
		</div>
	{/if}
</div>
