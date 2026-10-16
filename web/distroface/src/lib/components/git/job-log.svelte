<script lang="ts">
	import { onMount, tick, untrack } from 'svelte';
	import { Search, ChevronUp, ChevronDown, ChevronRight, Clock, Download, ArrowDown, Loader2 } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Input } from '$lib/components/ui/input';
	import RunStatusIcon from '$lib/components/git/run-status-icon.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { ConnectError } from '@connectrpc/connect';
	import type { Job, JobStep } from '$lib/proto/distroface/v1/actions_pb';
	import { durationOf, formatDuration, isActive } from './actions-state';
	import { cn } from '$lib/utils';

	const MAX_RENDER = 50000;
	const RETRY_MS = 3000;

	type LineKind = 'text' | 'group' | 'endgroup' | 'error' | 'warning' | 'notice' | 'debug';

	interface LogLine {
		n: number;
		raw: string;
		kind: LineKind;
		depth: number;
		group: number;
		at?: number;
	}

	interface LogGroup {
		id: number;
		name: string;
		start: number;
		end: number;
		depth: number;
		step?: number;
	}

	interface Span {
		text: string;
		cls: string;
		style: string;
	}

	interface Sgr {
		bold: boolean;
		dim: boolean;
		italic: boolean;
		underline: boolean;
		fg: string;
		bg: string;
	}

	let {
		namespace,
		name,
		runId,
		jobId,
		job,
		follow = false,
		rawUrl = ''
	}: {
		namespace: string;
		name: string;
		runId: string;
		jobId: string;
		job: Job;
		follow?: boolean;
		rawUrl?: string;
	} = $props();

	let lines = $state.raw<LogLine[]>([]);
	let groups = $state.raw<LogGroup[]>([]);
	let closed = $state<Record<number, boolean>>({});
	let streaming = $state(false);
	let finished = $state(false);
	let error = $state('');
	let query = $state('');
	let matchIndex = $state(0);
	let showTimes = $state(false);
	let pinned = $state(true);
	let highlight = $state(0);
	let container = $state<HTMLDivElement | undefined>();

	let total = 0;
	let partial = '';
	let openStack: number[] = [];
	let stepCursor = 0;
	let decoder = new TextDecoder();
	let abort: AbortController | null = null;
	let pendingAnchor = 0;

	const prefix = $derived(`log-${jobId}-`);

	// Top level groups carry step names, matched in order
	function matchStep(gname: string): number | undefined {
		const steps = job.steps;
		for (let i = stepCursor; i < steps.length; i++) {
			if (steps[i].name === gname) {
				stepCursor = i + 1;
				return steps[i].number;
			}
		}
		return undefined;
	}

	function ingest(data: Uint8Array, at: number | undefined, final: boolean) {
		const text = partial + decoder.decode(data, { stream: !final });
		const parts = text.split('\n');
		partial = final ? '' : (parts.pop() ?? '');
		if (final && parts.length > 0 && parts[parts.length - 1] === '') parts.pop();
		if (parts.length === 0) return;
		const added: LogLine[] = [];
		const addedGroups: LogGroup[] = [];
		let touched = false;
		for (const p of parts) {
			const line = p.endsWith('\r') ? p.slice(0, -1) : p;
			const n = ++total;
			const depth = openStack.length;
			if (line.startsWith('##[group]')) {
				const gname = line.slice(9);
				const id = groups.length + addedGroups.length;
				addedGroups.push({ id, name: gname, start: n, end: 0, depth, step: depth === 0 ? matchStep(gname) : undefined });
				added.push({ n, raw: gname, kind: 'group', depth, group: id, at });
				openStack.push(id);
			} else if (line === '##[endgroup]') {
				const id = openStack.pop();
				added.push({ n, raw: '', kind: 'endgroup', depth, group: id ?? -1, at });
				if (id !== undefined) {
					const g = id < groups.length ? groups[id] : addedGroups[id - groups.length];
					if (g) {
						g.end = n;
						touched = true;
					}
				}
			} else {
				const m = /^##\[(error|warning|notice|debug)\](.*)$/.exec(line);
				if (m) added.push({ n, raw: m[2], kind: m[1] as LineKind, depth, group: -1, at });
				else added.push({ n, raw: line, kind: 'text', depth, group: -1, at });
			}
		}
		lines = lines.concat(added);
		if (addedGroups.length > 0 || touched) groups = groups.concat(addedGroups);
	}

	function reset() {
		lines = [];
		groups = [];
		closed = {};
		total = 0;
		partial = '';
		openStack = [];
		stepCursor = 0;
		decoder = new TextDecoder();
		finished = false;
		error = '';
		matchIndex = 0;
		highlight = 0;
		pinned = true;
	}

	async function stream(id: string, ac: AbortController) {
		let from = 0;
		while (!ac.signal.aborted) {
			streaming = true;
			try {
				const chunks = rpcClient.actions.streamJobLog(
					{ namespace, name, jobId: id, fromIndex: from },
					{ ...silentCallOptions, signal: ac.signal }
				);
				for await (const chunk of chunks) {
					if (chunk.data.length > 0) ingest(chunk.data, isActive(job.status) ? Date.now() : undefined, false);
					from = chunk.index + 1;
					if (chunk.done) {
						ingest(new Uint8Array(), undefined, true);
						finished = true;
						streaming = false;
						return;
					}
				}
				error = '';
			} catch (err) {
				if (ac.signal.aborted) return;
				error = err instanceof ConnectError ? err.rawMessage || err.message : String(err);
			}
			if (ac.signal.aborted) return;
			if (!follow) {
				streaming = false;
				return;
			}
			await new Promise((r) => setTimeout(r, RETRY_MS));
		}
		streaming = false;
	}

	$effect(() => {
		const id = jobId;
		const ac = new AbortController();
		abort?.abort();
		abort = ac;
		untrack(() => {
			reset();
			void stream(id, ac);
		});
		return () => ac.abort();
	});

	function isClosed(id: number): boolean {
		const g = groups[id];
		return closed[id] ?? (g ? g.depth > 0 : false);
	}

	function toggle(id: number) {
		closed[id] = !isClosed(id);
	}

	function setAll(open: boolean) {
		const next: Record<number, boolean> = {};
		for (const g of groups) next[g.id] = !open;
		closed = next;
	}

	const visible = $derived.by(() => {
		const out: LogLine[] = [];
		let hiddenAt = -1;
		for (const l of lines) {
			if (hiddenAt >= 0) {
				if (l.depth > hiddenAt) continue;
				hiddenAt = -1;
			}
			if (l.kind === 'endgroup') continue;
			out.push(l);
			if (l.kind === 'group' && isClosed(l.group)) hiddenAt = l.depth;
		}
		return out;
	});

	const rendered = $derived(visible.length > MAX_RENDER ? visible.slice(visible.length - MAX_RENDER) : visible);
	const truncated = $derived(visible.length - rendered.length);

	const needle = $derived(query.trim().toLowerCase());
	const matches = $derived.by(() => {
		if (!needle) return [] as number[];
		const out: number[] = [];
		for (const l of lines) {
			if (l.kind !== 'endgroup' && l.raw.toLowerCase().includes(needle)) out.push(l.n);
		}
		return out;
	});
	const matchSet = $derived(new Set(matches));

	$effect(() => {
		void needle;
		matchIndex = 0;
	});

	function jump(delta: number) {
		if (matches.length === 0) return;
		matchIndex = (matchIndex + delta + matches.length) % matches.length;
		revealLine(matches[matchIndex]);
	}

	function scrollToLine(n: number) {
		const el = document.getElementById(`${prefix}L${n}`);
		if (!el || !container) return;
		container.scrollTo({ top: Math.max(0, el.offsetTop - 40) });
	}

	function revealLine(n: number) {
		for (const g of groups) {
			if (g.start < n && (g.end === 0 || g.end >= n) && isClosed(g.id)) closed[g.id] = false;
		}
		highlight = n;
		pinned = false;
		tick().then(() => scrollToLine(n));
	}

	export function scrollToStep(step: number) {
		const g = groups.find((x) => x.step === step);
		if (!g) return;
		closed[g.id] = false;
		highlight = g.start;
		pinned = false;
		tick().then(() => scrollToLine(g.start));
	}

	function selectLine(e: MouseEvent, n: number) {
		e.preventDefault();
		highlight = n;
		history.replaceState(history.state, '', `#L${n}`);
	}

	onMount(() => {
		const m = /^#L(\d+)$/.exec(location.hash);
		if (m) pendingAnchor = Number(m[1]);
	});

	$effect(() => {
		if (pendingAnchor && lines.length >= pendingAnchor) {
			const n = pendingAnchor;
			pendingAnchor = 0;
			revealLine(n);
		}
	});

	function onScroll() {
		if (!container) return;
		pinned = container.scrollHeight - container.scrollTop - container.clientHeight < 48;
	}

	function resume() {
		pinned = true;
		container?.scrollTo({ top: container.scrollHeight });
	}

	$effect(() => {
		void rendered.length;
		if (!follow || !pinned || !container) return;
		tick().then(() => {
			if (container && pinned) container.scrollTo({ top: container.scrollHeight });
		});
	});

	function stepByNumber(n: number): JobStep | undefined {
		return job.steps.find((s) => s.number === n);
	}

	function timeOf(l: LogLine): string {
		return l.at ? new Date(l.at).toLocaleTimeString([], { hour12: false }) : '';
	}

	function rawText(): string {
		const out: string[] = [];
		for (const l of lines) {
			switch (l.kind) {
				case 'group':
					out.push('##[group]' + l.raw);
					break;
				case 'endgroup':
					out.push('##[endgroup]');
					break;
				case 'text':
					out.push(l.raw);
					break;
				default:
					out.push(`##[${l.kind}]` + l.raw);
			}
		}
		return out.length ? out.join('\n') + '\n' : '';
	}

	function download() {
		const blob = new Blob([rawText()], { type: 'text/plain' });
		const url = URL.createObjectURL(blob);
		const a = document.createElement('a');
		a.href = url;
		a.download = `${runId}-${job.name || jobId}.log`.replace(/[^\w.-]+/g, '_');
		a.click();
		setTimeout(() => URL.revokeObjectURL(url), 1000);
	}

	const ESC = String.fromCharCode(27);
	const BEL = String.fromCharCode(7);
	const ANSI_RE = new RegExp(`${ESC}\\[[0-9;?]*[ -/]*[@-~]|${ESC}\\][^${BEL}${ESC}]*(?:${BEL}|${ESC}\\\\)?`, 'g');
	const BASE = ['#6b7280', '#f87171', '#4ade80', '#facc15', '#60a5fa', '#c084fc', '#22d3ee', '#e5e7eb'];
	const BRIGHT = ['#9ca3af', '#fca5a5', '#86efac', '#fde047', '#93c5fd', '#d8b4fe', '#67e8f9', '#ffffff'];

	function xterm(n: number): string {
		if (n < 8) return BASE[n];
		if (n < 16) return BRIGHT[n - 8];
		if (n < 232) {
			const i = n - 16;
			const v = (x: number) => (x === 0 ? 0 : 55 + x * 40);
			return `rgb(${v(Math.floor(i / 36))},${v(Math.floor((i % 36) / 6))},${v(i % 6)})`;
		}
		const gray = 8 + (n - 232) * 10;
		return `rgb(${gray},${gray},${gray})`;
	}

	function applySgr(params: string, s: Sgr) {
		const codes = params === '' ? [0] : params.split(';').map((c) => Number(c));
		for (let i = 0; i < codes.length; i++) {
			const c = codes[i];
			if (Number.isNaN(c)) continue;
			if (c === 0) {
				s.bold = s.dim = s.italic = s.underline = false;
				s.fg = s.bg = '';
			} else if (c === 1) s.bold = true;
			else if (c === 2) s.dim = true;
			else if (c === 3) s.italic = true;
			else if (c === 4) s.underline = true;
			else if (c === 22) s.bold = s.dim = false;
			else if (c === 23) s.italic = false;
			else if (c === 24) s.underline = false;
			else if (c >= 30 && c <= 37) s.fg = BASE[c - 30];
			else if (c >= 90 && c <= 97) s.fg = BRIGHT[c - 90];
			else if (c === 39) s.fg = '';
			else if (c >= 40 && c <= 47) s.bg = BASE[c - 40];
			else if (c >= 100 && c <= 107) s.bg = BRIGHT[c - 100];
			else if (c === 49) s.bg = '';
			else if (c === 38 || c === 48) {
				let color = '';
				if (codes[i + 1] === 5 && codes.length > i + 2) {
					color = xterm(Math.max(0, Math.min(255, codes[i + 2])));
					i += 2;
				} else if (codes[i + 1] === 2 && codes.length > i + 4) {
					color = `rgb(${codes[i + 2]},${codes[i + 3]},${codes[i + 4]})`;
					i += 4;
				}
				if (c === 38) s.fg = color;
				else s.bg = color;
			}
		}
	}

	function spanOf(text: string, s: Sgr): Span {
		const cls = [s.bold ? 'font-semibold' : '', s.dim ? 'opacity-60' : '', s.italic ? 'italic' : '', s.underline ? 'underline' : '']
			.filter(Boolean)
			.join(' ');
		const style = [s.fg ? `color:${s.fg}` : '', s.bg ? `background-color:${s.bg}` : ''].filter(Boolean).join(';');
		return { text, cls, style };
	}

	function ansi(text: string): Span[] {
		if (!text.includes('\x1b')) return [{ text, cls: '', style: '' }];
		const s: Sgr = { bold: false, dim: false, italic: false, underline: false, fg: '', bg: '' };
		const out: Span[] = [];
		let last = 0;
		for (const m of text.matchAll(ANSI_RE)) {
			const idx = m.index ?? 0;
			if (idx > last) out.push(spanOf(text.slice(last, idx), s));
			last = idx + m[0].length;
			const seq = m[0];
			if (seq.startsWith('\x1b[') && seq.endsWith('m')) applySgr(seq.slice(2, -1), s);
		}
		if (last < text.length) out.push(spanOf(text.slice(last), s));
		return out;
	}

	function kindClass(kind: LineKind): string {
		switch (kind) {
			case 'error':
				return 'bg-red-500/10 text-red-300';
			case 'warning':
				return 'bg-amber-500/10 text-amber-200';
			case 'notice':
				return 'text-sky-300';
			case 'debug':
				return 'text-white/40';
			default:
				return '';
		}
	}

	function kindPrefix(kind: LineKind): string {
		switch (kind) {
			case 'error':
				return 'Error: ';
			case 'warning':
				return 'Warning: ';
			case 'notice':
				return 'Notice: ';
			case 'debug':
				return '[debug] ';
			default:
				return '';
		}
	}
