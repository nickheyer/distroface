<script lang="ts">
	import { getContext } from 'svelte';
	import { rpc } from '$lib/rpc';
	import { Lister } from '$lib/list.svelte';
	import { CertSource } from '$lib/proto/distroface/v1/certificate_pb';
	import type { RegistryPortal } from '$lib/proto/distroface/v1/portal_pb';
	import { certSourceLabel, certStateLabel, certStateMark, fmtDate } from '$lib/fmt';
	import { effectiveAddress, portalUrl } from '$lib/net';
	import { OrgCtx, ORG_CTX } from '$lib/state/orgctx.svelte';
	import { errata } from '$lib/state/errata.svelte';
	import Leaf from '$lib/bits/Leaf.svelte';
	import Tally from '$lib/bits/Tally.svelte';
	import Mark from '$lib/bits/Mark.svelte';

	const ctx = getContext<OrgCtx>(ORG_CTX);

	const portals = new Lister<RegistryPortal>((page) =>
		rpc.portal
			.listPortals({ page, orgId: ctx.org?.id ?? '' })
			.then((r) => ({ rows: r.portals, page: r.page }))
	);

	$effect(() => {
		if (ctx.org) portals.first();
	});

	let issueBusy = $state(false);

	function canIssue(p: RegistryPortal): boolean {
		return (
			p.enabled &&
			p.hostname !== '' &&
			(p.certSource === CertSource.ACME || p.certSource === CertSource.ORG_CA)
		);
	}

	async function issueFor(p: RegistryPortal) {
		issueBusy = true;
		try {
			const r = await rpc.certificate.issueCertificate({
				target: { case: 'portalId', value: p.id }
			});
			errata.remark(`Certificate issued for ${p.name}, valid until ${fmtDate(r.cert?.notAfter)}.`);
			await portals.fetch();
		} catch {
			// Interceptor reports
		} finally {
			issueBusy = false;
		}
	}
</script>

<Leaf no="01" title="Portals">
	{#snippet aside()}
		{#if ctx.isAdmin}
			<a class="act" href="/orgs/{ctx.org?.name}/portals/new">New portal</a>
		{/if}
	{/snippet}

	<p class="note" style="margin-bottom: 0.9rem">
		A portal is an alternate registry address answering for this organization: its own hostname,
		its own port, or both, with names mapped into the organization's namespace.
	</p>

	{#if portals.loaded && portals.rows.length === 0}
		<p class="vacant">No portals yet.</p>
	{:else}
		<div class="ledger-scroll">
			<table class="ledger">
				<thead>
					<tr>
						<th>Portal</th>
						<th>Serves at</th>
						<th>Access</th>
						<th>Certificate</th>
						<th>Status</th>
						{#if ctx.isAdmin}
							<th class="end">&nbsp;</th>
						{/if}
					</tr>
				</thead>
				<tbody>
					{#each portals.rows as p (p.id)}
						<tr>
							<td><a href="/orgs/{ctx.org?.name}/portals/{p.id}">{p.name}</a></td>
							<td class="mono">
								<a href={portalUrl(p.hostname, p.port, p.certSource)} rel="external">
									{effectiveAddress(p.hostname, p.port)}
								</a>
							</td>
							<td>
								<span class="caps soft">
									{p.allowPush ? 'push + pull' : 'pull only'}{p.requireAuth ? ' · auth' : ''}
								</span>
							</td>
							<td><span class="caps soft">{certSourceLabel[p.certSource]}</span></td>
							<td>
								{#if !p.enabled}
									<Mark kind="off" label="disabled" />
								{:else}
									<Mark
										kind={certStateMark[p.certState]}
										label={certStateLabel[p.certState]}
										title={p.certDetail || undefined}
									/>
								{/if}
							</td>
							{#if ctx.isAdmin}
								<td class="end">
									{#if canIssue(p)}
										<button class="rowact plain" disabled={issueBusy} onclick={() => issueFor(p)}
											>issue certificate</button>
									{/if}
								</td>
							{/if}
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		<Tally lister={portals} unit="portals" />
	{/if}
</Leaf>
