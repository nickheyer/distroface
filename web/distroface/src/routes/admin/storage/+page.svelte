<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount, onDestroy } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import { Loader } from '$lib/icons';
	import { rpcClient, silentCallOptions, backgroundCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Act } from '$lib/act.svelte';
	import { formatBytes, relativeTime } from '$lib/utils';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import type { GetGCStatusResponse } from '$lib/proto/distroface/v1/gc_pb';

	let loading = $state(true);
	let gcStatus = $state<GetGCStatusResponse | null>(null);
	let gcRemoveUntagged = $state(false);
	let gcPollTimer: ReturnType<typeof setInterval> | null = null;

	const runAct = new Act();

	let canEdit = $derived(authStore.canUpdateSettings);

	async function loadGCStatus() {
		try {
			gcStatus = await rpcClient.gc.getGCStatus({}, backgroundCallOptions);
			if (gcStatus.running) {
				if (!gcPollTimer) gcPollTimer = setInterval(loadGCStatus, 2000);
			} else {
				stopGCPoll();
			}
		} catch {
			// Next poll or action refreshes
		}
	}

	function stopGCPoll() {
		if (gcPollTimer) {
			clearInterval(gcPollTimer);
			gcPollTimer = null;
		}
	}

	async function runGC(dryRun: boolean) {
		await runAct.run(() =>
			rpcClient.gc.runGC({ dryRun, removeUntagged: gcRemoveUntagged }, silentCallOptions)
		);
		await loadGCStatus();
	}

	onMount(() => {
		if (!authStore.hasPermission('settings', 'read')) {
			goto(resolve('/admin'));
			return;
		}
		loadGCStatus().finally(() => (loading = false));
	});

	onDestroy(stopGCPoll);
</script>

{#if loading}
	<Skeleton class="h-52 w-full rounded-lg" />
{:else}
	<FormPage
		level="section"
		title="Storage"
		description="Housekeeping for the blob store behind images and artifacts."
	>
		<FormSection
			title="Garbage collection"
			description={gcStatus?.scheduled
				? `Runs automatically every ${gcStatus.intervalHours}h.`
				: 'Automatic runs are off.'}
		>
			{#if gcStatus?.running}
				<div class="flex items-center gap-2 text-sm">
					<Loader class="size-4 animate-spin text-primary" />
					<span class="font-medium">Collection in progress</span>
					{#if gcStatus.runningOn}
						<span class="font-mono text-xs text-muted-foreground">on {gcStatus.runningOn}</span>
					{/if}
				</div>
			{:else if gcStatus?.lastRun}
				{@const run = gcStatus.lastRun}
				<div class="flex flex-wrap items-center gap-3 text-sm text-muted-foreground">
					{#if run.error}
						<Badge variant="destructive">Failed</Badge>
						<span class="font-mono text-xs">{run.error}</span>
					{:else}
						<Badge variant={run.dryRun ? 'outline' : 'success'}
							>{run.dryRun ? 'Dry run' : 'Completed'}</Badge
						>
						{#if run.finishedAt}
							<span>{relativeTime(timestampDate(run.finishedAt))}</span>
						{/if}
						<span class="tabular-nums"
							>{run.blobsDeleted.toLocaleString()} blob{run.blobsDeleted !== 1n ? 's' : ''}
							{run.dryRun ? 'eligible' : 'removed'}</span
						>
						<span class="tabular-nums"
							>{formatBytes(Number(run.bytesFreed))} {run.dryRun ? 'reclaimable' : 'freed'}</span
						>
					{/if}
					{#if run.ranOn}
						<span class="font-mono text-xs">on {run.ranOn}</span>
					{/if}
				</div>
			{:else}
				<p class="text-sm text-muted-foreground">No garbage collection has run yet</p>
			{/if}

			<FormField label="Remove untagged manifests" layout="toggle" error={runAct.error}>
				<Switch bind:checked={gcRemoveUntagged} disabled={!canEdit || gcStatus?.running} />
			</FormField>
			{#snippet footer()}
				{#if canEdit}
					<Button onclick={() => runGC(false)} disabled={runAct.busy || gcStatus?.running}>
						Run now
					</Button>
					<Button
						variant="outline"
						onclick={() => runGC(true)}
						disabled={runAct.busy || gcStatus?.running}
					>
						Dry run
					</Button>
				{/if}
			{/snippet}
		</FormSection>

		<FormSection
			title="Shared cache"
			description="Sessions, rate limits, blob descriptors and the collection lease live here when redis is configured."
		>
			<div class="flex flex-wrap items-center gap-3 text-sm text-muted-foreground">
				{#if gcStatus?.cache && gcStatus.cache !== 'memory'}
					{#if gcStatus.cacheError}
						<Badge variant="destructive">Unreachable</Badge>
						<span class="font-mono text-xs">{gcStatus.cache}</span>
						<span class="font-mono text-xs">{gcStatus.cacheError}</span>
					{:else}
						<Badge variant="success">Connected</Badge>
						<span class="font-mono text-xs">{gcStatus.cache}</span>
					{/if}
				{:else}
					<Badge variant="outline">Process memory</Badge>
					<span>Set redis.url to share caches across instances.</span>
				{/if}
			</div>
		</FormSection>
	</FormPage>
{/if}
