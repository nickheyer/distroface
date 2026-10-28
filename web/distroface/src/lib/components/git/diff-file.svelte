<script lang="ts" module>
	import { highlightLines, languageFromPath, escapeHtml } from '$lib/highlight';
	import type { DiffFile, DiffLine } from '$lib/proto/distroface/v1/git_pb';

	export type DiffSide = 'old' | 'new';
	export type DiffMode = 'unified' | 'split';

	const HIGHLIGHT_MAX_LINES = 5000;

	// Syntax colored html per hunk line in hunk order, null when the file is too large or unknown
	export async function highlightFile(file: DiffFile): Promise<string[] | null> {
		const lines = file.hunks.flatMap((h) => h.lines);
		if (lines.length === 0 || lines.length > HIGHLIGHT_MAX_LINES) return null;
		const out = await highlightLines(
			lines.map((l) => l.text).join('\n'),
			languageFromPath(file.newPath || file.oldPath)
		);
		return out.length === lines.length ? out : null;
	}

	type Range = [number, number];
	const WORD = /\w+|\s+|[^\w\s]/g;

	// Character ranges that differ between a removed and an added line, null when they barely overlap
	export function wordRanges(a: string, b: string): { a: Range[]; b: Range[] } | null {
		const ta = a.match(WORD) ?? [];
		const tb = b.match(WORD) ?? [];
		const n = ta.length;
		const m = tb.length;
		if (n === 0 || m === 0 || n > 200 || m > 200) return null;
		const w = m + 1;
		const dp = new Uint16Array((n + 1) * w);
		for (let i = n - 1; i >= 0; i--) {
			for (let j = m - 1; j >= 0; j--) {
				dp[i * w + j] =
					ta[i] === tb[j]
						? dp[(i + 1) * w + j + 1] + 1
						: Math.max(dp[(i + 1) * w + j], dp[i * w + j + 1]);
			}
		}
		const ra: Range[] = [];
		const rb: Range[] = [];
		const push = (list: Range[], start: number, end: number) => {
			const last = list[list.length - 1];
			if (last && last[1] === start) last[1] = end;
			else list.push([start, end]);
		};
		let i = 0;
		let j = 0;
		let pa = 0;
		let pb = 0;
		let same = 0;
		while (i < n || j < m) {
			if (i < n && j < m && ta[i] === tb[j]) {
				same += ta[i].length;
				pa += ta[i].length;
				pb += tb[j].length;
				i++;
				j++;
			} else if (j >= m || (i < n && dp[(i + 1) * w + j] >= dp[i * w + j + 1])) {
				push(ra, pa, pa + ta[i].length);
				pa += ta[i].length;
				i++;
			} else {
				push(rb, pb, pb + tb[j].length);
				pb += tb[j].length;
				j++;
			}
		}
		if (same < Math.max(a.length, b.length) * 0.3) return null;
		return { a: ra, b: rb };
	}

	// Wraps the characters inside ranges with a mark span, keeping token spans well nested
	export function markRanges(html: string, ranges: Range[], cls: string): string {
		if (ranges.length === 0) return html;
		let out = '';
		let pos = 0;
		let r = 0;
		let open = false;
		const openMark = () => {
			out += `<span class="${cls}">`;
			open = true;
		};
		const closeMark = () => {
			out += '</span>';
			open = false;
		};
		let i = 0;
		while (i < html.length) {
			const ch = html[i];
			if (ch === '<') {
				const end = html.indexOf('>', i);
				const tag = html.slice(i, end + 1);
				if (open) closeMark();
				out += tag;
				if (!tag.startsWith('</') && r < ranges.length && pos > ranges[r][0] && pos < ranges[r][1])
					openMark();
				i = end + 1;
				continue;
			}
			let unit = ch;
			if (ch === '&') {
				const end = html.indexOf(';', i);
				if (end > i) unit = html.slice(i, end + 1);
			}
			while (r < ranges.length && pos >= ranges[r][1]) {
				if (open) closeMark();
				r++;
			}
			if (r < ranges.length && pos >= ranges[r][0] && pos < ranges[r][1] && !open) openMark();
			out += unit;
			pos++;
			i += unit.length;
			if (open && r < ranges.length && pos >= ranges[r][1]) {
				closeMark();
				r++;
			}
		}
		if (open) closeMark();
		return out;
	}
</script>

