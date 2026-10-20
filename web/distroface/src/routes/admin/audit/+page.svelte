<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge, type BadgeVariant } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Table,
		TableBody,
		TableCell,
		TableHead,
		TableHeader,
		TableRow
	} from '$lib/components/ui/table';
	import EmptyState from '$lib/components/empty-state.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import { ScrollText, RefreshCw, ArrowUp, ArrowDown, Loader2 } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import type { AuditEvent } from '$lib/proto/distroface/v1/audit_pb';

	let events = $state<AuditEvent[]>([]);
	let loading = $state(true);
	let loaded = $state(false);
	let loadingMore = $state(false);
	let sortDesc = $state(true);
	let tableWrap = $state<HTMLDivElement | null>(null);
	const pager = new Pager(50);
	const filter = new QueryFilter([
		{ key: 'action', label: 'Action' },
		{ key: 'actor', label: 'Actor' },
		{ key: 'outcome', label: 'Outcome' },
		{ key: 'resource', label: 'Resource' },
		{ key: 'source_ip', label: 'Source IP' }
	]);

	function pageRequest() {
		return pager.request(filter.request(), `created_at ${sortDesc ? 'desc' : 'asc'}`);
	}

	async function loadEvents() {
		loading = true;
		pager.reset();
		try {
			const resp = await rpcClient.audit.listAuditEvents({ page: pageRequest() });
			events = resp.events;
			pager.apply(resp.page);
			tableWrap?.querySelector('[data-slot=table-container]')?.scrollTo({ top: 0 });
		} catch {
			// error interceptor
		} finally {
			loading = false;
			loaded = true;
		}
	}

	async function loadMore() {
		if (loading || loadingMore || !pager.next()) return;
		loadingMore = true;
		try {
			const resp = await rpcClient.audit.listAuditEvents({ page: pageRequest() });
			events = [...events, ...resp.events];
			pager.apply(resp.page);
		} catch {
			// Error interceptor, back a page so scrolling retries
			pager.prev();
		} finally {
			loadingMore = false;
		}
	}

	function onTableScroll(e: Event) {
		const el = e.target as HTMLElement;
		if (el.scrollHeight - el.scrollTop - el.clientHeight < 200) loadMore();
	}

	function toggleSort() {
		sortDesc = !sortDesc;
		loadEvents();
	}

	function filterChanged() {
		loadEvents();
	}

	function outcomeVariant(outcome: string): BadgeVariant {
		switch (outcome) {
			case 'success':
				return 'success';
			case 'denied':
				return 'warning';
			default:
				return 'destructive';
		}
	}

	function fullTime(event: AuditEvent): string {
		return event.createdAt ? timestampDate(event.createdAt).toLocaleString() : '';
	}

	onMount(() => {
		if (!authStore.hasPermission('settings', 'read')) {
			goto(resolve('/admin'));
			return;
		}
		loadEvents();
	});
</script>

<div class="space-y-4">
	<div class="section-header flex-wrap">
		<div class="min-w-0">
			<h2 class="section-title">Audit log</h2>
			<p class="section-subtitle">
				{#if pager.totalCount > 0}
					{pager.totalCount.toLocaleString()} recorded event{pager.totalCount !== 1 ? 's' : ''}
				{:else}
					Logins and administrative changes across the instance
				{/if}
			</p>
		</div>
		<div class="flex shrink-0 items-center gap-2">
			<div class="w-96">
				<QueryFilterBar {filter} placeholder="Search audit events" onchange={filterChanged} />
			</div>
			<Button
				variant="outline"
				size="icon"
				title="Refresh"
				aria-label="Refresh"
				onclick={loadEvents}
			>
				<RefreshCw />
			</Button>
		</div>
	</div>

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 6 }, i (i)}
				<div class="flex items-center gap-4 px-4 py-3">
					<Skeleton class="h-3.5 w-16" />
					<Skeleton class="h-3.5 w-24" />
					<Skeleton class="h-3.5 w-40" />
					<Skeleton class="h-4 w-14 rounded-full" />
				</div>
			{/each}
		</div>
	{:else if events.length === 0}
		<EmptyState
			message={filter.active ? 'No matching events' : 'No audit events yet'}
			description={filter.active
				? 'Search matches action, actor, and resource, or add a filter for an exact field.'
				: 'Logins, permission denials, and administrative changes appear here as they happen.'}
			icon={ScrollText}
		/>
	{:else}
		<div
			bind:this={tableWrap}
			onscrollcapture={onTableScroll}
			class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}
				**:data-[slot=table-container]:max-h-[65vh] **:data-[slot=table-container]:overflow-y-auto"
		>
			<Table>
				<TableHeader class="sticky top-0 z-10 bg-card">
					<TableRow>
						<TableHead>
							<button
								type="button"
								class="flex items-center gap-1 transition-colors hover:text-foreground"
								title={sortDesc
									? 'Newest first, click for oldest first'
									: 'Oldest first, click for newest first'}
								onclick={toggleSort}
							>
								Time
								{#if sortDesc}
									<ArrowDown class="size-3" />
								{:else}
									<ArrowUp class="size-3" />
								{/if}
							</button>
						</TableHead>
						<TableHead>Actor</TableHead>
						<TableHead>Action</TableHead>
						<TableHead>Outcome</TableHead>
						<TableHead>Source IP</TableHead>
						<TableHead>Detail</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each events as event (event.id)}
						<TableRow>
							<TableCell>
								<span class="text-sm text-muted-foreground tabular-nums" title={fullTime(event)}>
									{event.createdAt ? relativeTime(timestampDate(event.createdAt)) : '-'}
								</span>
							</TableCell>
							<TableCell>
								{#if event.actor}
									<span class="text-sm font-medium">{event.actor}</span>
								{:else}
									<span class="text-sm text-muted-foreground italic">anonymous</span>
								{/if}
							</TableCell>
							<TableCell>
								<div class="flex flex-wrap items-center gap-2">
									<code class="rounded-md bg-muted px-1.5 py-0.5 font-mono text-xs"
										>{event.action}</code
									>
									{#if event.resource}
										<Badge variant="outline">{event.resource}</Badge>
									{/if}
								</div>
							</TableCell>
							<TableCell>
								<Badge variant={outcomeVariant(event.outcome)}>{event.outcome}</Badge>
							</TableCell>
							<TableCell>
								<span class="font-mono text-xs text-muted-foreground">{event.sourceIp || '-'}</span>
							</TableCell>
							<TableCell class="max-w-64">
								<span class="block truncate text-xs text-muted-foreground" title={event.detail}>
									{event.detail || '-'}
								</span>
							</TableCell>
						</TableRow>
					{/each}
					{#if loadingMore}
						<TableRow class="hover:bg-transparent">
							<TableCell colspan={6}>
								<div class="flex items-center justify-center gap-2 text-xs text-muted-foreground">
									<Loader2 class="size-3.5 animate-spin" />
									Loading more events
								</div>
							</TableCell>
						</TableRow>
					{/if}
				</TableBody>
			</Table>
		</div>
	{/if}
</div>
