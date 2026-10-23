<script lang="ts">
	import { onMount } from 'svelte';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Select, SelectContent, SelectItem, SelectTrigger } from '$lib/components/ui/select';
	import {
		Table,
		TableBody,
		TableCell,
		TableHead,
		TableHeader,
		TableRow
	} from '$lib/components/ui/table';
	import StatCard from '$lib/components/stat-card.svelte';
	import { Bot, Coins, CheckCircle2, XCircle } from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import type { AgentUsageBucket, GetAgentUsageResponse } from '$lib/proto/distroface/v1/agents_pb';
	import { cacheShare, formatTokens } from './agents-state';

	let { scope }: { scope: { kind: string; id: string } } = $props();

	let days = $state(30);
	let usage = $state<GetAgentUsageResponse | undefined>();
	let loading = $state(true);

	async function load() {
		loading = true;
		try {
			usage = await rpcClient.agents.getAgentUsage({ scope, days }, silentCallOptions);
		} catch {
			usage = undefined;
		} finally {
			loading = false;
		}
	}

	onMount(load);

	const totals = $derived(usage?.totals);
	const activeDays = $derived(
		(usage?.days ?? [])
			.filter((d) => d.sessions > 0n)
			.slice(-14)
			.reverse()
	);
</script>

{#snippet bucketTable(title: string, rows: AgentUsageBucket[])}
	{#if rows.length > 0}
		<div class="data-table">
			<Table>
				<TableHeader>
					<TableRow>
						<TableHead>{title}</TableHead>
						<TableHead class="w-24 text-right">Sessions</TableHead>
						<TableHead class="hidden w-28 text-right sm:table-cell">Succeeded</TableHead>
						<TableHead class="w-28 text-right">Billed tokens</TableHead>
						<TableHead class="hidden w-20 text-right md:table-cell">Cached</TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each rows.slice(0, 10) as b (b.key)}
						<TableRow>
							<TableCell class="truncate">{b.label}</TableCell>
							<TableCell class="text-right tabular-nums">{b.sessions}</TableCell>
							<TableCell class="hidden text-right tabular-nums sm:table-cell"
								>{b.succeeded}<span class="text-muted-foreground">
									/ {b.failed} failed</span
								></TableCell
							>
							<TableCell class="text-right tabular-nums"
								>{formatTokens(b.promptTokens + b.outputTokens)}</TableCell
							>
							<TableCell class="hidden text-right text-muted-foreground tabular-nums md:table-cell"
								>{cacheShare(b.cacheReadTokens, b.promptTokens) || '-'}</TableCell
							>
						</TableRow>
					{/each}
				</TableBody>
			</Table>
		</div>
	{/if}
{/snippet}

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0">
			<h2 class="section-title">Usage</h2>
			<p class="section-subtitle">Sessions and tokens spent through this scope.</p>
		</div>
		<Select
			type="single"
			value={String(days)}
			onValueChange={(v) => {
				days = Number(v) || 30;
				void load();
			}}
		>
			<SelectTrigger class="w-36" aria-label="Window">Last {days} days</SelectTrigger>
			<SelectContent>
				{#each [7, 30, 90] as d (d)}
					<SelectItem value={String(d)}>Last {d} days</SelectItem>
				{/each}
			</SelectContent>
		</Select>
	</div>

	{#if loading && !usage}
		<Skeleton class="h-40 w-full rounded-lg" />
	{:else if totals}
		<div class="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
			<StatCard label="Sessions" value={Number(totals.sessions)} icon={Bot} />
			<StatCard
				label="Billed tokens"
				value={formatTokens(totals.promptTokens + totals.outputTokens)}
				icon={Coins}
				hint={cacheShare(totals.cacheReadTokens, totals.promptTokens)
					? `${cacheShare(totals.cacheReadTokens, totals.promptTokens)} served from cache`
					: undefined}
			/>
			<StatCard label="Succeeded" value={Number(totals.succeeded)} icon={CheckCircle2} />
			<StatCard label="Failed" value={Number(totals.failed)} icon={XCircle} />
		</div>
		<div class="grid gap-4 lg:grid-cols-2">
			{@render bucketTable('Agent', usage?.agents ?? [])}
			{@render bucketTable('Repository', usage?.repos ?? [])}
			{@render bucketTable('Connector', usage?.providers ?? [])}
			{@render bucketTable('Model', usage?.models ?? [])}
			{@render bucketTable('Day', activeDays)}
		</div>
		{#if totals.sessions === 0n}
			<p class="text-sm text-muted-foreground">No sessions in this window.</p>
		{/if}
	{:else}
		<p class="text-sm text-muted-foreground">Usage is not available.</p>
	{/if}
</div>
