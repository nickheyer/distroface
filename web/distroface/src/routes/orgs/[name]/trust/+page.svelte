<script lang="ts">
	import { getContext } from 'svelte';
	import { rpc } from '$lib/rpc';
	import { Lister } from '$lib/list.svelte';
	import {
		TLSScope,
		type CertificateDomain,
		type GetTLSMaterialResponse
	} from '$lib/proto/distroface/v1/certificate_pb';
	import { fmtDate } from '$lib/fmt';
	import { saveBlob } from '$lib/net';
	import { OrgCtx, ORG_CTX } from '$lib/state/orgctx.svelte';
	import { errata } from '$lib/state/errata.svelte';
	import Leaf from '$lib/bits/Leaf.svelte';
	import Tally from '$lib/bits/Tally.svelte';
	import Mark from '$lib/bits/Mark.svelte';
	import Confirm from '$lib/bits/Confirm.svelte';
	import MaterialDocket from '$lib/bits/MaterialDocket.svelte';

	const ctx = getContext<OrgCtx>(ORG_CTX);

	const domains = new Lister<CertificateDomain>((page) =>
		rpc.certificate
			.listCertificateDomains({ page, orgId: ctx.org?.id ?? '' })
			.then((r) => ({ rows: r.domains, page: r.page }))
	);

	let material = $state<GetTLSMaterialResponse | null>(null);

	async function loadMaterial() {
		if (!ctx.org) return;
		try {
			material = await rpc.certificate.getTLSMaterial({ orgId: ctx.org.id });
		} catch {
			material = null;
		}
	}

	$effect(() => {
		if (ctx.org) {
			domains.first();
			loadMaterial();
		}
	});

	let newDomain = $state('');
	let domainBusy = $state(false);

	async function claimDomain(e: Event) {
		e.preventDefault();
		if (!ctx.org) return;
		domainBusy = true;
		try {
			await rpc.certificate.addCertificateDomain({ domain: newDomain.trim(), orgId: ctx.org.id });
			errata.remark(`${newDomain.trim()} requested.`);
			newDomain = '';
			await domains.fetch();
		} catch {
			// Interceptor reports
		} finally {
			domainBusy = false;
		}
	}

	async function dropDomain(d: CertificateDomain) {
		await rpc.certificate.removeCertificateDomain({ id: d.id });
		errata.remark(`${d.domain} removed.`);
		await domains.fetch();
	}

	async function issueFor(d: CertificateDomain) {
		domainBusy = true;
		try {
			const r = await rpc.certificate.issueCertificate({
				target: { case: 'domainId', value: d.id }
			});
			errata.remark(`Certificate issued for ${d.domain}, valid until ${fmtDate(r.cert?.notAfter)}.`);
			await domains.fetch();
		} catch {
			// Interceptor reports
		} finally {
			domainBusy = false;
		}
	}

	let caName = $state('');
	let caBusy = $state(false);

	async function mintStandalone() {
		if (!ctx.org) return;
		caBusy = true;
		try {
			await rpc.certificate.generateOrgCA({ orgId: ctx.org.id, commonName: caName.trim() });
			errata.remark('Standalone CA generated.');
			caName = '';
			await loadMaterial();
		} catch {
			// Interceptor reports
		} finally {
			caBusy = false;
		}
	}

	async function mintIntermediate() {
		if (!ctx.org) return;
		caBusy = true;
		try {
			await rpc.certificate.issueOrgICA({ orgId: ctx.org.id, commonName: caName.trim() });
			errata.remark('Intermediate CA issued from the instance root.');
			caName = '';
			await loadMaterial();
		} catch {
			// Interceptor reports
		} finally {
			caBusy = false;
		}
	}

	function downloadCA() {
		const pem = material?.orgCa?.certPem;
		if (pem) saveBlob(pem, `${ctx.org?.name ?? 'org'}-ca.pem`);
	}

	async function dropCA() {
		if (!ctx.org) return;
		await rpc.certificate.deleteTLSCertificate({ scope: TLSScope.TLS_SCOPE_ORG_CA, orgId: ctx.org.id });
		errata.remark('Organization CA deleted.');
		await loadMaterial();
	}

	let caCertPem = $state('');
	let caKeyPem = $state('');

	async function uploadCA(e: Event) {
		e.preventDefault();
		if (!ctx.org) return;
		caBusy = true;
		try {
			await rpc.certificate.uploadTLSCertificate({
				scope: TLSScope.TLS_SCOPE_ORG_CA,
				orgId: ctx.org.id,
				certPem: caCertPem,
				keyPem: caKeyPem
			});
			errata.remark('Organization CA uploaded.');
			caCertPem = '';
			caKeyPem = '';
			await loadMaterial();
		} catch {
			// Interceptor reports
		} finally {
			caBusy = false;
		}
	}

	let sharedCertPem = $state('');
	let sharedKeyPem = $state('');
	let sharedBusy = $state(false);

	async function uploadShared(e: Event) {
		e.preventDefault();
		if (!ctx.org) return;
		sharedBusy = true;
		try {
			await rpc.certificate.uploadTLSCertificate({
				scope: TLSScope.TLS_SCOPE_ORG,
				orgId: ctx.org.id,
				certPem: sharedCertPem,
				keyPem: sharedKeyPem
			});
			errata.remark('Shared certificate uploaded.');
			sharedCertPem = '';
			sharedKeyPem = '';
			await loadMaterial();
		} catch {
			// Interceptor reports
		} finally {
			sharedBusy = false;
		}
	}

	async function dropShared() {
		if (!ctx.org) return;
		await rpc.certificate.deleteTLSCertificate({ scope: TLSScope.TLS_SCOPE_ORG, orgId: ctx.org.id });
		errata.remark('Shared certificate removed.');
		await loadMaterial();
	}

	let csrPem = $state('');
	let csrDays = $state('');
	let csrBusy = $state(false);
	let signedPem = $state('');

	async function signCSR(e: Event) {
		e.preventDefault();
		if (!ctx.org) return;
		csrBusy = true;
		signedPem = '';
		try {
			const r = await rpc.certificate.signCSR({
				orgId: ctx.org.id,
				csrPem,
				validityDays: Number(csrDays || '0')
			});
			signedPem = r.certPem;
			errata.remark(`Signed, valid until ${fmtDate(r.cert?.notAfter)}.`);
		} catch {
			// Interceptor reports
		} finally {
			csrBusy = false;
		}
	}
