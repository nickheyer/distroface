<script lang="ts">
	import { renderMarkdown } from '$lib/markdown';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { cn } from '$lib/utils';

	let {
		source,
		baseHref,
		rawBase,
		repo,
		wikiBase,
		class: className
	}: {
		source: string;
		baseHref?: string;
		rawBase?: string;
		// Autolink target, the current repository when omitted
		repo?: string;
		// Enables [[Page]] links into this wiki directory
		wikiBase?: string;
		class?: string;
	} = $props();

	const refRepo = $derived(repo ?? gitRepoStore.fullName);

	let html = $state('');
	let rendering = $state(true);
	let seq = 0;

	$effect(() => {
		const src = source;
		const base = baseHref;
		const raw = rawBase;
		const target = refRepo;
		const wiki = wikiBase;
		const mine = ++seq;
		rendering = true;
		renderMarkdown(src, { baseHref: base, rawBase: raw, repo: target || undefined, wikiBase: wiki })
			.then((out) => {
				if (mine !== seq) return;
				html = out;
			})
			.catch(() => {
				if (mine !== seq) return;
				html = '';
			})
			.finally(() => {
				if (mine === seq) rendering = false;
			});
	});
</script>

{#if rendering && !html}
	<div class="space-y-2.5">
		<Skeleton class="h-5 w-1/3" />
		<Skeleton class="h-4 w-full" />
		<Skeleton class="h-4 w-5/6" />
		<Skeleton class="h-4 w-2/3" />
	</div>
{:else}
	<div class={cn('markdown-body', className)}>
		<!-- eslint-disable-next-line svelte/no-at-html-tags -->
		{@html html}
	</div>
{/if}
