<script lang="ts" module>
	import type { DiffFile } from '$lib/proto/distroface/v1/git_pb';
	import type { PageInfo } from '$lib/proto/distroface/v1/pagination_pb';
	import type { Pager } from '$lib/pager.svelte';

	export type DiffPageRequest = ReturnType<Pager['request']>;

	// One server page of changed files plus totals over the whole diff
	export interface DiffListing {
		files: DiffFile[];
		page?: PageInfo;
		totalFiles: number;
		additions: number;
		deletions: number;
		truncated?: boolean;
	}
</script>

<script lang="ts">
	import { resolve } from '$app/paths';
	import { tick, untrack } from 'svelte';
	import type { Snippet } from 'svelte';
	import {
		ChevronLeft,
		ChevronRight,
		Columns2,
		Rows3,
		FileCode,
		FolderTree,
		PanelLeft,
		Search,
		X,
		Loader2,
		FileDiff,
		RefreshCw,
		Eye
	} from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge, type BadgeVariant } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import CopyButton from '$lib/components/copy-button.svelte';
	import FileTree from '$lib/components/git/file-tree.svelte';
	import DiffFileView, {
		highlightFile,
		type DiffMode,
		type DiffSide
	} from '$lib/components/git/diff-file.svelte';
	import { Pager as PagerClass } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { DiffStatus } from '$lib/proto/distroface/v1/git_pb';
	import { SvelteMap } from 'svelte/reactivity';

	const modeKey = 'df-diff-mode';

	// Last chosen layout, read before the props so the bindable default can use it
	function savedMode(): DiffMode {
		const saved = typeof localStorage !== 'undefined' ? localStorage.getItem(modeKey) : null;
		return saved === 'split' ? 'split' : 'unified';
	}

	let {
		namespace,
		name,
		head = '',
		base = '',
		listFiles,
		loadFile,
		resetKey,
		initialPath = '',
		revealId = '',
		onSelect,
		onFile,
		mode = $bindable<DiffMode>(savedMode()),
		onLineClick,
		lineExtra,
		viewed,
		onToggleViewed,
		commentCounts,
		toolbar,
		pageSize = 100
	}: {
		namespace: string;
		name: string;
		head?: string;
		base?: string;
		listFiles: (page: DiffPageRequest) => Promise<DiffListing>;
		loadFile: (path: string) => Promise<DiffFile | undefined>;
		resetKey: string;
		initialPath?: string;
		revealId?: string;
		onSelect?: (path: string) => void;
		onFile?: (file: DiffFile) => void;
		mode?: DiffMode;
		onLineClick?: (path: string, side: DiffSide, line: number) => void;
		lineExtra?: Snippet<[{ path: string; side: DiffSide; line: number }]>;
		viewed?: Record<string, string>;
		onToggleViewed?: (file: DiffFile) => void;
		commentCounts?: Map<string, number>;
		toolbar?: Snippet;
		pageSize?: number;
	} = $props();

	$effect(() => {
		localStorage.setItem(modeKey, mode);
	});

	const CACHE_MAX = 40;

	// svelte-ignore state_referenced_locally
	const pager = new PagerClass(pageSize);
	const filter = new QueryFilter();
	let files = $state<DiffFile[]>([]);
	let totalFiles = $state(0);
	let additions = $state(0);
	let deletions = $state(0);
	let truncated = $state(false);
	let listing = $state(true);
	let listFailed = $state(false);
	let selected = $state('');
	let current = $state<DiffFile | undefined>();
	let currentHtml = $state<string[] | null>(null);
	let fileLoading = $state(false);
	let fileFailed = $state(false);
	let renderAll = $state(false);
	const cache = new SvelteMap<string, { file: DiffFile; html: string[] | null }>();
	let listSeq = 0;
	let fileSeq = 0;
	let pendingInitial = '';
	let debounce: ReturnType<typeof setTimeout> | undefined;

	function pathOf(f: DiffFile): string {
		return f.newPath || f.oldPath;
	}

	type Pick = 'keep' | 'first' | 'last';

	async function loadList(pick: Pick) {
		const mine = ++listSeq;
		listing = true;
		listFailed = false;
		try {
			const resp = await listFiles(pager.request(filter.request()));
			if (mine !== listSeq) return;
			files = resp.files;
			totalFiles = resp.totalFiles;
			additions = resp.additions;
			deletions = resp.deletions;
			truncated = resp.truncated ?? false;
			pager.apply(resp.page);
		} catch {
			if (mine !== listSeq) return;
			files = [];
			totalFiles = 0;
			truncated = false;
			listFailed = true;
			pager.apply(undefined);
		} finally {
			if (mine === listSeq) listing = false;
		}
		if (mine !== listSeq) return;
		if (pendingInitial) {
			const target = pendingInitial;
			pendingInitial = '';
			select(target);
			return;
		}
		const paths = files.map(pathOf);
		if (pick === 'first' || (pick === 'keep' && !paths.includes(selected))) select(paths[0] ?? '');
		else if (pick === 'last') select(paths[paths.length - 1] ?? '');
	}

	// The pane keeps showing the previous file until the next one is loaded and colored
	async function select(path: string) {
		selected = path;
		renderAll = false;
		onSelect?.(path);
		const mine = ++fileSeq;
		fileFailed = false;
		if (!path) {
			current = undefined;
			currentHtml = null;
			fileLoading = false;
			return;
		}
		const cached = cache.get(path);
		if (cached) {
			current = cached.file;
			currentHtml = cached.html;
			fileLoading = false;
			return;
		}
		fileLoading = true;
		try {
			const f = await loadFile(path);
			if (mine !== fileSeq) return;
			if (!f) throw new Error('missing');
			const html = await highlightFile(f);
			if (mine !== fileSeq) return;
			if (cache.size >= CACHE_MAX) cache.delete(cache.keys().next().value as string);
			cache.set(path, { file: f, html });
			current = f;
			currentHtml = html;
		} catch {
			if (mine !== fileSeq) return;
			fileFailed = true;
		} finally {
			if (mine === fileSeq) fileLoading = false;
		}
	}

	$effect(() => {
		void resetKey;
		untrack(() => {
			pager.reset();
			filter.reset();
			cache.clear();
			pendingInitial = initialPath;
			selected = '';
			current = undefined;
			currentHtml = null;
			loadList('first');
		});
	});

	$effect(() => {
		if (current) onFile?.(current);
	});

	function onFilterInput() {
		clearTimeout(debounce);
		debounce = setTimeout(() => {
			pager.reset();
			loadList('first');
		}, 250);
	}

	function clearFilter() {
		filter.reset();
		pager.reset();
		loadList('first');
	}

	const index = $derived(files.findIndex((f) => pathOf(f) === selected));
	const position = $derived(index < 0 ? 0 : (pager.page - 1) * pager.pageSize + index + 1);
	const hasPrev = $derived(index > 0 || (index === 0 && pager.hasPrev));
	const hasNext = $derived(
		index >= 0 && (index < files.length - 1 || pager.page < pager.totalPages)
	);

	function prevFile() {
		if (listing) return;
		if (index > 0) select(pathOf(files[index - 1]));
		else if (pager.prev()) loadList('last');
	}

	function nextFile() {
		if (listing) return;
		if (index >= 0 && index < files.length - 1) select(pathOf(files[index + 1]));
		else if (pager.next()) loadList('first');
	}

	function onKey(e: KeyboardEvent) {
		if (e.ctrlKey || e.metaKey || e.altKey) return;
		const t = e.target as HTMLElement | null;
		if (
			t &&
			(t.tagName === 'INPUT' ||
				t.tagName === 'TEXTAREA' ||
				t.tagName === 'SELECT' ||
				t.isContentEditable)
		)
			return;
		if (e.key === 'j') nextFile();
		else if (e.key === 'k') prevFile();
	}

	let showTree = $state(true);
	let toolbarHeight = $state(0);
	let mobileTreeOpen = $state(false);

	function pickFromTree(path: string) {
		mobileTreeOpen = false;
		select(path);
	}

	// Deep links reveal their element once the file has rendered, growing the chunk window when needed
	let revealed = '';
	$effect(() => {
		const id = revealId;
		if (!id || !current || fileLoading || revealed === id) return;
		void renderAll;
		tick().then(() => {
			const el = document.getElementById(id);
			if (el) {
				revealed = id;
				el.scrollIntoView({ block: 'center' });
			} else if (!renderAll) {
				renderAll = true;
			}
		});
	});

	const viewedCount = $derived(viewed ? Object.keys(viewed).length : 0);
	const viewedPct = $derived(
		totalFiles > 0 ? Math.min(100, Math.round((viewedCount / totalFiles) * 100)) : 0
	);
	const isViewed = $derived(
		!!current && !!viewed && viewed[pathOf(current)] === (current.newSha || current.oldSha)
	);

	function statusLabel(s: DiffStatus): string {
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

	function statusVariant(s: DiffStatus): BadgeVariant {
		switch (s) {
			case DiffStatus.ADDED:
				return 'success';
			case DiffStatus.DELETED:
				return 'destructive';
			case DiffStatus.RENAMED:
			case DiffStatus.COPIED:
				return 'default';
			default:
				return 'warning';
		}
	}

	function blobHref(ref: string, path: string): string {
		return resolve('/[namespace]/[name]/blob/[ref]/[...path]', {
			namespace,
			name,
			ref: encodeURIComponent(ref),
			path
		});
	}

	function splitPath(p: string): { dir: string; base: string } {
		const idx = p.lastIndexOf('/');
		return idx < 0 ? { dir: '', base: p } : { dir: p.slice(0, idx + 1), base: p.slice(idx + 1) };
	}

	const currentPath = $derived(current ? pathOf(current) : selected);
	const currentParts = $derived(splitPath(currentPath));
	const rename = $derived(
		!!current && (current.status === DiffStatus.RENAMED || current.status === DiffStatus.COPIED)
	);
</script>

<svelte:window onkeydown={onKey} />

{#snippet filterBox()}
	<div class="relative">
		<Search class="absolute top-1/2 left-2.5 size-3.5 -translate-y-1/2 text-muted-foreground" />
		<Input
			bind:value={filter.text}
			oninput={onFilterInput}
			placeholder="Filter files"
			class="pr-7 pl-8"
			aria-label="Filter changed files"
		/>
		{#if filter.text}
			<button
				type="button"
				class="absolute top-1/2 right-2 -translate-y-1/2 text-muted-foreground hover:text-foreground"
				onclick={clearFilter}
				aria-label="Clear filter"
			>
				<X class="size-3" />
			</button>
		{/if}
	</div>
{/snippet}

<!-- Clip not hidden so sticky children follow the viewport -->
<div
	class="overflow-clip rounded-lg border bg-card"
	style="--diff-top: calc(3.25rem + {toolbarHeight}px)"
>
	<div
		bind:offsetHeight={toolbarHeight}
		class="sticky top-13 z-20 flex flex-wrap items-center gap-2 border-b bg-card px-3 py-2 text-sm"
	>
		<Button
			variant="ghost-muted"
			size="icon"
			class="hidden lg:inline-flex"
			onclick={() => (showTree = !showTree)}
			title="Toggle file tree"
			aria-label="Toggle file tree"
			aria-pressed={showTree}
		>
			<PanelLeft />
		</Button>
		<Button
			variant="ghost-muted"
			size="icon"
			class="lg:hidden"
			onclick={() => (mobileTreeOpen = !mobileTreeOpen)}
			title="Toggle file list"
			aria-label="Toggle file list"
			aria-expanded={mobileTreeOpen}
		>
			<FolderTree />
		</Button>
		<span class="font-medium whitespace-nowrap tabular-nums">
			{totalFiles} file{totalFiles !== 1 ? 's' : ''}
		</span>
		<span class="font-mono text-xs whitespace-nowrap tabular-nums">
			<span class="text-success">+{additions}</span>
			<span class="ml-1 text-destructive">-{deletions}</span>
		</span>
		{#if truncated}
			<span
				class="whitespace-nowrap text-warning"
				title="This diff is too large to count every file, files past the cap are listed without stats"
			>
				partial totals
			</span>
		{/if}
		{#if viewed}
			<span
				class="inline-flex items-center gap-1 whitespace-nowrap text-muted-foreground tabular-nums"
				title="{viewedPct}% reviewed"
			>
				<Eye class="size-3.5" />{viewedCount} / {totalFiles}
			</span>
		{/if}
		{#if listing}
			<Loader2 class="size-3.5 animate-spin text-muted-foreground" />
		{/if}
		<div class="ml-auto flex items-center gap-2">
			{#if toolbar}
				{@render toolbar()}
			{/if}
			<div class="inline-flex overflow-hidden rounded-md border">
				<button
					type="button"
					class="inline-flex h-8 items-center px-2 transition-colors {mode === 'unified'
						? 'bg-muted text-foreground'
						: 'text-muted-foreground hover:text-foreground'}"
					onclick={() => (mode = 'unified')}
					title="Unified"
					aria-label="Unified"
					aria-pressed={mode === 'unified'}
				>
					<Rows3 class="size-3.5" />
				</button>
				<button
					type="button"
					class="inline-flex h-8 items-center border-l px-2 transition-colors {mode === 'split'
						? 'bg-muted text-foreground'
						: 'text-muted-foreground hover:text-foreground'}"
					onclick={() => (mode = 'split')}
					title="Split"
					aria-label="Split"
					aria-pressed={mode === 'split'}
				>
					<Columns2 class="size-3.5" />
				</button>
			</div>
		</div>
		{#if viewed && totalFiles > 0}
			<div class="absolute right-0 bottom-0 left-0 h-0.5 bg-muted" aria-hidden="true">
				<div
					class="h-full bg-success transition-[width] duration-300"
					style="width: {viewedPct}%"
				></div>
			</div>
		{/if}
	</div>

	{#if mobileTreeOpen}
		<div class="flex max-h-80 flex-col border-b lg:hidden">
			<div class="px-2 pt-2">{@render filterBox()}</div>
			<FileTree
				{files}
				{selected}
				onSelect={pickFromTree}
				{viewed}
				{onToggleViewed}
				{commentCounts}
				{pager}
				onPage={() => loadList('first')}
				loading={listing}
			/>
		</div>
	{/if}

	<div class="grid {showTree ? 'lg:grid-cols-[17rem_minmax(0,1fr)]' : ''}">
		{#if showTree}
			<aside
				class="sticky top-(--diff-top) hidden max-h-[calc(100vh-var(--diff-top))] flex-col self-start border-r lg:flex"
			>
				<div class="px-2 pt-2 pb-1">{@render filterBox()}</div>
				{#if listing && files.length === 0}
					<div class="space-y-2 p-3">
						<Skeleton class="h-4 w-3/4" />
						<Skeleton class="h-4 w-2/3" />
						<Skeleton class="h-4 w-4/5" />
					</div>
				{:else}
					<FileTree
						{files}
						{selected}
						onSelect={pickFromTree}
						{viewed}
						{onToggleViewed}
						{commentCounts}
						{pager}
						onPage={() => loadList('first')}
						loading={listing}
					/>
				{/if}
			</aside>
		{/if}

		<section class="min-w-0 lg:min-h-96">
			{#if listFailed}
				<div class="space-y-2 px-4 py-12 text-center text-sm text-muted-foreground">
					<p>The diff could not be loaded.</p>
					<Button variant="outline" size="sm" onclick={() => loadList('first')}
						><RefreshCw />Retry</Button
					>
				</div>
			{:else if !listing && totalFiles === 0}
				<div class="px-4 py-12 text-center text-sm text-muted-foreground">
					{filter.active ? 'No changed files match this filter' : 'No changes'}
				</div>
			{:else if fileFailed}
				<div class="space-y-2 px-4 py-12 text-center text-sm text-muted-foreground">
					<p class="font-mono text-xs break-all">{selected}</p>
					<p>This file diff could not be loaded.</p>
					<Button variant="outline" size="sm" onclick={() => select(selected)}
						><RefreshCw />Retry</Button
					>
				</div>
			{:else if !current}
				<div class="space-y-3 p-4">
					<Skeleton class="h-8 w-2/3" />
					<Skeleton class="h-64 w-full" />
				</div>
			{:else}
				{@const f = current}
				<div
					class="relative z-10 flex min-w-0 items-center gap-2 border-b bg-card px-3 py-2 lg:sticky lg:top-(--diff-top)"
					aria-busy={fileLoading}
				>
					<div class="inline-flex shrink-0 overflow-hidden rounded-md border">
						<button
							type="button"
							class="inline-flex size-7 items-center justify-center text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-40"
							onclick={prevFile}
							disabled={!hasPrev || listing}
							title="Previous file (k)"
							aria-label="Previous file"
						>
							<ChevronLeft class="size-4" />
						</button>
						<button
							type="button"
							class="inline-flex size-7 items-center justify-center border-l text-muted-foreground transition-colors hover:bg-muted hover:text-foreground disabled:opacity-40"
							onclick={nextFile}
							disabled={!hasNext || listing}
							title="Next file (j)"
							aria-label="Next file"
						>
							<ChevronRight class="size-4" />
						</button>
					</div>
					<span class="hidden shrink-0 text-xs text-muted-foreground tabular-nums sm:inline"
						>{position > 0 ? `${position} / ${totalFiles}` : ''}</span
					>
					<Badge variant={statusVariant(f.status)} class="shrink-0">{statusLabel(f.status)}</Badge>
					<span
						class="min-w-0 flex-1 font-mono text-xs leading-snug break-all"
						title={rename ? `${f.oldPath} -> ${f.newPath}` : currentPath}
					>
						{#if rename}
							<span class="text-muted-foreground">{f.oldPath}</span>
							<span class="mx-1 text-muted-foreground/50">&rarr;</span>
							<span class="text-muted-foreground">{splitPath(f.newPath).dir}</span><span
								class="font-semibold text-foreground">{splitPath(f.newPath).base}</span
							>
						{:else}
							<span class="text-muted-foreground">{currentParts.dir}</span><span
								class="font-semibold text-foreground">{currentParts.base}</span
							>
						{/if}
						{#if f.oldMode && f.newMode && f.oldMode !== f.newMode}
							<span class="ml-1 text-muted-foreground">{f.oldMode} &rarr; {f.newMode}</span>
						{/if}
					</span>
					<CopyButton text={currentPath} label="Path copied" />
					<span class="shrink-0 font-mono text-xs tabular-nums">
						<span class="text-success">+{f.additions}</span>
						<span class="ml-1 text-destructive">-{f.deletions}</span>
					</span>
					{#if viewed && onToggleViewed}
						<label
							class="inline-flex h-7 shrink-0 cursor-pointer items-center gap-1.5 rounded-md border px-2 text-xs transition-colors select-none {isViewed
								? 'border-success/30 bg-success/10 text-success'
								: 'text-muted-foreground hover:border-border-strong hover:text-foreground'}"
						>
							<Checkbox
								checked={isViewed}
								onCheckedChange={() => onToggleViewed(f)}
								class="size-3.5"
								aria-label="Viewed"
							/>
							<span class="hidden sm:inline">Viewed</span>
						</label>
					{/if}
					{#if head && f.status !== DiffStatus.DELETED}
						<Button
							variant="ghost-muted"
							size="icon-sm"
							href={resolve('/[namespace]/[name]/blob/[ref]/[...path]', {
								namespace,
								name,
								ref: encodeURIComponent(head),
								path: f.newPath
							})}
							title="View file at head"
							aria-label="View file at head"
						>
							<FileCode />
						</Button>
					{/if}
					{#if fileLoading}
						<div class="diff-progress" aria-hidden="true"></div>
					{/if}
				</div>

				<div class="transition-opacity duration-150 {fileLoading ? 'opacity-60' : ''}">
					{#if f.isBinary}
						<div class="px-4 py-12 text-center text-sm text-muted-foreground">
							Binary file not shown
						</div>
					{:else if f.isSubmodule}
						<div class="px-4 py-6 font-mono text-xs text-muted-foreground">
							Submodule {f.oldSha.slice(0, 7)} &rarr; {f.newSha.slice(0, 7)}
						</div>
					{:else if f.truncated}
						<div class="space-y-3 px-4 py-12 text-center text-sm text-muted-foreground">
							<FileDiff class="mx-auto size-6 text-muted-foreground/70" />
							<p>
								This file changes {f.additions + f.deletions} lines, above the 20,000 line cap for inline
								diffs.
							</p>
							<div class="flex flex-wrap items-center justify-center gap-2">
								{#if base && f.oldPath}
									<Button variant="outline" size="sm" href={blobHref(base, f.oldPath)}
										>View old version</Button
									>
								{/if}
								{#if head && f.newPath}
									<Button variant="outline" size="sm" href={blobHref(head, f.newPath)}
										>View new version</Button
									>
								{/if}
							</div>
						</div>
					{:else if f.hunks.length === 0}
						<div class="px-4 py-8 text-center text-sm text-muted-foreground">
							{#if f.isLfs}LFS pointer changed{:else}No textual changes{/if}
						</div>
					{:else}
						<DiffFileView
							file={f}
							hunkHtml={currentHtml}
							{mode}
							{namespace}
							{name}
							{head}
							{onLineClick}
							{lineExtra}
							{renderAll}
						/>
					{/if}
				</div>
			{/if}
		</section>
	</div>
</div>