<script lang="ts">
	import { ChevronUp, ChevronDown, UnfoldVertical, Loader } from '$lib/icons';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { DiffLineKind, DiffStatus } from '$lib/proto/distroface/v1/git_pb';
	import type { DiffHunk } from '$lib/proto/distroface/v1/git_pb';
	import { SvelteMap, SvelteSet } from 'svelte/reactivity';
	import type { Snippet } from 'svelte';

	let {
		file,
		hunkHtml = null,
		mode = 'unified',
		namespace = '',
		name = '',
		head = '',
		onLineClick,
		lineExtra,
		renderAll = false
	}: {
		file: DiffFile;
		hunkHtml?: string[] | null;
		mode?: DiffMode;
		namespace?: string;
		name?: string;
		head?: string;
		onLineClick?: (path: string, side: DiffSide, line: number) => void;
		lineExtra?: Snippet<[{ path: string; side: DiffSide; line: number }]>;
		renderAll?: boolean;
	} = $props();

	// Hidden context above a hunk, count below zero runs to the end of the file
	type Gap = {
		key: string;
		oldStart: number;
		newStart: number;
		count: number;
		leading: boolean;
		trailing: boolean;
	};

	type Row =
		| { kind: 'hunk'; header: string; key: string; gap?: Gap }
		| { kind: 'line'; line: DiffLine; key: string };

	type SplitRow =
		| { kind: 'hunk'; header: string; key: string; gap?: Gap }
		| { kind: 'pair'; left?: DiffLine; right?: DiffLine; key: string };

	const CHUNK = 500;
	const STEP = 20;

	const path = $derived(file.newPath || file.oldPath);
	const canExpand = $derived(!!namespace && !!name && !!head);
	const expandable = $derived(
		file.status !== DiffStatus.ADDED && file.status !== DiffStatus.DELETED
	);

	// Lines revealed from the top and bottom of each gap
	const expanded = new SvelteMap<string, { top: number; bottom: number }>();
	const loadingGaps = new SvelteSet<string>();
	const gapHtml = new SvelteMap<number, string>();
	let fileText = $state<string[] | null>(null);
	let fileTextPromise: Promise<string[] | null> | undefined;

	// New state per file so expanded gaps and rendered chunks never leak across files
	$effect(() => {
		void path;
		void file.newSha;
		expanded.clear();
		gapHtml.clear();
		fileText = null;
		fileTextPromise = undefined;
		limit = CHUNK;
	});

	function hunkHeader(h: DiffHunk): string {
		return `@@ -${h.oldStart},${h.oldLines} +${h.newStart},${h.newLines} @@`;
	}

	function contextLine(oldNumber: number, newNumber: number, text: string): DiffLine {
		return {
			$typeName: 'distroface.v1.DiffLine',
			kind: DiffLineKind.CONTEXT,
			oldNumber,
			newNumber,
			text
		};
	}

	// Hunk dividers carry what is still hidden above them, revealed context lands as lines around the divider
	const rows = $derived.by((): Row[] => {
		const out: Row[] = [];
		const hunks = file.hunks;
		const text = fileText;
		const pushContext = (oldStart: number, newStart: number, count: number) => {
			if (!text) return;
			for (let n = newStart; n < newStart + count && n <= text.length; n++) {
				out.push({
					kind: 'line',
					line: contextLine(oldStart + (n - newStart), n, text[n - 1]),
					key: `g${n}`
				});
			}
		};
		const gapRows = (
			key: string,
			oldStart: number,
			newStart: number,
			count: number,
			leading: boolean,
			trailing: boolean
		): { gap?: Gap; after: () => void } => {
			if (count === 0 || !expandable) return { after: () => {} };
			const st = expanded.get(key) ?? { top: 0, bottom: 0 };
			const known = count > 0 ? count : text ? Math.max(0, text.length - newStart + 1) : -1;
			const top = known < 0 ? st.top : Math.min(st.top, known);
			const bottom = known < 0 ? 0 : Math.min(st.bottom, known - top);
			const remaining = known < 0 ? -1 : known - top - bottom;
			pushContext(oldStart, newStart, top);
			const gap =
				remaining !== 0
					? {
							key,
							oldStart: oldStart + top,
							newStart: newStart + top,
							count: remaining,
							leading,
							trailing
						}
					: undefined;
			return {
				gap,
				after: () =>
					pushContext(
						oldStart + top + Math.max(remaining, 0),
						newStart + top + Math.max(remaining, 0),
						bottom
					)
			};
		};
		hunks.forEach((h, i) => {
			let gap: Gap | undefined;
			let after = () => {};
			if (i === 0) {
				if (h.newStart > 1) ({ gap, after } = gapRows('g0', 1, 1, h.newStart - 1, true, false));
			} else {
				const prev = hunks[i - 1];
				const oldStart = prev.oldStart + prev.oldLines;
				const newStart = prev.newStart + prev.newLines;
				const count = h.newStart - newStart;
				if (count > 0) ({ gap, after } = gapRows(`g${i}`, oldStart, newStart, count, false, false));
			}
			out.push({ kind: 'hunk', header: h.header || hunkHeader(h), key: `h${i}`, gap });
			after();
			h.lines.forEach((line, j) => out.push({ kind: 'line', line, key: `l${i}:${j}` }));
		});
		if (hunks.length > 0) {
			const last = hunks[hunks.length - 1];
			const newEnd = last.newStart + last.newLines;
			const tail = file.newLineCount > 0 ? Math.max(0, file.newLineCount - newEnd + 1) : -1;
			const { gap } = gapRows('tail', last.oldStart + last.oldLines, newEnd, tail, false, true);
			if (gap) out.push({ kind: 'hunk', header: '', key: 'tail', gap });
		}
		return out;
	});

	const hunkLines = $derived(file.hunks.flatMap((h) => h.lines));
	const lineIndex = $derived.by(() => {
		const map = new SvelteMap<DiffLine, number>();
		hunkLines.forEach((l, i) => map.set(l, i));
		return map;
	});

	// Pairs deletions with the additions that replaced them, in both layouts
	const paired = $derived.by((): { split: SplitRow[]; partner: WeakMap<DiffLine, DiffLine> } => {
		const split: SplitRow[] = [];
		const partner = new WeakMap<DiffLine, DiffLine>();
		let i = 0;
		while (i < rows.length) {
			const r = rows[i];
			if (r.kind !== 'line') {
				split.push(r);
				i++;
				continue;
			}
			if (r.line.kind === DiffLineKind.CONTEXT || r.line.kind === DiffLineKind.UNSPECIFIED) {
				split.push({ kind: 'pair', left: r.line, right: r.line, key: r.key });
				i++;
				continue;
			}
			const dels: Row[] = [];
			const adds: Row[] = [];
			while (i < rows.length) {
				const x = rows[i];
				if (x.kind !== 'line') break;
				if (x.line.kind === DiffLineKind.DELETE) dels.push(x);
				else if (x.line.kind === DiffLineKind.ADD) adds.push(x);
				else break;
				i++;
			}
			const n = Math.max(dels.length, adds.length);
			for (let k = 0; k < n; k++) {
				const left = dels[k];
				const right = adds[k];
				const l = left?.kind === 'line' ? left.line : undefined;
				const a = right?.kind === 'line' ? right.line : undefined;
				if (l && a) {
					partner.set(l, a);
					partner.set(a, l);
				}
				split.push({
					kind: 'pair',
					left: l,
					right: a,
					key: `${left?.key ?? ''}|${right?.key ?? ''}`
				});
			}
		}
		return { split, partner };
	});
	const splitRows = $derived(paired.split);

	// Rows render in chunks as the sentinel scrolls into view
	let limit = $state(CHUNK);
	const totalRows = $derived(mode === 'split' ? splitRows.length : rows.length);
	const visibleUnified = $derived(rows.slice(0, limit));
	const visibleSplit = $derived(splitRows.slice(0, limit));
	const remaining = $derived(Math.max(0, totalRows - limit));
	let sentinel = $state<HTMLElement | undefined>();

	$effect(() => {
		if (renderAll) limit = Math.max(limit, totalRows);
	});

	$effect(() => {
		const el = sentinel;
		if (!el) return;
		const io = new IntersectionObserver(
			(entries) => {
				if (entries.some((e) => e.isIntersecting)) limit += CHUNK;
			},
			{ rootMargin: '600px 0px' }
		);
		io.observe(el);
		return () => io.disconnect();
	});

	const htmlCache = new WeakMap<DiffLine, string>();
	const rangeCache = new WeakMap<DiffLine, Range[] | null>();

	function baseHtml(line: DiffLine): string {
		const idx = lineIndex.get(line);
		if (idx !== undefined) return hunkHtml?.[idx] ?? escapeHtml(line.text);
		return gapHtml.get(line.newNumber) ?? escapeHtml(line.text);
	}

	function rangesFor(line: DiffLine, other: DiffLine): Range[] | null {
		if (rangeCache.has(line)) return rangeCache.get(line) ?? null;
		const del = line.kind === DiffLineKind.DELETE ? line : other;
		const add = line.kind === DiffLineKind.DELETE ? other : line;
		const r = wordRanges(del.text, add.text);
		rangeCache.set(del, r ? r.a : null);
		rangeCache.set(add, r ? r.b : null);
		return rangeCache.get(line) ?? null;
	}

	// Token colored html with the characters that changed against the paired line marked
	function lineHtml(line: DiffLine): string {
		const cached = htmlCache.get(line);
		if (cached !== undefined) return cached;
		let html = baseHtml(line);
		const other = paired.partner.get(line);
		if (other) {
			const ranges = rangesFor(line, other);
			if (ranges)
				html = markRanges(
					html,
					ranges,
					line.kind === DiffLineKind.DELETE ? 'diff-word-del' : 'diff-word-add'
				);
		}
		htmlCache.set(line, html);
		return html;
	}

	async function loadFileText(): Promise<string[] | null> {
		if (!fileTextPromise) {
			fileTextPromise = rpcClient.gitFiles
				.getBlob({ namespace, name, ref: head, path }, silentCallOptions)
				.then((resp) => {
					const b = resp.blob;
					if (!b || b.isBinary || b.truncated) return null;
					const text = new TextDecoder().decode(b.content);
					const body = text.endsWith('\n') ? text.slice(0, -1) : text;
					return body.split('\n');
				})
				.catch(() => null);
		}
		return fileTextPromise;
	}

	// Colors the revealed lines before they appear so the gap opens already highlighted
	async function reveal(gap: Gap, where: 'top' | 'bottom' | 'all', count: number) {
		if (loadingGaps.has(gap.key)) return;
		loadingGaps.add(gap.key);
		try {
			const text = await loadFileText();
			if (!text) return;
			const known = gap.count > 0 ? gap.count : Math.max(0, text.length - gap.newStart + 1);
			const take = where === 'all' ? known : Math.min(count, known);
			const start = where === 'bottom' ? gap.newStart + known - take : gap.newStart;
			const slice = text.slice(start - 1, start - 1 + take);
			const html = await highlightLines(slice.join('\n'), languageFromPath(path));
			if (html.length === slice.length) slice.forEach((_, k) => gapHtml.set(start + k, html[k]));
			const st = expanded.get(gap.key) ?? { top: 0, bottom: 0 };
			if (where === 'all') expanded.set(gap.key, { top: st.top + known, bottom: st.bottom });
			else if (where === 'top') expanded.set(gap.key, { top: st.top + take, bottom: st.bottom });
			else expanded.set(gap.key, { top: st.top, bottom: st.bottom + take });
			fileText = text;
		} finally {
			loadingGaps.delete(gap.key);
		}
	}

	function sideOf(line: DiffLine): DiffSide {
		return line.kind === DiffLineKind.DELETE ? 'old' : 'new';
	}

	function numberOf(line: DiffLine): number {
		return line.kind === DiffLineKind.DELETE ? line.oldNumber : line.newNumber;
	}

	function rowClass(line: DiffLine): string {
		if (line.kind === DiffLineKind.ADD) return 'diff-add';
		if (line.kind === DiffLineKind.DELETE) return 'diff-del';
		return '';
	}

	function cellClass(line: DiffLine): string {
		if (line.kind === DiffLineKind.ADD) return 'diff-cell-add';
		if (line.kind === DiffLineKind.DELETE) return 'diff-cell-del';
		return '';
	}

	function sign(line: DiffLine): string {
		if (line.kind === DiffLineKind.ADD) return '+';
		if (line.kind === DiffLineKind.DELETE) return '-';
		return ' ';
	}

	function click(side: DiffSide, n: number) {
		if (n > 0) onLineClick?.(path, side, n);
	}

	function hiddenLabel(count: number): string {
		return count < 0
			? 'lines to the end of the file'
			: `${count} hidden line${count !== 1 ? 's' : ''}`;
	}
