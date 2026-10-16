<script lang="ts">
	import { ChevronRight, ChevronLeft, Folder, FolderOpen, MessageSquare } from '@lucide/svelte';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { Button } from '$lib/components/ui/button';
	import { DiffStatus } from '$lib/proto/distroface/v1/git_pb';
	import type { DiffFile } from '$lib/proto/distroface/v1/git_pb';
	import type { Pager } from '$lib/pager.svelte';
	import { SvelteSet } from 'svelte/reactivity';

	let {
		files,
		selected = '',
		onSelect,
		viewed,
		onToggleViewed,
		commentCounts,
		pager,
		onPage,
		loading = false
	}: {
		files: DiffFile[];
		selected?: string;
		onSelect: (path: string) => void;
		viewed?: Record<string, string>;
		onToggleViewed?: (file: DiffFile) => void;
		commentCounts?: Map<string, number>;
		pager?: Pager;
		onPage?: () => void;
		loading?: boolean;
	} = $props();

	type Node = { name: string; path: string; dirs: Node[]; files: DiffFile[] };

	function pathOf(f: DiffFile): string {
		return f.newPath || f.oldPath;
	}

	const root = $derived.by(() => {
		const top: Node = { name: '', path: '', dirs: [], files: [] };
		for (const f of files) {
			const parts = pathOf(f).split('/');
			let node = top;
			for (let i = 0; i < parts.length - 1; i++) {
				const dirPath = parts.slice(0, i + 1).join('/');
				let next = node.dirs.find((d) => d.path === dirPath);
				if (!next) {
					next = { name: parts[i], path: dirPath, dirs: [], files: [] };
					node.dirs.push(next);
				}
				node = next;
			}
			node.files.push(f);
		}
		return collapseSingles(top);
	});

	// Folders with one child folder fold into a single row
	function collapseSingles(n: Node): Node {
		n.dirs = n.dirs.map(collapseSingles);
		while (n.path && n.dirs.length === 1 && n.files.length === 0) {
			const only = n.dirs[0];
			n = { name: `${n.name}/${only.name}`, path: only.path, dirs: only.dirs, files: only.files };
		}
		n.dirs.sort((a, b) => a.name.localeCompare(b.name));
		n.files.sort((a, b) => pathOf(a).localeCompare(pathOf(b)));
		return n;
	}

	const closed = new SvelteSet<string>();

	function toggle(path: string) {
		if (closed.has(path)) closed.delete(path);
		else closed.add(path);
	}

	function statusDot(s: DiffStatus): string {
		switch (s) {
			case DiffStatus.ADDED:
				return 'bg-emerald-500';
			case DiffStatus.DELETED:
				return 'bg-red-500';
			case DiffStatus.RENAMED:
			case DiffStatus.COPIED:
				return 'bg-sky-500';
			default:
				return 'bg-amber-500';
		}
	}

	function statusName(s: DiffStatus): string {
		switch (s) {
			case DiffStatus.ADDED:
				return 'Added';
			case DiffStatus.DELETED:
				return 'Deleted';
			case DiffStatus.RENAMED:
				return 'Renamed';
			case DiffStatus.COPIED:
				return 'Copied';
			case DiffStatus.TYPE_CHANGED:
				return 'Type changed';
			default:
				return 'Modified';
		}
	}

	function isViewed(f: DiffFile): boolean {
		return !!viewed && viewed[pathOf(f)] === (f.newSha || f.oldSha);
	}

	const rangeStart = $derived(pager ? (pager.page - 1) * pager.pageSize + 1 : 1);
	const rangeEnd = $derived(pager ? Math.min(pager.page * pager.pageSize, pager.totalCount) : files.length);
	const paged = $derived(!!pager && pager.totalCount > pager.pageSize);

	function page(changed: boolean) {
		if (changed) onPage?.();
	}
</script>

