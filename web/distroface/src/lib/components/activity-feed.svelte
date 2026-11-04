<script lang="ts">
	import { onMount } from 'svelte';
	import { rpcClient, silentCallOptions, backgroundCallOptions } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import EmptyState from '$lib/components/empty-state.svelte';
	import ActivityItem from '$lib/components/activity-item.svelte';
	import { Activity, Loader, RefreshCw } from '$lib/icons';
	import { ActivityFeed, type ActivityEvent } from '$lib/proto/distroface/v1/activity_pb';

	let {
		feed = ActivityFeed.PUBLIC,
		actor = '',
		namespace = '',
		kinds = [],
		pageSize = 25,
		compact = false,
		poll = 0,
		emptyMessage = 'Nothing has happened yet',
		emptyDescription = 'Pushes, releases, issues, stars, and uploads show up here as they happen.',
		class: className = ''
	}: {
		feed?: ActivityFeed;
		actor?: string;
		namespace?: string;
		kinds?: string[];
		pageSize?: number;
		compact?: boolean;
		// Seconds between silent refreshes, zero disables
		poll?: number;
		emptyMessage?: string;
		emptyDescription?: string;
		class?: string;
	} = $props();

	let events = $state<ActivityEvent[]>([]);
	let loading = $state(true);
	let loadingMore = $state(false);
	let total = $state(0);
	let offset = 0;
	let generation = 0;

	// Offsets ride the same base64 cursor as every list rpc
	function request(off: number) {
		return {
			page: { pageSize, pageToken: off > 0 ? btoa(String(off)) : '', orderBy: '' },
			feed,
			actor,
			namespace,
			kinds
		};
	}

	async function load(silent = false) {
		const mine = ++generation;
		if (!silent) loading = true;
		try {
			const resp = await rpcClient.activity.listActivity(
				request(0),
				silent ? backgroundCallOptions : silentCallOptions
			);
			if (mine !== generation) return;
			events = resp.events;
			total = Number(resp.page?.totalCount ?? 0n);
			offset = resp.events.length;
		} catch {
			if (mine !== generation) return;
			events = [];
			total = 0;
		} finally {
			if (mine === generation) loading = false;
		}
	}

	async function more() {
		if (loadingMore) return;
		loadingMore = true;
		const mine = generation;
		try {
			const resp = await rpcClient.activity.listActivity(request(offset), silentCallOptions);
			if (mine !== generation) return;
			const seen = new Set(events.map((e) => e.id));
			events = [...events, ...resp.events.filter((e) => !seen.has(e.id))];
			offset += resp.events.length;
			total = Number(resp.page?.totalCount ?? 0n);
		} catch {
			// Error already surfaced
		} finally {
			loadingMore = false;
		}
	}

	const hasMore = $derived(events.length < total);

	// Inputs change, the list starts over
	$effect(() => {
		void [feed, actor, namespace, kinds.join(',')];
		void load();
	});

	onMount(() => {
		if (!poll) return;
		const timer = setInterval(() => {
			if (document.visibilityState === 'visible') void load(true);
		}, poll * 1000);
		return () => clearInterval(timer);
	});

	export function refresh() {
		return load(true);
	}
</script>

<div class={className}>
	{#if loading && events.length === 0}
		<div class="space-y-4 {compact ? '' : 'px-4 py-3'}">
			{#each { length: 5 }, i (i)}
				<div class="flex items-center gap-3">
					<Skeleton class="size-8 rounded-full" />
					<div class="flex-1 space-y-2">
						<Skeleton class="h-3.5 w-2/3" />
						<Skeleton class="h-3 w-1/3" />
					</div>
				</div>
			{/each}
		</div>
	{:else if events.length === 0}
		{#if compact}
			<p class="py-6 text-center text-sm text-muted-foreground">{emptyMessage}</p>
		{:else}
			<EmptyState icon={Activity} message={emptyMessage} description={emptyDescription} />
		{/if}
	{:else}
		<div class={compact ? 'divide-y' : 'box divide-y'}>
			{#each events as ev (ev.id)}
				<ActivityItem event={ev} {compact} />
			{/each}
		</div>
		{#if hasMore}
			<div class="mt-3 flex justify-center">
				<Button variant="outline" size="sm" onclick={more} disabled={loadingMore}>
					{#if loadingMore}
						<Loader class="animate-spin" />
					{:else}
						<RefreshCw />
					{/if}
					Load more
				</Button>
			</div>
		{/if}
	{/if}
</div>
