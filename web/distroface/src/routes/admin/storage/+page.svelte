<script lang="ts">
	import { rpc, hush } from '$lib/rpc';
	import type { GetGCStatusResponse, GetStorageUsageResponse } from '$lib/proto/distroface/v1/gc_pb';
	import { fmtBytes, fmtWhen, fmtCount } from '$lib/fmt';
	import { errata } from '$lib/state/errata.svelte';
	import Leaf from '$lib/bits/Leaf.svelte';
	import Mark from '$lib/bits/Mark.svelte';

	let usage = $state<GetStorageUsageResponse | null>(null);
	let gc = $state<GetGCStatusResponse | null>(null);
	let dryRun = $state(true);
	let removeUntagged = $state(false);
	let busy = $state(false);
	let poller: ReturnType<typeof setInterval> | undefined;

	async function load() {
		try {
			usage = await rpc.gc.getStorageUsage({});
		} catch {
			usage = null;
		}
		await pollStatus();
	}

	async function pollStatus() {
		try {
			gc = await rpc.gc.getGCStatus({}, hush);
			if (!gc.running && poller) {
				clearInterval(poller);
				poller = undefined;
				usage = await rpc.gc.getStorageUsage({});
			}
		} catch {
			// Leave the last known state
		}
	}

	$effect(() => {
		load();
		return () => {
			if (poller) clearInterval(poller);
		};
	});

	async function sweep() {
		busy = true;
		try {
			await rpc.gc.runGC({ dryRun, removeUntagged });
			errata.remark(dryRun ? 'Dry run started.' : 'Garbage collection started.');
			if (!poller) poller = setInterval(pollStatus, 2500);
			await pollStatus();
		} catch {
			// Interceptor reports
		} finally {
			busy = false;
		}
	}
</script>

<Leaf no="01" title="Storage usage">
	{#if usage}
		<dl class="docket" style="max-width: 30rem; margin-bottom: 1.2rem">
			<dt>Registry blobs</dt>
			<dd class="mono">{fmtBytes(usage.registryBytes)}</dd>
			<dt>Artifact blobs</dt>
			<dd class="mono">{fmtBytes(usage.artifactBytes)}</dd>
		</dl>
		<p class="note" style="margin-bottom: 1rem">
			Blobs are content-addressed and shared, so the attributions below can sum above the totals.
		</p>

		<div class="row" style="gap: 3rem; align-items: flex-start">
			<div style="flex: 1; min-width: 18rem">
				<p class="caps soft" style="margin-bottom: 0.4rem">Largest registry namespaces</p>
				<table class="ledger">
					<thead>
						<tr>
							<th>Namespace</th>
							<th class="num">Repos</th>
							<th class="num">Held</th>
						</tr>
					</thead>
					<tbody>
						{#each usage.registryNamespaces as e (e.name)}
							<tr>
								<td class="mono">{e.name}</td>
								<td class="num mono">{e.count}</td>
								<td class="num mono">{fmtBytes(e.bytes)}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
			<div style="flex: 1; min-width: 18rem">
				<p class="caps soft" style="margin-bottom: 0.4rem">Largest artifact repositories</p>
				<table class="ledger">
					<thead>
						<tr>
							<th>Repository</th>
							<th class="num">Artifacts</th>
							<th class="num">Held</th>
						</tr>
					</thead>
					<tbody>
						{#each usage.artifactRepos as e (e.name)}
							<tr>
								<td class="mono">{e.name}</td>
								<td class="num mono">{fmtCount(e.count)}</td>
								<td class="num mono">{fmtBytes(e.bytes)}</td>
							</tr>
						{/each}
					</tbody>
				</table>
			</div>
		</div>
	{:else}
		<p class="working">loading</p>
	{/if}
</Leaf>

<Leaf no="02" title="Garbage collection">
	{#if gc}
		<dl class="docket" style="max-width: 40rem; margin-bottom: 1.2rem">
			<dt>Status</dt>
			<dd>
				{#if gc.running}
					<Mark kind="mid" label="running" />
				{:else}
					<Mark kind="ok" label="idle" />
				{/if}
			</dd>
			<dt>Schedule</dt>
			<dd>
				{#if gc.scheduled}
					<span class="mono">every {gc.intervalHours} h</span>
				{:else}
					<span class="faint">manual only</span>
				{/if}
			</dd>
			{#if gc.lastRun}
				<dt>Last run</dt>
				<dd>
					<span class="mono">{fmtWhen(gc.lastRun.startedAt)}</span>
					{#if gc.lastRun.error}
						<span class="mark bad">failed</span>
						<span class="note">{gc.lastRun.error}</span>
					{:else}
						<span class="mono">
							· {fmtCount(gc.lastRun.blobsDeleted)} blobs, {fmtBytes(gc.lastRun.bytesFreed)}
							{gc.lastRun.dryRun ? 'reclaimable (dry run)' : 'reclaimed'}
						</span>
					{/if}
				</dd>
			{/if}
		</dl>
	{/if}

	<label class="tick">
		<input type="checkbox" bind:checked={dryRun} />
		Dry run
		<span class="hint">Report what would be deleted without deleting anything.</span>
	</label>
	<label class="tick">
		<input type="checkbox" bind:checked={removeUntagged} />
		Remove untagged manifests
	</label>
	<div class="gap-top">
		<button class="act wax" disabled={busy || gc?.running} onclick={sweep}>
			{dryRun ? 'Start dry run' : 'Start collection'}
		</button>
	</div>
</Leaf>
