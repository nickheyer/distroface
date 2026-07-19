<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount, onDestroy } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import FormField from '$lib/components/form-field.svelte';
	import FormCard from '$lib/components/form-card.svelte';
	import { Loader2 } from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
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
			gcStatus = await rpcClient.gc.getGCStatus({}, silentCallOptions);
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
		if (!authStore.hasPermission('settings', 'read')) { goto(resolve('/admin')); return; }
		loadGCStatus().finally(() => (loading = false));
	});

	onDestroy(stopGCPoll);
</script>

{#if loading}
	<div class="space-y-6">
		<Skeleton class="h-52 w-full rounded-xl" />
	</div>
{:else}
	<div class="space-y-6">
		<FormCard
			title="Garbage Collection"
			description={gcStatus?.scheduled
				? `Runs automatically every ${gcStatus.intervalHours}h`
				: 'Automatic runs are off'}
		>
			<div class="space-y-4">
				{#if gcStatus?.running}
					<div class="flex items-center gap-2 text-sm">
						<Loader2 class="h-4 w-4 animate-spin text-primary" />
						<span class="font-medium">Collection in progress</span>
					</div>
				{:else if gcStatus?.lastRun}
					{@const run = gcStatus.lastRun}
					<div class="flex items-center gap-3 text-[13px] text-muted-foreground flex-wrap">
						{#if run.error}
							<Badge variant="outline" class="text-xs border-destructive/40 text-destructive">Failed</Badge>
							<span class="font-mono text-xs">{run.error}</span>
						{:else}
							<Badge variant="outline" class="text-xs">{run.dryRun ? 'Dry run' : 'Completed'}</Badge>
							{#if run.finishedAt}
								<span>{relativeTime(timestampDate(run.finishedAt))}</span>
							{/if}
							{#if !run.dryRun}
								<span class="tabular-nums">{run.blobsDeleted.toLocaleString()} blob{run.blobsDeleted !== 1n ? 's' : ''} removed</span>
								<span class="tabular-nums">{formatBytes(Number(run.bytesFreed))} freed</span>
							{/if}
						{/if}
					</div>
				{:else}
					<p class="text-[13px] text-muted-foreground">No garbage collection has run yet</p>
				{/if}

				<FormField
					label="Remove untagged manifests"
					horizontal
					error={runAct.error}
				>
					<Switch bind:checked={gcRemoveUntagged} disabled={!canEdit || gcStatus?.running} />
				</FormField>
			</div>
			{#snippet footer()}
				{#if canEdit}
					<Button variant="outline" onclick={() => runGC(true)} disabled={runAct.busy || gcStatus?.running}>
						Dry Run
					</Button>
					<Button onclick={() => runGC(false)} disabled={runAct.busy || gcStatus?.running}>
						Run Now
					</Button>
				{/if}
			{/snippet}
		</FormCard>
	</div>
{/if}
