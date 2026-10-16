<script lang="ts">
	import type { Snippet } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Search, Plus, BookOpen, PanelLeft, PanelBottom } from '@lucide/svelte';
	import { Input } from '$lib/components/ui/input';
	import { Button } from '$lib/components/ui/button';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import Markdown from './markdown.svelte';
	import type { WikiPage } from '$lib/proto/distroface/v1/wiki_pb';
	import { cn } from '$lib/utils';

	let {
		namespace,
		name,
		pages,
		current = '',
		sidebar = '',
		footer = '',
		canWrite = false,
		rawBase,
		class: className,
		extra
	}: {
		namespace: string;
		name: string;
		pages: WikiPage[];
		current?: string;
		sidebar?: string;
		footer?: string;
		canWrite?: boolean;
		rawBase?: string;
		class?: string;
		extra?: Snippet;
	} = $props();

	let query = $state('');

	const base = $derived(resolve('/[namespace]/[name]/wiki', { namespace, name }));
	const newHref = $derived(resolve('/[namespace]/[name]/wiki/new', { namespace, name }));
	const listed = $derived(pages.filter((p) => !p.title.startsWith('_')));
	const visible = $derived.by(() => {
		const q = query.trim().toLowerCase();
		return listed.filter((p) => !q || p.title.toLowerCase().includes(q));
	});
	const hasSidebarPage = $derived(pages.some((p) => p.title === '_Sidebar'));
	const hasFooterPage = $derived(pages.some((p) => p.title === '_Footer'));

	function jump(title: string | undefined) {
		if (!title || title === current) return;
		void goto(resolve('/[namespace]/[name]/wiki/[page]', { namespace, name, page: encodeURIComponent(title) }));
	}
</script>

<aside class={cn('space-y-4 min-w-0', className)}>
	<div class="lg:hidden flex items-center gap-2">
		<Select type="single" value={current} onValueChange={jump}>
			<SelectTrigger class="flex-1 min-w-0" aria-label="Wiki page">
				<span class="inline-flex items-center gap-2 truncate">
					<BookOpen class="h-3.5 w-3.5 text-muted-foreground shrink-0" />
					<span class="truncate">{current || 'Pages'}</span>
				</span>
			</SelectTrigger>
			<SelectContent>
				{#each listed as p (p.title)}
					<SelectItem value={p.title}>{p.title}</SelectItem>
				{/each}
			</SelectContent>
		</Select>
		{#if canWrite}
			<Button variant="outline" size="sm" class="h-9 shrink-0" href={resolve('/[namespace]/[name]/wiki/new', { namespace, name })}>
				<Plus class="h-3.5 w-3.5 mr-1.5" />New Page
			</Button>
		{/if}
	</div>

	<div class="hidden lg:block space-y-4">
		{#if sidebar.trim()}
			<div class="rounded-xl border border-border/60 px-4 py-3 min-w-0 overflow-x-auto">
				<Markdown source={sidebar} baseHref="{base}/" wikiBase="{base}/" {rawBase} class="text-[13px]" />
			</div>
		{/if}

		<div class="rounded-xl border border-border/60 overflow-hidden">
			<div class="flex items-center gap-2 px-3 py-2 border-b border-border/40 bg-muted/20">
				<BookOpen class="h-3.5 w-3.5 text-muted-foreground" />
				<span class="text-[12.5px] font-semibold flex-1">Pages</span>
				<span class="text-[11.5px] text-muted-foreground tabular-nums">{visible.length}</span>
				{#if canWrite}
					<Button variant="ghost" size="sm" class="h-6 w-6 p-0" href={resolve('/[namespace]/[name]/wiki/new', { namespace, name })} title="New page">
						<Plus class="h-3.5 w-3.5" />
					</Button>
				{/if}
			</div>
			<div class="px-2 py-2 border-b border-border/40">
				<div class="relative">
					<Search class="h-3.5 w-3.5 text-muted-foreground absolute left-2.5 top-1/2 -translate-y-1/2" />
					<Input bind:value={query} placeholder="Find a page" class="h-8 pl-8 text-[12.5px]" aria-label="Find a page" />
				</div>
			</div>
			{#if visible.length === 0}
				<p class="px-3 py-3 text-[12.5px] text-muted-foreground">{query ? 'No pages match' : 'No pages yet'}</p>
			{:else}
				<ul class="max-h-96 overflow-y-auto py-1">
					{#each visible as p (p.title)}
						<li>
							<a
								href={resolve('/[namespace]/[name]/wiki/[page]', { namespace, name, page: encodeURIComponent(p.title) })}
								class="block px-3 py-1.5 text-[13px] truncate transition-colors {p.title === current ? 'bg-primary/10 text-primary font-medium' : 'hover:bg-muted/50'}"
								aria-current={p.title === current ? 'page' : undefined}
							>
								{p.title}
							</a>
						</li>
					{/each}
				</ul>
			{/if}
		</div>

		{#if canWrite}
			<div class="flex items-center gap-4 px-1 text-[12px] text-muted-foreground flex-wrap">
				{#if hasSidebarPage}
					<a href={resolve('/[namespace]/[name]/wiki/[page]/edit', { namespace, name, page: '_Sidebar' })} class="inline-flex items-center gap-1 hover:text-primary">
						<PanelLeft class="h-3 w-3" />Edit sidebar
					</a>
				{:else}
					<a href={resolve(`${newHref}?title=_Sidebar` as `/${string}`)} class="inline-flex items-center gap-1 hover:text-primary">
						<PanelLeft class="h-3 w-3" />Add sidebar
					</a>
				{/if}
				{#if hasFooterPage}
					<a href={resolve('/[namespace]/[name]/wiki/[page]/edit', { namespace, name, page: '_Footer' })} class="inline-flex items-center gap-1 hover:text-primary">
						<PanelBottom class="h-3 w-3" />Edit footer
					</a>
				{:else}
					<a href={resolve(`${newHref}?title=_Footer` as `/${string}`)} class="inline-flex items-center gap-1 hover:text-primary">
						<PanelBottom class="h-3 w-3" />Add footer
					</a>
				{/if}
			</div>
		{/if}

		{#if footer.trim()}
			<div class="rounded-xl border border-border/60 px-4 py-3 min-w-0 overflow-x-auto">
				<Markdown source={footer} baseHref="{base}/" wikiBase="{base}/" {rawBase} class="text-[12.5px]" />
			</div>
		{/if}

		{@render extra?.()}
	</div>
</aside>
