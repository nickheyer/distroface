<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import { ScrollText, RefreshCw } from '@lucide/svelte';
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
	const pager = new Pager(50);
	const filter = new QueryFilter([
		{ key: 'action', label: 'Action' },
		{ key: 'actor', label: 'Actor' },
		{ key: 'outcome', label: 'Outcome' },
		{ key: 'resource', label: 'Resource' },
		{ key: 'source_ip', label: 'Source IP' }
	]);

	async function loadEvents() {
		loading = true;
		try {
			const resp = await rpcClient.audit.listAuditEvents({
				page: pager.request(filter.request())
			});
			events = resp.events;
			pager.apply(resp.page);
		} catch {
			// error interceptor
		} finally {
			loading = false;
			loaded = true;
		}
	}

	function filterChanged() {
		pager.reset();
		loadEvents();
	}

	function outcomeBadgeClass(outcome: string): string {
		switch (outcome) {
			case 'success':
				return 'border-primary/30 text-primary';
			case 'denied':
				return 'border-amber-500/40 text-amber-600 dark:text-amber-400';
			default:
				return 'border-destructive/40 text-destructive';
		}
	}

	function fullTime(event: AuditEvent): string {
		return event.createdAt ? timestampDate(event.createdAt).toLocaleString() : '';
	}

	onMount(() => {
		if (!authStore.hasPermission('settings', 'read')) { goto(resolve('/admin')); return; }
		loadEvents();
	});
</script>

<div class="space-y-4">
	<div class="section-header">
		<div>
			<h2 class="section-title">Audit Log</h2>
			<p class="section-subtitle">
				{#if pager.totalCount > 0}
					{pager.totalCount.toLocaleString()} recorded event{pager.totalCount !== 1 ? 's' : ''}
				{:else}
					Logins and administrative changes across the instance
				{/if}
			</p>
		</div>
		<div class="flex items-center gap-2">
			<div class="w-96">
				<QueryFilterBar {filter} placeholder="Search audit events..." onchange={filterChanged} />
			</div>
			<Button variant="outline" size="icon" class="h-9 w-9 shrink-0" title="Refresh" onclick={loadEvents}>
				<RefreshCw class="h-4 w-4" />
			</Button>
		</div>
	</div>

	{#if !loaded}
		<div class="space-y-2">
			{#each { length: 6 }, i (i)}
				<Skeleton class="h-11 w-full rounded-lg" />
			{/each}
		</div>
	{:else if events.length === 0}
		<EmptyState
			message={filter.active ? 'No events match the current filter' : 'No audit events recorded yet'}
			description={filter.active
				? 'Search matches action, actor, and resource. Add filters for exact fields.'
				: 'Logins, permission denials, and administrative mutations will appear here as they happen.'}
			icon={ScrollText}
		/>
	{:else}
		<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			<Table>
				<TableHeader>
					<TableRow class="bg-muted/30 hover:bg-muted/30">
						<TableHead class="th">Time</TableHead>
						<TableHead class="th">Actor</TableHead>
						<TableHead class="th">Action</TableHead>
						<TableHead class="th">Outcome</TableHead>
						<TableHead class="th">Source IP</TableHead>
						<TableHead class="th">Detail</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each events as event (event.id)}
						<TableRow>
							<TableCell class="py-2.5 px-3 whitespace-nowrap">
								<span class="text-sm text-muted-foreground tabular-nums" title={fullTime(event)}>
									{event.createdAt ? relativeTime(timestampDate(event.createdAt)) : '-'}
								</span>
							</TableCell>
							<TableCell class="py-2.5 px-3">
								{#if event.actor}
									<span class="text-sm font-medium">{event.actor}</span>
								{:else}
									<span class="text-sm text-muted-foreground italic">anonymous</span>
								{/if}
							</TableCell>
							<TableCell class="py-2.5 px-3">
								<div class="flex items-center gap-2 flex-wrap">
									<code class="text-xs bg-muted px-1.5 py-0.5 rounded font-mono">{event.action}</code>
									{#if event.resource}
										<Badge variant="outline" class="text-[10px] px-1.5 py-0">{event.resource}</Badge>
									{/if}
								</div>
							</TableCell>
							<TableCell class="py-2.5 px-3">
								<Badge variant="outline" class="text-xs {outcomeBadgeClass(event.outcome)}">
									{event.outcome}
								</Badge>
							</TableCell>
							<TableCell class="py-2.5 px-3">
								<span class="text-xs text-muted-foreground font-mono">{event.sourceIp || '-'}</span>
							</TableCell>
							<TableCell class="py-2.5 px-3 max-w-64">
								<span class="text-xs text-muted-foreground truncate block" title={event.detail}>
									{event.detail || '-'}
								</span>
							</TableCell>
						</TableRow>
					{/each}
				</TableBody>
			</Table>
		</div>

		<DataPagination
			page={pager.page} pageSize={pager.pageSize} totalCount={pager.totalCount}
			onPrev={() => { if (pager.prev()) loadEvents(); }}
			onNext={() => { if (pager.next()) loadEvents(); }}
		/>
	{/if}
</div>
