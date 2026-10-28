<script lang="ts">
	import { tick, untrack } from 'svelte';
	import {
		Bot,
		User,
		Wrench,
		ChevronRight,
		AlertCircle,
		CheckCircle2,
		ShieldAlert,
		ShieldCheck,
		ShieldX,
		Info,
		Loader2,
		ArrowDown,
		Terminal,
		FileText
	} from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import Markdown from '$lib/components/git/markdown.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { ConnectError } from '@connectrpc/connect';
	import {
		AgentEventKind,
		AgentSessionStatus,
		type AgentSessionEvent
	} from '$lib/proto/distroface/v1/agents_pb';
	import { parseEventData, summarizeArgs } from './agents-state';
	import { cn } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';

	const RETRY_MS = 3000;

	let {
		namespace,
		name,
		sessionId,
		active = false,
		onStatus,
		onEvent
	}: {
		namespace: string;
		name: string;
		sessionId: string;
		// Keeps the stream open and auto scrolls while the session works
		active?: boolean;
		onStatus?: (status: AgentSessionStatus, done: boolean) => void;
		onEvent?: (ev: AgentSessionEvent) => void;
	} = $props();

	interface Row {
		key: string;
		event: AgentSessionEvent;
		// Result paired with a tool call
		result?: AgentSessionEvent;
		// Approval decision paired with its request
		decision?: AgentSessionEvent;
	}

	let events = $state.raw<AgentSessionEvent[]>([]);
	let streaming = $state(false);
	let finished = $state(false);
	let error = $state('');
	let open = $state<Record<string, boolean>>({});
	let pinned = $state(true);
	let container = $state<HTMLDivElement | undefined>();
	let abort: AbortController | null = null;

	// Tool results and approval decisions fold into the row that asked
	const rows = $derived.by(() => {
		const out: Row[] = [];
		const callRows: Record<string, Row> = {};
		const approvalRows: Record<string, Row> = {};
		const results = events.filter((e) => e.kind === AgentEventKind.RESULT);
		for (const ev of events) {
			const data = parseEventData(ev.data);
			switch (ev.kind) {
				case AgentEventKind.ASSISTANT: {
					// A closing reply shows once, as the result it became
					const calls = Array.isArray(data.tool_calls) ? data.tool_calls.length : 0;
					const text = ev.text.trim();
					const next = results.find((r) => r.seq > ev.seq);
					if (!calls && text && next?.text.trim().startsWith(text)) continue;
					break;
				}
				case AgentEventKind.TOOL_RESULT: {
					const row = callRows[ev.toolCallId];
					if (row) {
						row.result = ev;
						continue;
					}
					break;
				}
				case AgentEventKind.APPROVAL: {
					const id = String(data.approval_id ?? '');
					const prior = approvalRows[id];
					if (prior && data.status !== 'pending') {
						prior.decision = ev;
						continue;
					}
					const row: Row = { key: `e${ev.seq}`, event: ev };
					if (id) approvalRows[id] = row;
					out.push(row);
					continue;
				}
				case AgentEventKind.USAGE:
					continue;
			}
			const row: Row = { key: `e${ev.seq}`, event: ev };
			if (ev.kind === AgentEventKind.TOOL_CALL && ev.toolCallId) callRows[ev.toolCallId] = row;
			out.push(row);
		}
		return out;
	});

	function reset() {
		events = [];
		finished = false;
		error = '';
		pinned = true;
	}

	async function stream(id: string, ac: AbortController) {
		let after = 0n;
		while (!ac.signal.aborted) {
			streaming = true;
			try {
				const chunks = rpcClient.agents.streamAgentSessionEvents(
					{ namespace, name, id, afterSeq: after },
					{ ...silentCallOptions, signal: ac.signal }
				);
				for await (const ev of chunks) {
					if (ev.done) {
						finished = true;
						streaming = false;
						onStatus?.(ev.status, true);
						return;
					}
					if (ev.seq > after) {
						after = ev.seq;
						events = [...events, ev];
						onStatus?.(ev.status, false);
						onEvent?.(ev);
					}
				}
				error = '';
			} catch (err) {
				if (ac.signal.aborted) return;
				error = err instanceof ConnectError ? err.rawMessage || err.message : String(err);
			}
			if (ac.signal.aborted) return;
			await new Promise((r) => setTimeout(r, RETRY_MS));
		}
		streaming = false;
	}

	// Derived id keeps parent re-renders from restarting the stream
	const streamId = $derived(sessionId);

	$effect(() => {
		const id = streamId;
		const ac = new AbortController();
		abort?.abort();
		abort = ac;
		untrack(() => {
			reset();
			void stream(id, ac);
		});
		return () => ac.abort();
	});

	function onScroll() {
		if (!container) return;
		pinned = container.scrollHeight - container.scrollTop - container.clientHeight < 64;
	}

	function resume() {
		pinned = true;
		container?.scrollTo({ top: container.scrollHeight });
	}

	// Markdown lands after the row did, keep the bottom in view
	function follow() {
		if (active && pinned && container) container.scrollTo({ top: container.scrollHeight });
	}

	$effect(() => {
		void rows.length;
		if (!active || !pinned || !container) return;
		tick().then(() => {
			if (container && pinned) container.scrollTo({ top: container.scrollHeight });
		});
	});

	function toggle(key: string) {
		open[key] = !open[key];
	}

	function timeOf(ev: AgentSessionEvent): string {
		return ev.createdAt
			? timestampDate(ev.createdAt).toLocaleTimeString([], { hour12: false })
			: '';
	}

	function pretty(v: unknown): string {
		try {
			return JSON.stringify(v, null, 2);
		} catch {
			return String(v);
		}
	}

	function resultOk(row: Row): boolean | undefined {
		if (!row.result) return undefined;
		const data = parseEventData(row.result.data);
		return data.ok !== false;
	}
