<script lang="ts">
	import type { Snippet } from 'svelte';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { Search, Plus, BookOpen, PanelLeft, PanelBottom } from '$lib/icons';
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
		void goto(
			resolve('/[namespace]/[name]/wiki/[page]', {
				namespace,
				name,
				page: encodeURIComponent(title)
			})
		);
	}
</script>

<aside class={cn('min-w-0 space-y-4', className)}>
	<div class="flex items-center gap-2 lg:hidden">
		<Select type="single" value={current} onValueChange={jump}>
			<SelectTrigger class="min-w-0 flex-1" aria-label="Wiki page">
				<span class="inline-flex items-center gap-2 truncate">
					<BookOpen class="size-3.5 shrink-0 text-muted-foreground" />
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
			<Button
				variant="outline"
				class="shrink-0"
				href={resolve('/[namespace]/[name]/wiki/new', { namespace, name })}
			>
				<Plus />New page
			</Button>
		{/if}
	</div>

	<div class="hidden space-y-4 lg:block">
		{#if sidebar.trim()}
			<div class="min-w-0 overflow-x-auto rounded-lg border bg-card px-4 py-3">
				<Markdown source={sidebar} baseHref="{base}/" wikiBase="{base}/" {rawBase} />
			</div>
		{/if}

		<div class="box">
			<div class="box-header">
				<span class="flex items-center gap-2">
					<BookOpen class="size-3.5 text-muted-foreground" />
					Pages
					<span
						class="rounded-full bg-muted px-1.5 text-xs font-normal text-muted-foreground tabular-nums"
						>{visible.length}</span
					>
				</span>
				{#if canWrite}
					<Button
						variant="ghost-muted"
						size="icon-xs"
						href={resolve('/[namespace]/[name]/wiki/new', { namespace, name })}
						title="New page"
						aria-label="New page"
					>
						<Plus />
					</Button>
				{/if}
			</div>
			<div class="border-b p-2">
				<div class="relative">
					<Search
						class="absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground"
					/>
					<Input
						bind:value={query}
						placeholder="Find a page"
						class="pl-8"
						aria-label="Find a page"
					/>
				</div>
			</div>
			{#if visible.length === 0}
				<p class="px-3 py-3 text-sm text-muted-foreground">
					{query ? 'No pages match' : 'No pages yet'}
				</p>
			{:else}
				<ul class="max-h-96 overflow-y-auto py-1">
					{#each visible as p (p.title)}
						<li>
							<a
								href={resolve('/[namespace]/[name]/wiki/[page]', {
									namespace,
									name,
									page: encodeURIComponent(p.title)
								})}
								class="block truncate px-3 py-1.5 text-sm transition-colors {p.title === current
									? 'bg-muted font-medium text-foreground'
									: 'text-muted-foreground hover:bg-muted/60 hover:text-foreground'}"
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
			<div class="flex flex-wrap items-center gap-4 px-1 text-xs text-muted-foreground">
				{#if hasSidebarPage}
					<a
						href={resolve('/[namespace]/[name]/wiki/[page]/edit', {
							namespace,
							name,
							page: '_Sidebar'
						})}
						class="inline-flex items-center gap-1 hover:text-primary"
					>
						<PanelLeft class="size-3" />Edit sidebar
					</a>
				{:else}
					<a
						href={resolve(`${newHref}?title=_Sidebar` as `/${string}`)}
						class="inline-flex items-center gap-1 hover:text-primary"
					>
						<PanelLeft class="size-3" />Add sidebar
					</a>
				{/if}
				{#if hasFooterPage}
					<a
						href={resolve('/[namespace]/[name]/wiki/[page]/edit', {
							namespace,
							name,
							page: '_Footer'
						})}
						class="inline-flex items-center gap-1 hover:text-primary"
					>
						<PanelBottom class="size-3" />Edit footer
					</a>
				{:else}
					<a
						href={resolve(`${newHref}?title=_Footer` as `/${string}`)}
						class="inline-flex items-center gap-1 hover:text-primary"
					>
						<PanelBottom class="size-3" />Add footer
					</a>
				{/if}
			</div>
		{/if}

		{#if footer.trim()}
			<div class="min-w-0 overflow-x-auto rounded-lg border bg-card px-4 py-3">
				<Markdown source={footer} baseHref="{base}/" wikiBase="{base}/" {rawBase} />
			</div>
		{/if}

		{@render extra?.()}
	</div>
</aside>