</script>

{#snippet lineNo(side: DiffSide, n: number)}
	<td class="line-no {onLineClick ? 'commentable' : ''}">
		{#if onLineClick}
			<button
				type="button"
				onclick={() => click(side, n)}
				title={n > 0 ? `Comment on ${side} line ${n}` : undefined}>{n > 0 ? n : ''}</button
			>
		{:else}
			{n > 0 ? n : ''}
		{/if}
	</td>
{/snippet}

{#snippet hunkRow(row: Extract<Row, { kind: 'hunk' }>)}
	<tr class="diff-hunk">
		<td colspan="4">
			<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
				{#if row.gap}
					{@const gap = row.gap}
					{@const busy = loadingGaps.has(gap.key)}
					{@const stepped = gap.count < 0 || gap.count > STEP * 2}
					{#if canExpand}
						<span class="diff-expand" aria-busy={busy}>
							{#if busy}
								<Loader class="size-3 animate-spin" />
							{:else}
								{#if stepped && !gap.leading}
									<button
										type="button"
										onclick={() => reveal(gap, 'top', STEP)}
										title="Show {STEP} lines below the previous hunk"
									>
										<ChevronDown class="size-3" />{STEP}
									</button>
								{/if}
								{#if stepped && !gap.trailing}
									<button
										type="button"
										onclick={() => reveal(gap, 'bottom', STEP)}
										title="Show {STEP} lines above this hunk"
									>
										<ChevronUp class="size-3" />{STEP}
									</button>
								{/if}
								<button
									type="button"
									onclick={() => reveal(gap, 'all', 0)}
									title="Show every hidden line"
								>
									<UnfoldVertical class="size-3" />
									{stepped ? 'all' : 'Expand'}
								</button>
							{/if}
						</span>
						<span class="text-xs text-muted-foreground">{hiddenLabel(gap.count)}</span>
					{:else}
						<span class="text-xs text-muted-foreground">{hiddenLabel(gap.count)}</span>
					{/if}
				{/if}
				{#if row.header}
					<span class="min-w-0 text-xs">{row.header}</span>
				{/if}
			</div>
		</td>
	</tr>
{/snippet}

<table class="code-table diff-table {mode === 'split' ? 'diff-split' : 'diff-unified'}">
	{#if mode === 'split'}
		<colgroup>
			<col class="diff-col-no" />
			<col class="diff-col-code" />
			<col class="diff-col-no" />
			<col class="diff-col-code" />
		</colgroup>
	{:else}
		<colgroup>
			<col class="diff-col-no" />
			<col class="diff-col-no" />
			<col class="diff-col-sign" />
			<col class="diff-col-code" />
		</colgroup>
	{/if}
	<tbody>
		{#if mode === 'unified'}
			{#each visibleUnified as row (row.key)}
				{#if row.kind === 'hunk'}
					{@render hunkRow(row)}
				{:else}
					<tr class="diff-row {rowClass(row.line)}">
						{@render lineNo('old', row.line.oldNumber)}
						{@render lineNo('new', row.line.newNumber)}
						<td class="diff-sign">{sign(row.line)}</td>
						<td class="code-line wrap">
							<!-- eslint-disable-next-line svelte/no-at-html-tags -->
							{@html lineHtml(row.line)}
						</td>
					</tr>
					{#if lineExtra}
						<tr class="diff-extra"
							><td colspan="4"
								>{@render lineExtra({ path, side: sideOf(row.line), line: numberOf(row.line) })}</td
							></tr
						>
					{/if}
				{/if}
			{/each}
		{:else}
			{#each visibleSplit as row (row.key)}
				{#if row.kind === 'hunk'}
					{@render hunkRow(row)}
				{:else}
					<tr class="diff-row">
						{#if row.left}
							{@render lineNo('old', row.left.oldNumber)}
							<td class="code-line wrap split-cell {cellClass(row.left)}">
								<!-- eslint-disable-next-line svelte/no-at-html-tags -->
								{@html lineHtml(row.left)}
							</td>
						{:else}
							<td class="line-no diff-empty"></td>
							<td class="code-line split-cell diff-empty"></td>
						{/if}
						{#if row.right}
							{@render lineNo('new', row.right.newNumber)}
							<td class="code-line wrap split-cell {cellClass(row.right)}">
								<!-- eslint-disable-next-line svelte/no-at-html-tags -->
								{@html lineHtml(row.right)}
							</td>
						{:else}
							<td class="line-no diff-empty"></td>
							<td class="code-line split-cell diff-empty"></td>
						{/if}
					</tr>
					{#if lineExtra}
						{#if row.left && row.left.kind === DiffLineKind.DELETE}
							<tr class="diff-extra"
								><td colspan="4"
									>{@render lineExtra({ path, side: 'old', line: row.left.oldNumber })}</td
								></tr
							>
						{/if}
						{#if row.right}
							<tr class="diff-extra"
								><td colspan="4"
									>{@render lineExtra({ path, side: 'new', line: row.right.newNumber })}</td
								></tr
							>
						{/if}
					{/if}
				{/if}
			{/each}
		{/if}
	</tbody>
</table>
{#if remaining > 0}
	<div
		bind:this={sentinel}
		class="flex items-center justify-center gap-2 border-t py-3 text-xs text-muted-foreground"
	>
		<Loader class="size-3.5 animate-spin" />
		{remaining} more row{remaining !== 1 ? 's' : ''}
	</div>
{/if}