</script>

<div class="overflow-hidden rounded-lg border bg-card">
	<div class="flex flex-wrap items-center gap-2 border-b bg-muted/50 px-3 py-2 text-xs">
		<span class="font-medium">Transcript</span>
		<span class="text-muted-foreground tabular-nums">{events.length} events</span>
		<div class="ml-auto flex items-center gap-2">
			{#if streaming && !finished}
				<span class="inline-flex items-center gap-1 text-muted-foreground"
					><Loader2 class="size-3 animate-spin" />Live</span
				>
			{/if}
		</div>
	</div>
	<div class="relative">
		{#if error}
			<div
				class="absolute inset-x-0 top-0 z-10 bg-destructive/10 px-3 py-1.5 text-xs text-destructive backdrop-blur-sm"
			>
				Stream interrupted: {error}, reconnecting
			</div>
		{/if}
		<div
			bind:this={container}
			onscroll={onScroll}
			class={cn(
				'transcript relative overflow-auto px-4 py-3',
				active && !finished ? 'h-[70vh]' : 'max-h-[75vh] min-h-32'
			)}
		>
			{#if rows.length === 0}
				<p class="py-6 text-center text-sm text-muted-foreground">
					{streaming ? 'Waiting for the first event' : 'No transcript yet.'}
				</p>
			{/if}
			<div class="space-y-3">
				{#each rows as row (row.key)}
					{@const ev = row.event}
					{@const data = parseEventData(ev.data)}
					{#if ev.kind === AgentEventKind.USER}
						<div class="flex gap-3">
							<div
								class="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground"
							>
								<User class="size-3.5" />
							</div>
							<div class="min-w-0 flex-1 rounded-lg border bg-muted/30 px-3 py-2 text-sm">
								<div class="mb-1 flex items-center gap-2 text-xs text-muted-foreground">
									<span class="font-medium text-foreground">{data.user || 'Instruction'}</span>
									<span>{timeOf(ev)}</span>
								</div>
								<Markdown source={ev.text} onRendered={follow} />
							</div>
						</div>
					{:else if ev.kind === AgentEventKind.SYSTEM}
						<div class="flex gap-3">
							<div
								class="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full bg-muted text-muted-foreground"
							>
								<FileText class="size-3.5" />
							</div>
							<div class="min-w-0 flex-1">
								<button
									type="button"
									class="flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground"
									onclick={() => toggle(row.key)}
									aria-expanded={!!open[row.key]}
								>
									<ChevronRight
										class={cn('size-3.5 transition-transform', open[row.key] && 'rotate-90')}
									/>
									System prompt
								</button>
								{#if open[row.key]}
									<pre
										class="mt-2 max-h-96 overflow-auto rounded-md border bg-muted/30 p-3 font-mono text-xs whitespace-pre-wrap">{ev.text}</pre>
								{/if}
							</div>
						</div>
					{:else if ev.kind === AgentEventKind.ASSISTANT}
						{#if ev.text.trim()}
							<div class="flex gap-3">
								<div
									class="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full bg-primary/10 text-primary"
								>
									<Bot class="size-3.5" />
								</div>
								<div class="min-w-0 flex-1 text-sm">
									<div class="mb-1 text-xs text-muted-foreground">{timeOf(ev)}</div>
									<Markdown source={ev.text} onRendered={follow} />
								</div>
							</div>
						{/if}
					{:else if ev.kind === AgentEventKind.TOOL_CALL}
						{@const args = (data.arguments ?? {}) as Record<string, unknown>}
						{@const ok = resultOk(row)}
						<div class="flex gap-3">
							<div class="w-6 shrink-0"></div>
							<div class="min-w-0 flex-1 rounded-md border">
								<button
									type="button"
									class="flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-xs hover:bg-muted/40"
									onclick={() => toggle(row.key)}
									aria-expanded={!!open[row.key]}
								>
									<ChevronRight
										class={cn(
											'size-3.5 shrink-0 text-muted-foreground transition-transform',
											open[row.key] && 'rotate-90'
										)}
									/>
									{#if ev.tool === 'run_command'}
										<Terminal class="size-3.5 shrink-0 text-muted-foreground" />
									{:else}
										<Wrench class="size-3.5 shrink-0 text-muted-foreground" />
									{/if}
									<span class="font-mono font-medium">{ev.tool}</span>
									<span class="min-w-0 truncate text-muted-foreground"
										>{summarizeArgs(ev.tool, args)}</span
									>
									<span class="ml-auto shrink-0">
										{#if ok === undefined}
											<Loader2 class="size-3.5 animate-spin text-warning" />
										{:else if ok}
											<CheckCircle2 class="size-3.5 text-success" />
										{:else}
											<AlertCircle class="size-3.5 text-destructive" />
										{/if}
									</span>
								</button>
								{#if open[row.key]}
									<div class="space-y-2 border-t px-2.5 py-2">
										<div class="eyebrow">Arguments</div>
										<pre
											class="max-h-64 overflow-auto rounded-md bg-muted/30 p-2 font-mono text-xs whitespace-pre-wrap">{pretty(
												args
											)}</pre>
										{#if row.result}
											<div class="eyebrow">Result</div>
											<pre
												class={cn(
													'max-h-96 overflow-auto rounded-md p-2 font-mono text-xs whitespace-pre-wrap',
													ok ? 'bg-muted/30' : 'bg-destructive/10 text-destructive'
												)}>{row.result.text}</pre>
										{/if}
									</div>
								{/if}
							</div>
						</div>
					{:else if ev.kind === AgentEventKind.TOOL_RESULT}
						<div class="flex gap-3">
							<div class="w-6 shrink-0"></div>
							<div class="min-w-0 flex-1 rounded-md border px-2.5 py-1.5 text-xs">
								<span class="font-mono font-medium">{ev.tool}</span>
								<pre
									class="mt-1 max-h-64 overflow-auto font-mono whitespace-pre-wrap">{ev.text}</pre>
							</div>
						</div>
					{:else if ev.kind === AgentEventKind.APPROVAL}
						{@const decision = row.decision ? parseEventData(row.decision.data) : null}
						{@const status = String(decision?.status ?? data.status ?? 'pending')}
						<div class="flex gap-3">
							<div class="w-6 shrink-0"></div>
							<div
								class={cn(
									'min-w-0 flex-1 rounded-md border px-3 py-2 text-xs',
									status === 'pending'
										? 'border-warning/40 bg-warning/5'
										: status === 'approved'
											? 'border-success/30 bg-success/5'
											: 'border-border bg-muted/30'
								)}
							>
								<div class="flex flex-wrap items-center gap-2">
									{#if status === 'pending'}
										<ShieldAlert class="size-3.5 text-warning" />
										<span class="font-medium">Approval requested</span>
									{:else if status === 'approved'}
										<ShieldCheck class="size-3.5 text-success" />
										<span class="font-medium">Approved</span>
									{:else}
										<ShieldX class="size-3.5 text-muted-foreground" />
										<span class="font-medium">Rejected</span>
									{/if}
									<span class="truncate text-muted-foreground">{ev.text}</span>
									{#if decision?.decided_by}
										<Badge variant="outline">{decision.decided_by}</Badge>
									{/if}
								</div>
								{#if decision?.note}
									<p class="mt-1 text-muted-foreground">{decision.note}</p>
								{/if}
							</div>
						</div>
					{:else if ev.kind === AgentEventKind.ERROR}
						<div class="flex gap-3">
							<div class="w-6 shrink-0"></div>
							<div
								class="min-w-0 flex-1 rounded-md border border-destructive/30 bg-destructive/10 px-3 py-2 text-xs text-destructive"
							>
								<div class="flex items-center gap-2 font-medium">
									<AlertCircle class="size-3.5" />Error
								</div>
								<p class="mt-1 whitespace-pre-wrap">{ev.text}</p>
							</div>
						</div>
					{:else if ev.kind === AgentEventKind.RESULT}
						<div class="flex gap-3">
							<div
								class="mt-0.5 flex size-6 shrink-0 items-center justify-center rounded-full bg-success/10 text-success"
							>
								<CheckCircle2 class="size-3.5" />
							</div>
							<div
								class="min-w-0 flex-1 rounded-lg border border-success/30 bg-success/5 px-3 py-2 text-sm"
							>
								<div class="mb-1 text-xs font-medium text-muted-foreground">Summary</div>
								<Markdown source={ev.text} onRendered={follow} />
							</div>
						</div>
					{:else if ev.kind === AgentEventKind.STATUS}
						<div class="flex items-center gap-2 pl-9 text-xs text-muted-foreground">
							<Info class="size-3" />
							<span>{ev.text}</span>
							<span>{timeOf(ev)}</span>
							{#if data.url && typeof data.url === 'string'}
								<!-- eslint-disable svelte/no-navigation-without-resolve -->
								<a href={data.url} class="link">open run</a>
								<!-- eslint-enable svelte/no-navigation-without-resolve -->
							{/if}
						</div>
					{/if}
				{/each}
			</div>
		</div>
		{#if active && !pinned && !finished}
			<Button size="sm" class="absolute right-4 bottom-3 rounded-full shadow-lg" onclick={resume}>
				<ArrowDown />Follow
			</Button>
		{/if}
	</div>
</div>

<style>
	.transcript {
		overflow-anchor: none;
	}
</style>
