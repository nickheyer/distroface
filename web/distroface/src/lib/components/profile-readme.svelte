<script lang="ts">
	import { resolve } from '$app/paths';
	import { rpcClient, backgroundCallOptions } from '$lib/api/rpc-client';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import Markdown from '$lib/components/git/markdown.svelte';
	import { BookOpen } from '$lib/icons';
	import type { GitBlob } from '$lib/proto/distroface/v1/git_pb';

	// A repository named after the namespace lends its readme to the profile
	let {
		namespace,
		isOwn = false,
		ownerLabel = 'profile'
	}: { namespace: string; isOwn?: boolean; ownerLabel?: string } = $props();

	let readme = $state<GitBlob | undefined>();
	let loaded = $state(false);
	let loadedFor = '';

	$effect(() => {
		if (!namespace || namespace === loadedFor) return;
		loadedFor = namespace;
		loaded = false;
		readme = undefined;
		rpcClient.gitFiles
			.getReadme({ namespace, name: namespace, ref: '', path: '' }, backgroundCallOptions)
			.then((r) => (readme = r.blob))
			.catch(() => (readme = undefined))
			.finally(() => (loaded = true));
	});

	const text = $derived(readme ? new TextDecoder().decode(readme.content) : '');
	const dir = $derived(
		readme?.path.includes('/') ? readme.path.slice(0, readme.path.lastIndexOf('/')) : ''
	);
	const baseHref = $derived(`/${namespace}/${namespace}/blob/HEAD/${dir ? `${dir}/` : ''}`);
	const rawBase = $derived(`/${namespace}/${namespace}/-/raw/HEAD/${dir ? `${dir}/` : ''}`);
	const repoHref = $derived(resolve('/[namespace]/[name]', { namespace, name: namespace }));
</script>

<!-- eslint-disable svelte/no-navigation-without-resolve -->
{#if !loaded}
	<Skeleton class="h-40 w-full rounded-lg" />
{:else if readme}
	<div class="box">
		<div class="box-header">
			<a href={repoHref} class="flex min-w-0 items-center gap-2 hover:text-primary">
				<BookOpen class="size-4 text-muted-foreground" />
				<span class="truncate">{namespace}/{namespace}</span>
				<span class="text-xs font-normal text-muted-foreground">/ {readme.name}</span>
			</a>
		</div>
		<div class="overflow-x-auto px-4 py-4 sm:px-6 sm:py-5">
			<Markdown
				source={text}
				{baseHref}
				{rawBase}
				repo={`${namespace}/${namespace}`}
				class="markdown-readme"
			/>
		</div>
	</div>
{:else if isOwn}
	<div class="rounded-lg border border-dashed bg-card px-5 py-6 text-sm text-muted-foreground">
		<p class="font-medium text-foreground">Introduce this {ownerLabel}</p>
		<p class="mt-1">
			Create a git repository named <code class="rounded bg-muted px-1 py-0.5 font-mono text-xs"
				>{namespace}/{namespace}</code
			>
			with a README and it renders here.
		</p>
		<a
			href={`${resolve('/git/new')}?namespace=${encodeURIComponent(namespace)}&name=${encodeURIComponent(namespace)}`}
			class="link mt-2 inline-block">Create it</a
		>
	</div>
{/if}
<!-- eslint-enable svelte/no-navigation-without-resolve -->
