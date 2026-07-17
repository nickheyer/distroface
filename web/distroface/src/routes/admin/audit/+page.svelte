<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Input } from '$lib/components/ui/input';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import { ScrollText, Search, RefreshCw } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import type { AuditEvent } from '$lib/proto/distroface/v1/audit_pb';

	let events = $state<AuditEvent[]>([]);
	let loading = $state(true);
	let totalCount = $state(0);
	let currentPage = $state(1);
	const pageSize = 50;

	let actorFilter = $state('');
	let actionFilter = $state('');
	let filterTimeout: ReturnType<typeof setTimeout> | undefined;

	let hasFilters = $derived(actorFilter.trim() !== '' || actionFilter.trim() !== '');

	async function loadEvents() {
		loading = true;
		try {
			const resp = await rpcClient.audit.listAuditEvents({
				action: actionFilter.trim(),
				actor: actorFilter.trim(),
				limit: pageSize,
				offset: (currentPage - 1) * pageSize
			});
			events = resp.events;
			totalCount = Number(resp.total);
		} catch {
			// error interceptor
		} finally {
			loading = false;
		}
	}

	function handleFilter() {
		clearTimeout(filterTimeout);
		filterTimeout = setTimeout(() => { currentPage = 1; loadEvents(); }, 300);
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
				{#if totalCount > 0}
					{totalCount.toLocaleString()} recorded event{totalCount !== 1 ? 's' : ''}
				{:else}
					Logins and administrative changes across the instance
				{/if}
			</p>
		</div>
		<div class="flex items-center gap-2">
			<div class="relative w-44">
				<Search class="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
				<Input placeholder="Filter by actor..." class="pl-9 h-9" bind:value={actorFilter} oninput={handleFilter} />
			</div>
			<div class="relative w-52">
				<Search class="absolute left-3 top-1/2 -translate-y-1/2 h-4 w-4 text-muted-foreground" />
				<Input placeholder="Filter by action..." class="pl-9 h-9" bind:value={actionFilter} oninput={handleFilter} />
			</div>
			<Button variant="outline" size="icon" class="h-9 w-9 shrink-0" title="Refresh" onclick={loadEvents}>
				<RefreshCw class="h-4 w-4" />
			</Button>
		</div>
	</div>

	{#if loading}
		<div class="space-y-2">
			{#each { length: 6 }, i (i)}
				<Skeleton class="h-11 w-full rounded-lg" />
			{/each}
		</div>
	{:else if events.length === 0}
		<EmptyState
			message={hasFilters ? 'No events match the current filters' : 'No audit events recorded yet'}
			description={hasFilters
				? 'Action filters match the full name, e.g. AuthService/Login.'
				: 'Logins, permission denials, and administrative mutations will appear here as they happen.'}
			icon={ScrollText}
		/>
	{:else}
		<div class="data-table">
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
			page={currentPage} {pageSize} {totalCount}
			onPrev={() => { currentPage--; loadEvents(); }}
			onNext={() => { currentPage++; loadEvents(); }}
		/>
	{/if}
</div>