</script>

<div class="rounded-xl border border-border/60 overflow-hidden bg-[#0d1117] text-[#e6edf3]">
	<div class="flex items-center gap-2 px-3 py-2 border-b border-white/10 bg-[#161b22] text-[12px] flex-wrap">
		<div class="relative w-full sm:w-56">
			<Search class="h-3.5 w-3.5 text-white/40 absolute left-2.5 top-1/2 -translate-y-1/2" />
			<Input
				bind:value={query}
				placeholder="Search log"
				class="h-7 pl-8 text-[12px] bg-white/5 border-white/15 text-inherit placeholder:text-white/30"
				onkeydown={(e) => {
					if (e.key === 'Enter') {
						e.preventDefault();
						jump(e.shiftKey ? -1 : 1);
					}
				}}
			/>
		</div>
		{#if needle}
			<span class="tabular-nums text-white/50">{matches.length ? matchIndex + 1 : 0} / {matches.length}</span>
			<Button variant="ghost" size="sm" class="h-7 px-1.5 text-inherit hover:bg-white/10 hover:text-inherit" onclick={() => jump(-1)} disabled={!matches.length} title="Previous match">
				<ChevronUp class="h-3.5 w-3.5" />
			</Button>
			<Button variant="ghost" size="sm" class="h-7 px-1.5 text-inherit hover:bg-white/10 hover:text-inherit" onclick={() => jump(1)} disabled={!matches.length} title="Next match">
				<ChevronDown class="h-3.5 w-3.5" />
			</Button>
		{/if}
		<div class="ml-auto flex items-center gap-1">
			{#if streaming && !finished}
				<span class="inline-flex items-center gap-1 text-white/50 mr-1"><Loader2 class="h-3 w-3 animate-spin" />Live</span>
			{/if}
			<span class="text-white/40 tabular-nums hidden sm:inline mr-1">{lines.length.toLocaleString()} lines</span>
			<Button variant="ghost" size="sm" class="h-7 px-2 text-[11px] text-inherit hover:bg-white/10 hover:text-inherit" onclick={() => setAll(true)} disabled={groups.length === 0}>Expand all</Button>
			<Button variant="ghost" size="sm" class="h-7 px-2 text-[11px] text-inherit hover:bg-white/10 hover:text-inherit" onclick={() => setAll(false)} disabled={groups.length === 0}>Collapse all</Button>
			<Button variant="ghost" size="sm" class={cn('h-7 px-2 text-inherit hover:bg-white/10 hover:text-inherit', showTimes && 'bg-white/15')} onclick={() => (showTimes = !showTimes)} title="Toggle timestamps">
				<Clock class="h-3.5 w-3.5" />
			</Button>
			{#if rawUrl}
				<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
				<a href={rawUrl} download class="inline-flex items-center h-7 px-2 rounded-md hover:bg-white/10" title="Download raw log">
					<Download class="h-3.5 w-3.5" />
				</a>
			{:else if lines.length > 0}
				<Button variant="ghost" size="sm" class="h-7 px-2 text-inherit hover:bg-white/10 hover:text-inherit" onclick={download} title="Download raw log">
					<Download class="h-3.5 w-3.5" />
				</Button>
			{/if}
		</div>
	</div>

	{#if error}
		<div class="px-3 py-1.5 text-[12px] bg-red-500/10 text-red-300 border-b border-white/10">
			Log stream interrupted: {error}{follow ? '. Reconnecting...' : ''}
		</div>
	{/if}
	{#if truncated > 0}
		<div class="px-3 py-1.5 text-[12px] bg-amber-500/10 text-amber-200 border-b border-white/10">
			Showing the last {MAX_RENDER.toLocaleString()} of {visible.length.toLocaleString()} lines. Download the raw log for everything.
		</div>
	{/if}

	<div class="relative">
		<div bind:this={container} onscroll={onScroll} class="relative max-h-[70vh] min-h-32 overflow-auto font-mono text-[12px] leading-5 py-1">
			{#if rendered.length === 0}
				<div class="px-4 py-6 text-white/50 text-[12.5px]">
					{streaming ? 'Waiting for output...' : finished ? 'This job produced no output.' : 'No log available.'}
				</div>
			{/if}
			{#each rendered as l (l.n)}
				{#if l.kind === 'group'}
					{@const g = groups[l.group]}
					{@const open = !isClosed(l.group)}
					{@const step = g?.step !== undefined ? stepByNumber(g.step) : undefined}
					{@const ms = step ? durationOf(step.startedAt, step.finishedAt) : undefined}
					<div
						id="{prefix}L{l.n}"
						class={cn('flex items-center pr-3 hover:bg-white/5', highlight === l.n && 'bg-sky-500/15', matchSet.has(l.n) && 'bg-yellow-500/10')}
						style="padding-left: {l.depth * 16}px"
					>
						<a href="#L{l.n}" class="w-12 shrink-0 text-right pr-2 text-white/30 hover:text-white/70 select-none tabular-nums" onclick={(e) => selectLine(e, l.n)}>{l.n}</a>
						{#if showTimes}
							<span class="w-20 shrink-0 text-white/30 tabular-nums">{timeOf(l)}</span>
						{/if}
						<button type="button" class="flex-1 min-w-0 flex items-center gap-1.5 text-left font-medium text-white/90" onclick={() => toggle(l.group)}>
							<ChevronRight class={cn('h-3.5 w-3.5 shrink-0 transition-transform text-white/50', open && 'rotate-90')} />
							{#if step}
								<RunStatusIcon status={step.status} conclusion={step.conclusion} class="h-3.5 w-3.5" />
							{/if}
							<span class="truncate">{l.raw}</span>
							{#if ms !== undefined}
								<span class="ml-auto pl-3 text-white/40 tabular-nums font-normal">{formatDuration(ms)}</span>
							{/if}
						</button>
					</div>
				{:else}
					<div
						id="{prefix}L{l.n}"
						class={cn('flex items-start pr-3 hover:bg-white/5', kindClass(l.kind), highlight === l.n && 'bg-sky-500/15', matchSet.has(l.n) && 'bg-yellow-500/10')}
						style="padding-left: {l.depth * 16}px"
					>
						<a href="#L{l.n}" class="w-12 shrink-0 text-right pr-2 text-white/30 hover:text-white/70 select-none tabular-nums" onclick={(e) => selectLine(e, l.n)}>{l.n}</a>
						{#if showTimes}
							<span class="w-20 shrink-0 text-white/30 tabular-nums">{timeOf(l)}</span>
						{/if}
						<span class="flex-1 min-w-0 whitespace-pre-wrap break-all">{#if l.kind !== 'text'}<span class="font-semibold">{kindPrefix(l.kind)}</span>{/if}{#each ansi(l.raw) as s, i (i)}<span class={s.cls} style={s.style}>{s.text}</span>{/each}</span>
					</div>
				{/if}
			{/each}
		</div>
		{#if follow && !pinned && !finished}
			<button type="button" class="absolute bottom-3 right-4 inline-flex items-center gap-1.5 rounded-full bg-primary text-primary-foreground text-[12px] px-3 py-1 shadow-lg hover:bg-primary/90" onclick={resume}>
				<ArrowDown class="h-3.5 w-3.5" />Resume auto scroll
			</button>
		{/if}
	</div>
</div>
