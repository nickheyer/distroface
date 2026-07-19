<script lang="ts">
	import { rpc } from '$lib/rpc';
	import { Lister } from '$lib/list.svelte';
	import type { AuditEvent } from '$lib/proto/distroface/v1/audit_pb';
	import { fmtWhen } from '$lib/fmt';
	import Leaf from '$lib/bits/Leaf.svelte';
	import Find from '$lib/bits/Find.svelte';
	import Tally from '$lib/bits/Tally.svelte';
	import Mark from '$lib/bits/Mark.svelte';

	const events = new Lister<AuditEvent>(
		(page) => rpc.audit.listAuditEvents({ page }).then((r) => ({ rows: r.events, page: r.page })),
		{ pageSize: 100 }
	);

	$effect(() => {
		events.first();
	});

	let open = $state('');

	function outcomeMark(outcome: string): 'ok' | 'bad' | 'mid' {
		const o = outcome.toLowerCase();
		if (o === 'success' || o === 'ok' || o === 'allowed') return 'ok';
		if (o === 'denied' || o === 'failure' || o === 'failed' || o === 'error') return 'bad';
		return 'mid';
	}
</script>

<Leaf no="01" title="Events">
	{#snippet aside()}
		<Find lister={events} placeholder="actor, action, address…" />
	{/snippet}

	{#if events.loaded && events.rows.length === 0}
		<p class="vacant">No events recorded.</p>
	{:else}
		<div class="ledger-scroll">
			<table class="ledger">
				<thead>
					<tr>
						<th>When</th>
						<th>Actor</th>
						<th>Action</th>
						<th>Resource</th>
						<th>Outcome</th>
						<th>From</th>
					</tr>
				</thead>
				<tbody>
					{#each events.rows as e (e.id)}
						<tr
							onclick={() => (open = open === e.id ? '' : e.id)}
							style={e.detail ? 'cursor: pointer' : ''}
						>
							<td class="mono">{fmtWhen(e.createdAt)}</td>
							<td>{e.actor || '—'}</td>
							<td class="mono">{e.action}</td>
							<td class="mono" style="overflow-wrap: anywhere">{e.resource || '—'}</td>
							<td><Mark kind={outcomeMark(e.outcome)} label={e.outcome || 'unknown'} /></td>
							<td class="mono">{e.sourceIp || '—'}</td>
						</tr>
						{#if open === e.id && e.detail}
							<tr>
								<td colspan="6" style="padding-top: 0">
									<pre class="tract" style="margin-bottom: 0.4rem">{e.detail}</pre>
								</td>
							</tr>
						{/if}
					{/each}
				</tbody>
			</table>
		</div>
		<Tally lister={events} unit="events" />
	{/if}
</Leaf>
