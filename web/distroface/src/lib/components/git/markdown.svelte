<script lang="ts">
	import { tick } from 'svelte';
	import { renderMarkdown } from '$lib/markdown';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { gitRepoStore } from '$lib/stores/git-repo.svelte';
	import { cn } from '$lib/utils';

	// Renders slower than this show a skeleton
	const SLOW_MS = 150;

	let {
		source,
		baseHref,
		rawBase,
		repo,
		wikiBase,
		class: className,
		onRendered
	}: {
		source: string;
		baseHref?: string;
		rawBase?: string;
		// Autolink target, the current repository when omitted
		repo?: string;
		// Enables [[Page]] links into this wiki directory
		wikiBase?: string;
		class?: string;
		// Fires once the html is in the DOM
		onRendered?: () => void;
	} = $props();

	const refRepo = $derived(repo ?? gitRepoStore.fullName);

	let html = $state('');
	let slow = $state(false);
	let seq = 0;

	$effect(() => {
		const src = source;
		const base = baseHref;
		const raw = rawBase;
		const target = refRepo;
		const wiki = wikiBase;
		const mine = ++seq;
		const timer = setTimeout(() => {
			if (mine === seq) slow = true;
		}, SLOW_MS);
		renderMarkdown(src, { baseHref: base, rawBase: raw, repo: target || undefined, wikiBase: wiki })
			.then((out) => {
				if (mine !== seq) return;
				html = out;
				void tick().then(() => {
					if (mine === seq) onRendered?.();
				});
			})
			.catch(() => {
				if (mine !== seq) return;
				html = '';
			})
			.finally(() => {
				clearTimeout(timer);
				if (mine === seq) slow = false;
			});
		return () => clearTimeout(timer);
	});
</script>

{#if slow && !html}
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