{#snippet dir(node: Node, depth: number)}
	{#each node.dirs as d (d.path)}
		{@const open = !closed.has(d.path)}
		<button
			type="button"
			class="w-full flex items-center gap-1.5 h-7 rounded-md pr-2 text-left text-[12.5px] text-muted-foreground hover:bg-muted/50 hover:text-foreground transition-colors"
			style="padding-left: {depth * 0.75 + 0.25}rem"
			onclick={() => toggle(d.path)}
			aria-expanded={open}
		>
			<ChevronRight class="h-3 w-3 shrink-0 transition-transform duration-150 {open ? 'rotate-90' : ''}" />
			{#if open}
				<FolderOpen class="h-3.5 w-3.5 shrink-0 text-muted-foreground/70" />
			{:else}
				<Folder class="h-3.5 w-3.5 shrink-0 text-muted-foreground/70" />
			{/if}
			<span class="truncate">{d.name}</span>
		</button>
		{#if open}
			{@render dir(d, depth + 1)}
		{/if}
	{/each}
	{#each node.files as f (pathOf(f))}
		{@const path = pathOf(f)}
		{@const count = commentCounts?.get(path) ?? 0}
		{@const active = path === selected}
		{@const done = isViewed(f)}
		<div
			class="group flex items-center gap-1.5 h-7 min-w-0 rounded-md pr-1.5 transition-colors {active
				? 'bg-primary/10 shadow-[inset_2px_0_0_var(--primary)]'
				: 'hover:bg-muted/50'}"
			style="padding-left: {depth * 0.75 + 0.5}rem"
		>
			{#if viewed && onToggleViewed}
				<Checkbox checked={done} onCheckedChange={() => onToggleViewed(f)} class="size-3.5 shrink-0 opacity-70 group-hover:opacity-100 {done ? 'opacity-100' : ''}" aria-label="Viewed" />
			{/if}
			<button
				type="button"
				class="flex-1 min-w-0 flex items-center gap-2 text-left text-[12.5px] {active ? 'text-primary font-medium' : ''} {done && !active ? 'text-muted-foreground' : ''}"
				onclick={() => onSelect(path)}
				title="{statusName(f.status)}  {path}"
				aria-current={active ? 'true' : undefined}
			>
				<span class="h-2 w-2 rounded-[3px] shrink-0 {statusDot(f.status)} {done ? 'opacity-40' : ''}" aria-label={statusName(f.status)}></span>
				<span class="truncate font-mono text-[12px] {done ? 'line-through decoration-muted-foreground/50' : ''}">{path.split('/').pop()}</span>
			</button>
			{#if count > 0}
				<span class="inline-flex items-center gap-0.5 text-[10.5px] text-muted-foreground tabular-nums shrink-0" title="{count} comment{count !== 1 ? 's' : ''}"><MessageSquare class="h-3 w-3" />{count}</span>
			{/if}
			<span class="text-[10px] font-mono tabular-nums shrink-0 opacity-70 group-hover:opacity-100 {active ? 'opacity-100' : ''}">
				{#if f.additions > 0}<span class="text-emerald-600 dark:text-emerald-400">+{f.additions}</span>{/if}
				{#if f.deletions > 0}<span class="text-red-600 dark:text-red-400">-{f.deletions}</span>{/if}
			</span>
		</div>
	{/each}
{/snippet}

<div class="text-[13px] flex flex-col min-h-0 h-full">
	<div class="flex-1 min-h-0 overflow-y-auto p-1.5 transition-opacity {loading ? 'opacity-60' : ''}">
		{#if files.length === 0}
			<p class="text-muted-foreground text-[12.5px] px-2 py-3">No files</p>
		{:else}
			{@render dir(root, 0)}
		{/if}
	</div>
	{#if pager && paged}
		<div class="flex items-center justify-between gap-1 border-t border-border/50 bg-muted/20 px-2 py-1.5 text-[11.5px] text-muted-foreground tabular-nums">
			<span>{rangeStart}&ndash;{rangeEnd} of {pager.totalCount}</span>
			<span class="flex items-center gap-0.5">
				<Button variant="ghost" size="icon" class="h-6 w-6" disabled={!pager.hasPrev || loading} onclick={() => page(pager.prev())} aria-label="Previous files">
					<ChevronLeft class="h-3.5 w-3.5" />
				</Button>
				<Button variant="ghost" size="icon" class="h-6 w-6" disabled={pager.page >= pager.totalPages || loading} onclick={() => page(pager.next())} aria-label="Next files">
					<ChevronRight class="h-3.5 w-3.5" />
				</Button>
			</span>
		</div>
	{/if}
</div>