</script>

<Leaf no="01" title="Hostnames">
	<p class="note" style="margin-bottom: 0.9rem">
		Hostnames this organization may hold certificates for. A hostname must match one of the
		organization's portal hostnames, and may need an administrator's approval before issuance.
	</p>

	{#if domains.loaded && domains.rows.length === 0}
		<p class="vacant">No hostnames registered.</p>
	{:else}
		<div class="ledger-scroll">
			<table class="ledger">
				<thead>
					<tr>
						<th>Hostname</th>
						<th>Status</th>
						<th>Certificate</th>
						{#if ctx.isAdmin}
							<th class="end">&nbsp;</th>
						{/if}
					</tr>
				</thead>
				<tbody>
					{#each domains.rows as d (d.id)}
						<tr>
							<td class="mono">{d.domain}</td>
							<td>
								{#if d.approved}
									<Mark kind="ok" label="approved" />
								{:else}
									<Mark kind="mid" label="pending approval" />
								{/if}
							</td>
							<td>
								{#if d.cert?.issued}
									<span class="mono">until {fmtDate(d.cert.notAfter)}</span>
									<span class="mono faint">· {d.cert.issuer}</span>
								{:else}
									<span class="faint">not issued</span>
								{/if}
							</td>
							{#if ctx.isAdmin}
								<td class="end">
									{#if d.approved}
										<button class="rowact plain" disabled={domainBusy} onclick={() => issueFor(d)}
											>issue now</button>
									{/if}
									<Confirm label="remove" onconfirm={() => dropDomain(d)} />
								</td>
							{/if}
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		<Tally lister={domains} unit="hostnames" />
	{/if}

	{#if ctx.isAdmin}
		<form class="row gap-top" onsubmit={claimDomain}>
			<input
				type="text"
				style="width: 18rem"
				placeholder="registry.example.com"
				bind:value={newDomain}
				aria-label="hostname"
			/>
			<button class="act" type="submit" disabled={domainBusy || !newDomain.trim()}
				>Request hostname</button>
		</form>
	{/if}
</Leaf>

<Leaf no="02" title="Organization CA">
	<p class="note" style="margin-bottom: 0.9rem">
		The organization's certificate authority. Portals with certificate source <i>org CA</i> get
		their serving certificates from it automatically, and it signs certificate requests below.
	</p>

	{#if material?.orgCa}
		<MaterialDocket info={material.orgCa} />
		<div class="row gap-top">
			{#if material.orgCa.certPem}
				<button class="act" onclick={downloadCA}>Download ca.pem</button>
			{/if}
			{#if ctx.isAdmin}
				<Confirm label="delete CA" onconfirm={dropCA} />
			{/if}
		</div>
	{:else}
		<p class="vacant">No organization CA exists yet.</p>
	{/if}

	{#if ctx.isAdmin}
		<div class="row foot gap-top">
			<label class="field" style="margin: 0; min-width: 16rem">
				<span>Common name</span>
				<input type="text" bind:value={caName} placeholder={ctx.org?.name} />
			</label>
			<button class="act" disabled={caBusy} onclick={mintStandalone}>Generate standalone CA</button>
			{#if material?.appCaExists}
				<button class="act" disabled={caBusy} onclick={mintIntermediate}
					>Issue intermediate from instance root</button>
			{/if}
		</div>
		<p class="note">
			Generating or issuing replaces the current CA. An intermediate chains to the instance root,
			so clients trusting the root trust every organization under it.
		</p>

		<details class="fold">
			<summary>Upload an existing CA</summary>
			<form class="fold-body" onsubmit={uploadCA}>
				<label class="field" style="max-width: none">
					<span>CA certificate</span>
					<textarea rows="4" bind:value={caCertPem} placeholder="-----BEGIN CERTIFICATE-----" required
					></textarea>
				</label>
				<label class="field" style="max-width: none">
					<span>CA private key</span>
					<textarea rows="4" bind:value={caKeyPem} placeholder="-----BEGIN PRIVATE KEY-----" required
					></textarea>
				</label>
				<button class="act wax" type="submit" disabled={caBusy}>Upload CA</button>
			</form>
		</details>
	{/if}
</Leaf>

<Leaf no="03" title="Shared certificate">
	<p class="note" style="margin-bottom: 0.9rem">
		One uploaded certificate the whole organization may serve. Portals with certificate source
		<i>org certificate</i> present it; useful for a wildcard covering every portal hostname.
	</p>

	{#if material?.orgCert}
		<MaterialDocket info={material.orgCert} />
		{#if ctx.isAdmin}
			<div class="gap-top">
				<Confirm label="remove certificate" onconfirm={dropShared} />
			</div>
		{/if}
	{:else}
		<p class="vacant">No shared certificate uploaded.</p>
	{/if}

	{#if ctx.isAdmin}
		<details class="fold">
			<summary>Upload a certificate</summary>
			<form class="fold-body" onsubmit={uploadShared}>
				<label class="field" style="max-width: none">
					<span>Certificate chain</span>
					<textarea
						rows="4"
						bind:value={sharedCertPem}
						placeholder="-----BEGIN CERTIFICATE-----"
						required
					></textarea>
				</label>
				<label class="field" style="max-width: none">
					<span>Private key</span>
					<textarea rows="4" bind:value={sharedKeyPem} placeholder="-----BEGIN PRIVATE KEY-----" required
					></textarea>
				</label>
				<button class="act wax" type="submit" disabled={sharedBusy}>Upload</button>
			</form>
		</details>
	{/if}
</Leaf>

{#if ctx.isAdmin}
	<Leaf no="04" title="Sign a CSR">
		<p class="note" style="margin-bottom: 0.9rem">
			Paste a PEM certificate signing request to have it signed by the organization's CA. Private
			keys never leave your side; requested names must satisfy the hostname policy.
		</p>
		<form onsubmit={signCSR}>
			<label class="field" style="max-width: none">
				<span>Certificate signing request</span>
				<textarea rows="6" bind:value={csrPem} placeholder="-----BEGIN CERTIFICATE REQUEST-----" required
				></textarea>
			</label>
			<label class="field" style="max-width: 12rem">
				<span>Validity, days</span>
				<input type="text" bind:value={csrDays} placeholder="90" />
			</label>
			<button class="act wax" type="submit" disabled={csrBusy || !material?.orgCa}>Sign</button>
			{#if !material?.orgCa}
				<span class="note" style="margin-left: 0.8rem">Requires an organization CA.</span>
			{/if}
		</form>

		{#if signedPem}
			<div class="panel">
				<p class="panel-title">Signed certificate</p>
				<pre class="tract" style="max-height: 16rem; overflow-y: auto">{signedPem}</pre>
				<div class="gap-top">
					<button class="act" onclick={() => saveBlob(signedPem, 'signed-cert.pem')}
						>Download PEM</button>
				</div>
			</div>
		{/if}
	</Leaf>
{/if}
