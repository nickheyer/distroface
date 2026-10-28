<script lang="ts">
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Container, Pin, Link, Unlink, Loader, Search } from '$lib/icons';
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
			const resp = await rpcClient.git.listLinkedRepositories(
				{ namespace, name },
				silentCallOptions
			);
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
					page: {
						pageSize: 50,
						pageToken: '',
						query: { text: query.trim(), filters: [] },
						orderBy: ''
					}
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
			const resp = await rpcClient.release.attachReleaseImage({
				namespace,
				name,
				tag,
				imageName: repo.name,
				imageTag: t.name
			});
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
			await rpcClient.release.detachReleaseImage({
				namespace,
				name,
				tag,
				imageName: img.name,
				imageTag: img.tag
			});
			toast.success('Image unpinned');
			onDetached?.(img);
		} catch {
			// Error interceptor already toasted
		} finally {
			busyKey = '';
		}
	}

	function platformLabel(platforms: Platform[]): string {
		return platforms
			.map((p) => [p.os, p.architecture, p.variant].filter(Boolean).join('/'))
			.join(', ');
	}

	onMount(loadLinked);
</script>

<div class="space-y-4">
	{#if images.length > 0}
		<section class="space-y-1.5">
			<span class="eyebrow">Attached images</span>
			<ul class="box divide-y">
				{#each images as img (keyOf(img.name, img.tag))}
					<li class="flex min-w-0 items-center gap-3 px-4 py-2.5 text-sm">
						<Container class="size-3.5 shrink-0 text-muted-foreground" />
						<div class="min-w-0 flex-1">
							<div class="flex min-w-0 flex-wrap items-center gap-2">
								<a
									href={resolve('/images/[namespace]/[...name]', {
										namespace: img.fullName.split('/')[0],
										name: img.fullName.split('/').slice(1).join('/')
									})}
									class="truncate font-mono font-medium hover:text-primary"
								>
									{img.fullName}:{img.tag}
								</a>
								{#if img.pinned}
									<Badge variant="outline"><Pin />Pinned</Badge>
								{:else}
									<Badge variant="outline">Tag match</Badge>
								{/if}
							</div>
							{#if img.digest}
								<p class="truncate font-mono text-xs text-muted-foreground" title={img.digest}>
									{truncateDigest(img.digest, 24)}
								</p>
							{/if}
						</div>
						{#if img.pinned && !disabled}
							<Button
								variant="ghost-muted"
								size="icon-sm"
								class="shrink-0 hover:text-destructive"
								disabled={busyKey === keyOf(img.name, img.tag)}
								onclick={() => detach(img)}
								title="Unpin image"
								aria-label="Unpin image"
							>
								<Unlink />
							</Button>
						{/if}
					</li>
				{/each}
			</ul>
		</section>
	{/if}

	{#if loadingLinked}
		<Skeleton class="h-24 w-full rounded-lg" />
	{:else if linked.length === 0}
		<EmptyState
			icon={Container}
			message="No linked images"
			description="Push an image under {namespace}/{name} or link one from the Packages tab."
		/>
	{:else}
		<div class="space-y-2">
			<div class="flex flex-wrap items-center gap-2">
				<Select
					type="single"
					value={selected}
					onValueChange={(v) => {
						if (v) pick(v);
					}}
				>
					<SelectTrigger class="w-full font-mono sm:w-64" aria-label="Image repository"
						>{current?.fullName ?? 'Choose an image'}</SelectTrigger
					>
					<SelectContent>
						{#each linked as l (l.fullName)}
							<SelectItem value={l.name} class="font-mono">{l.fullName}</SelectItem>
						{/each}
					</SelectContent>
				</Select>
				<div class="relative w-full sm:w-56">
					<Search
						class="absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground"
					/>
					<Input
						bind:value={query}
						oninput={onQuery}
						placeholder="Filter tags"
						class="pl-8"
						aria-label="Filter tags"
					/>
				</div>
			</div>

			{#if loadingTags && tags.length === 0}
				<Skeleton class="h-32 w-full rounded-lg" />
			{:else if tags.length === 0}
				<p class="text-sm text-muted-foreground">
					{query ? 'No tags match' : 'No tags pushed yet'}
				</p>
			{:else}
				<ul class="box divide-y transition-opacity {loadingTags ? 'opacity-60' : ''}">
					{#each tags as t (t.name)}
						{@const key = keyOf(current?.name ?? '', t.name)}
						{@const done = attached.has(key)}
						<li class="flex min-w-0 items-center gap-3 px-4 py-2.5 text-sm">
							<div class="min-w-0 flex-1">
								<div class="flex min-w-0 flex-wrap items-center gap-2">
									<span class="truncate font-mono font-medium">{t.name}</span>
									{#if t.name === tag}
										<Badge variant="success">Matches release</Badge>
									{/if}
								</div>
								<p class="truncate text-xs text-muted-foreground">
									<span class="font-mono" title={t.digest}>{truncateDigest(t.digest, 20)}</span>
									{#if t.platforms.length > 0}
										<span class="mx-1">on</span>{platformLabel(t.platforms)}
									{/if}
								</p>
							</div>
							<Button
								variant={done ? 'outline' : 'default'}
								class="shrink-0"
								disabled={disabled || done || busyKey === key}
								onclick={() => attach(t)}
							>
								{#if busyKey === key}
									<Loader class="animate-spin" />
								{:else if done}
									Attached
								{:else}
									<Link />Attach
								{/if}
							</Button>
						</li>
					{/each}
				</ul>
			{/if}
		</div>
	{/if}
</div>
