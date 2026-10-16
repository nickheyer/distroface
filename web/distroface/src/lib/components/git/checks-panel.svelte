<script lang="ts">
	import { CheckCircle2, XCircle, Clock, AlertCircle, ExternalLink, ChevronDown, ChevronRight, RefreshCw, CircleDashed } from '@lucide/svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { CommitStatusState } from '$lib/proto/distroface/v1/git_pb';
	import type { GitCommitStatus } from '$lib/proto/distroface/v1/git_pb';
	import { relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { SvelteMap, SvelteSet } from 'svelte/reactivity';

	let {
		namespace,
		name,
		sha,
		onState
	}: {
		namespace: string;
		name: string;
		sha: string;
		onState?: (state: CommitStatusState) => void;
	} = $props();

	type Group = { context: string; latest: GitCommitStatus; history: GitCommitStatus[] };

	let statuses = $state<GitCommitStatus[]>([]);
	let loading = $state(true);
	let refreshing = $state(false);
	let seq = 0;
	let timer: ReturnType<typeof setTimeout> | undefined;
	const openHistory = new SvelteSet<string>();

	// Newest first, so the first per context is current
	const groups = $derived.by(() => {
		const map = new SvelteMap<string, Group>();
		for (const s of statuses) {
			const g = map.get(s.context);
			if (g) g.history.push(s);
			else map.set(s.context, { context: s.context, latest: s, history: [] });
		}
		return [...map.values()].sort((a, b) => a.context.localeCompare(b.context));
	});

	const combined = $derived.by(() => {
		if (groups.length === 0) return CommitStatusState.UNSPECIFIED;
		let pending = false;
		for (const g of groups) {
			const s = g.latest.state;
			if (s === CommitStatusState.FAILURE || s === CommitStatusState.ERROR) return CommitStatusState.FAILURE;
			if (s === CommitStatusState.PENDING) pending = true;
		}
		return pending ? CommitStatusState.PENDING : CommitStatusState.SUCCESS;
	});

	const counts = $derived({
		success: groups.filter((g) => g.latest.state === CommitStatusState.SUCCESS).length,
		pending: groups.filter((g) => g.latest.state === CommitStatusState.PENDING).length,
		failed: groups.filter((g) => g.latest.state === CommitStatusState.FAILURE || g.latest.state === CommitStatusState.ERROR).length
	});

	async function load(silent = false) {
		if (!sha) {
			statuses = [];
			loading = false;
			return;
		}
		const mine = ++seq;
		if (!silent) loading = true;
		else refreshing = true;
		try {
			const resp = await rpcClient.gitFiles.listCommitStatuses(
				{ namespace, name, sha, page: { pageSize: 200, pageToken: '', orderBy: '' } },
				silentCallOptions
			);
			if (mine !== seq) return;
			statuses = resp.statuses;
		} catch {
			if (mine !== seq) return;
			statuses = [];
		} finally {
			if (mine === seq) {
				loading = false;
				refreshing = false;
			}
		}
	}

	$effect(() => {
		void sha;
		void namespace;
		void name;
		load();
	});

	$effect(() => {
		onState?.(combined);
	});

	// Poll while any check is still running
	$effect(() => {
		void statuses;
		clearTimeout(timer);
		if (combined !== CommitStatusState.PENDING) return;
		timer = setTimeout(() => load(true), 15000);
		return () => clearTimeout(timer);
	});

	function icon(s: CommitStatusState) {
		switch (s) {
			case CommitStatusState.SUCCESS:
				return CheckCircle2;
			case CommitStatusState.FAILURE:
				return XCircle;
			case CommitStatusState.ERROR:
				return AlertCircle;
			case CommitStatusState.PENDING:
				return Clock;
			default:
				return CircleDashed;
		}
	}

	function color(s: CommitStatusState): string {
		switch (s) {
			case CommitStatusState.SUCCESS:
				return 'text-success';
			case CommitStatusState.FAILURE:
			case CommitStatusState.ERROR:
				return 'text-destructive';
			case CommitStatusState.PENDING:
				return 'text-amber-500';
			default:
				return 'text-muted-foreground';
		}
	}

	function headline(): string {
		switch (combined) {
			case CommitStatusState.SUCCESS:
				return 'All checks passed';
			case CommitStatusState.FAILURE:
				return counts.failed === 1 ? '1 check failed' : `${counts.failed} checks failed`;
			case CommitStatusState.PENDING:
				return counts.pending === 1 ? '1 check in progress' : `${counts.pending} checks in progress`;
			default:
				return 'No checks reported';
		}
	}

	function toggleHistory(ctx: string) {
		if (openHistory.has(ctx)) openHistory.delete(ctx);
		else openHistory.add(ctx);
	}

	function when(s: GitCommitStatus): string {
		return s.createdAt ? relativeTime(timestampDate(s.createdAt)) : '';
	}
</script>

<div class="rounded-xl border border-border/60 bg-card overflow-hidden">
	<div class="flex items-center gap-2 px-3 py-2 border-b border-border/50 bg-muted/20">
		{#if loading}
			<Skeleton class="h-4 w-4 rounded-full" />
			<Skeleton class="h-4 w-32" />
		{:else}
			{@const Icon = icon(combined)}
			<Icon class="h-4 w-4 {color(combined)}" />
			<span class="text-[13px] font-medium">{headline()}</span>
			{#if groups.length > 0}
				<span class="text-[11.5px] text-muted-foreground tabular-nums">
					{counts.success} passed{counts.pending ? `, ${counts.pending} pending` : ''}{counts.failed ? `, ${counts.failed} failed` : ''}
				</span>
			{/if}
		{/if}
		<Button variant="ghost" size="icon" class="h-6 w-6 ml-auto" onclick={() => load(true)} disabled={refreshing || loading} title="Refresh">
			<RefreshCw class="h-3 w-3 text-muted-foreground {refreshing ? 'animate-spin' : ''}" />
		</Button>
	</div>
	{#if loading}
		<div class="px-3 py-2 space-y-2">
			<Skeleton class="h-4 w-full" />
			<Skeleton class="h-4 w-4/5" />
		</div>
	{:else if groups.length === 0}
		<p class="px-3 py-3 text-[12.5px] text-muted-foreground">No statuses on {sha ? sha.slice(0, 7) : 'this commit'} yet.</p>
	{:else}
		<ul class="divide-y divide-border/50">
			{#each groups as g (g.context)}
				{@const Icon = icon(g.latest.state)}
				<li class="px-3 py-2 text-[12.5px]">
					<div class="flex items-center gap-2 min-w-0">
						<Icon class="h-3.5 w-3.5 shrink-0 {color(g.latest.state)}" />
						<span class="font-medium truncate">{g.context}</span>
						{#if g.latest.description}
							<span class="text-muted-foreground truncate min-w-0 hidden sm:inline">{g.latest.description}</span>
						{/if}
						<span class="ml-auto flex items-center gap-1.5 shrink-0">
							<span class="text-[11px] text-muted-foreground">{when(g.latest)}</span>
							{#if g.latest.targetUrl}
								<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
								<a href={g.latest.targetUrl} target="_blank" rel="noopener noreferrer" class="text-primary hover:underline inline-flex items-center gap-1 text-[11.5px]">
									Details<ExternalLink class="h-3 w-3" />
								</a>
							{/if}
							{#if g.history.length > 0}
								<button type="button" class="text-muted-foreground hover:text-foreground inline-flex items-center gap-0.5 text-[11px]" onclick={() => toggleHistory(g.context)}>
									{#if openHistory.has(g.context)}<ChevronDown class="h-3 w-3" />{:else}<ChevronRight class="h-3 w-3" />{/if}
									{g.history.length} earlier
								</button>
							{/if}
						</span>
					</div>
					{#if openHistory.has(g.context)}
						<ul class="mt-1.5 ml-5 space-y-1">
							{#each g.history as h (h.id)}
								{@const HIcon = icon(h.state)}
								<li class="flex items-center gap-2 text-[12px] text-muted-foreground">
									<HIcon class="h-3 w-3 shrink-0 {color(h.state)}" />
									<span class="truncate">{h.description || h.creator || h.context}</span>
									<span class="ml-auto text-[11px] shrink-0">{when(h)}</span>
									{#if h.targetUrl}
										<!-- eslint-disable-next-line svelte/no-navigation-without-resolve -->
										<a href={h.targetUrl} target="_blank" rel="noopener noreferrer" class="text-primary hover:underline shrink-0"><ExternalLink class="h-3 w-3" /></a>
									{/if}
								</li>
							{/each}
						</ul>
					{/if}
				</li>
			{/each}
		</ul>
	{/if}
</div>
