<script lang="ts">
	import { ChevronRight, ChevronLeft, Folder, FolderOpen, MessageSquare } from '$lib/icons';
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
				return 'bg-success';
			case DiffStatus.DELETED:
				return 'bg-destructive';
			case DiffStatus.RENAMED:
			case DiffStatus.COPIED:
				return 'bg-info';
			default:
				return 'bg-warning';
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
	const rangeEnd = $derived(
		pager ? Math.min(pager.page * pager.pageSize, pager.totalCount) : files.length
	);
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
			class="flex h-7 w-full items-center gap-1.5 rounded-md pr-2 text-left text-xs text-muted-foreground transition-colors hover:bg-muted/60 hover:text-foreground"
			style="padding-left: {depth * 0.75 + 0.25}rem"
			onclick={() => toggle(d.path)}
			aria-expanded={open}
		>
			<ChevronRight
				class="size-3 shrink-0 transition-transform duration-150 {open ? 'rotate-90' : ''}"
			/>
			{#if open}
				<FolderOpen class="size-3.5 shrink-0" />
			{:else}
				<Folder class="size-3.5 shrink-0" />
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
			class="group relative flex h-7 min-w-0 items-center gap-1.5 rounded-md pr-1.5 transition-colors {active
				? 'bg-muted'
				: 'hover:bg-muted/60'}"
			style="padding-left: {depth * 0.75 + 0.5}rem"
		>
			{#if active}
				<span class="absolute inset-y-1.5 left-0 w-0.5 rounded-full bg-primary" aria-hidden="true"
				></span>
			{/if}
			{#if viewed && onToggleViewed}
				<Checkbox
					checked={done}
					onCheckedChange={() => onToggleViewed(f)}
					class="size-3.5 shrink-0"
					aria-label="Viewed"
				/>
			{/if}
			<button
				type="button"
				class="flex min-w-0 flex-1 items-center gap-2 text-left text-xs {active
					? 'font-medium text-foreground'
					: ''} {done && !active ? 'text-muted-foreground' : ''}"
				onclick={() => onSelect(path)}
				title="{statusName(f.status)}  {path}"
				aria-current={active ? 'true' : undefined}
			>
				<span
					class="size-2 shrink-0 rounded-[3px] {statusDot(f.status)} {done ? 'opacity-40' : ''}"
					aria-label={statusName(f.status)}
				></span>
				<span class="truncate font-mono {done ? 'line-through' : ''}">{path.split('/').pop()}</span>
			</button>
			{#if count > 0}
				<span
					class="inline-flex shrink-0 items-center gap-0.5 text-2xs text-muted-foreground tabular-nums"
					title="{count} comment{count !== 1 ? 's' : ''}"
				>
					<MessageSquare class="size-3" />{count}
				</span>
			{/if}
			<span class="shrink-0 font-mono text-2xs tabular-nums">
				{#if f.additions > 0}<span class="text-success">+{f.additions}</span>{/if}
				{#if f.deletions > 0}<span class="text-destructive">-{f.deletions}</span>{/if}
			</span>
		</div>
	{/each}
{/snippet}

<div class="flex h-full min-h-0 flex-col text-sm">
	<div
		class="min-h-0 flex-1 overflow-y-auto p-1.5 transition-opacity {loading ? 'opacity-60' : ''}"
	>
		{#if files.length === 0}
			<p class="px-2 py-3 text-sm text-muted-foreground">No files</p>
		{:else}
			{@render dir(root, 0)}
		{/if}
	</div>
	{#if pager && paged}
		<div
			class="flex items-center justify-between gap-1 border-t bg-muted/30 px-2 py-1.5 text-xs text-muted-foreground tabular-nums"
		>
			<span>{rangeStart}&ndash;{rangeEnd} of {pager.totalCount}</span>
			<span class="flex items-center gap-0.5">
				<Button
					variant="ghost-muted"
					size="icon-xs"
					disabled={!pager.hasPrev || loading}
					onclick={() => page(pager.prev())}
					aria-label="Previous files"
				>
					<ChevronLeft />
				</Button>
				<Button
					variant="ghost-muted"
					size="icon-xs"
					disabled={pager.page >= pager.totalPages || loading}
					onclick={() => page(pager.next())}
					aria-label="Next files"
				>
					<ChevronRight />
				</Button>
			</span>
		</div>
	{/if}
</div>
