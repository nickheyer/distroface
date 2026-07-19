<script lang="ts">
	import { rpc } from '$lib/rpc';
	import { Lister } from '$lib/list.svelte';
	import {
		CertificateDomainScope,
		CertSource,
		CertState,
		TLSScope,
		type CertificateDomain,
		type GetCertStatusResponse,
		type GetTLSMaterialResponse
	} from '$lib/proto/distroface/v1/certificate_pb';
	import { certSourceLabel, certStateLabel, certStateMark, fmtDate } from '$lib/fmt';
	import { saveBlob } from '$lib/net';
	import { site } from '$lib/state/site.svelte';
	import { errata } from '$lib/state/errata.svelte';
	import Leaf from '$lib/bits/Leaf.svelte';
	import Tally from '$lib/bits/Tally.svelte';
	import Mark from '$lib/bits/Mark.svelte';
	import Confirm from '$lib/bits/Confirm.svelte';
	import Copy from '$lib/bits/Copy.svelte';
	import MaterialDocket from '$lib/bits/MaterialDocket.svelte';

	const queue = new Lister<CertificateDomain>((page) =>
		rpc.certificate
			.listCertificateDomains({ page, pendingOnly: true })
			.then((r) => ({ rows: r.domains, page: r.page }))
	);

	let scopeFilter = $state<CertificateDomainScope>(CertificateDomainScope.UNSPECIFIED);

	const domains = new Lister<CertificateDomain>((page) =>
		rpc.certificate
			.listCertificateDomains({ page, scope: scopeFilter })
			.then((r) => ({ rows: r.domains, page: r.page }))
	);

	let material = $state<GetTLSMaterialResponse | null>(null);
	let status = $state<GetCertStatusResponse | null>(null);

	async function loadTrust() {
		try {
			material = await rpc.certificate.getTLSMaterial({});
		} catch {
			material = null;
		}
		try {
			status = await rpc.certificate.getCertStatus({});
		} catch {
			status = null;
		}
	}

	$effect(() => {
		queue.first();
		loadTrust();
	});

	$effect(() => {
		void scopeFilter;
		domains.first();
	});

	async function approve(d: CertificateDomain) {
		await rpc.certificate.approveCertificateDomain({ id: d.id });
		errata.remark(`${d.domain} approved for issuance.`);
		await Promise.all([queue.first(), domains.first()]);
	}

	async function dropDomain(d: CertificateDomain) {
		await rpc.certificate.removeCertificateDomain({ id: d.id });
		errata.remark(`${d.domain} removed.`);
		await Promise.all([queue.first(), domains.first()]);
	}

	let newDomain = $state('');
	let domainBusy = $state(false);

	async function addSystemDomain(e: Event) {
		e.preventDefault();
		domainBusy = true;
		try {
			await rpc.certificate.addCertificateDomain({ domain: newDomain.trim() });
			errata.remark(`${newDomain.trim()} registered.`);
			newDomain = '';
			await domains.first();
		} catch {
			// Interceptor reports
		} finally {
			domainBusy = false;
		}
	}

	async function issueFor(d: CertificateDomain) {
		domainBusy = true;
		try {
			const r = await rpc.certificate.issueCertificate({ target: { case: 'domainId', value: d.id } });
			errata.remark(`Certificate issued for ${d.domain}, valid until ${fmtDate(r.cert?.notAfter)}.`);
			await domains.fetch();
		} catch {
			// Interceptor reports
		} finally {
			domainBusy = false;
		}
	}

	let rootName = $state('');
	let rootBusy = $state(false);

	async function mintRoot() {
		rootBusy = true;
		try {
			await rpc.certificate.generateAppCA({ commonName: rootName.trim() });
			errata.remark('Instance root CA generated.');
			rootName = '';
			await loadTrust();
		} catch {
			// Interceptor reports
		} finally {
			rootBusy = false;
		}
	}

	function downloadRoot() {
		const pem = material?.appCa?.certPem;
		if (pem) saveBlob(pem, 'instance-root.pem');
	}

	async function dropScoped(scope: TLSScope, done: string) {
		await rpc.certificate.deleteTLSCertificate({ scope });
		errata.remark(done);
		await loadTrust();
	}

	let upScope = $state<TLSScope>(TLSScope.TLS_SCOPE_APP);
	let upCert = $state('');
	let upKey = $state('');
	let upBusy = $state(false);

	async function uploadMaterial(e: Event) {
		e.preventDefault();
		upBusy = true;
		try {
			await rpc.certificate.uploadTLSCertificate({ scope: upScope, certPem: upCert, keyPem: upKey });
			errata.remark('Certificate uploaded.');
			upCert = '';
			upKey = '';
			await loadTrust();
		} catch {
			// Interceptor reports
		} finally {
			upBusy = false;
		}
	}

	let issueBusy = $state(false);

	// Only acme and the instance ca issue on demand
	const canIssuePrimary = $derived(
		status?.source === CertSource.ACME || status?.source === CertSource.APP_CA
	);

	async function issuePrimary() {
		issueBusy = true;
		try {
			const r = await rpc.certificate.issueCertificate({});
			errata.remark(
				`Certificate issued for the primary hostname, valid until ${fmtDate(r.cert?.notAfter)}.`
			);
			await loadTrust();
		} catch {
			// Interceptor reports
		} finally {
			issueBusy = false;
		}
	}

	let csrPem = $state('');
	let csrDays = $state('');
	let csrBusy = $state(false);
	let signedPem = $state('');

	async function signCSR(e: Event) {
		e.preventDefault();
		csrBusy = true;
		signedPem = '';
		try {
			const r = await rpc.certificate.signCSR({ csrPem, validityDays: Number(csrDays || '0') });
			signedPem = r.certPem;
			errata.remark(`Signed, valid until ${fmtDate(r.cert?.notAfter)}.`);
		} catch {
			// Interceptor reports
		} finally {
			csrBusy = false;
		}
	}

	const trustBundleURL = $derived(`https://${site.publicHostname}/.well-known/distroface/ca.pem`);
	const dfcliTrust = $derived(`dfcli trust install --server https://${site.publicHostname}`);
	const acmeDirectoryURL = $derived(`https://${site.publicHostname}/acme/directory`);
</script>

<Leaf no="01" title="Pending approval">
	{#if queue.loaded && queue.rows.length === 0}
		<p class="vacant">No hostname requests are waiting for approval.</p>
	{:else}
		<div class="ledger-scroll">
			<table class="ledger">
				<thead>
					<tr>
						<th>Hostname</th>
						<th>Organization</th>
						<th>Requested by</th>
						<th>Date</th>
						<th class="end">&nbsp;</th>
					</tr>
				</thead>
				<tbody>
					{#each queue.rows as d (d.id)}
						<tr>
							<td class="mono">{d.domain}</td>
							<td>{d.orgName || '—'}</td>
							<td>{d.createdBy}</td>
							<td class="mono">{fmtDate(d.createdAt)}</td>
							<td class="end">
								<button class="rowact" onclick={() => approve(d)}>approve</button>
								<Confirm label="reject" onconfirm={() => dropDomain(d)} />
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		<Tally lister={queue} unit="requests" />
	{/if}
</Leaf>

<Leaf no="02" title="Registered hostnames">
	{#snippet aside()}
		<select bind:value={scopeFilter} style="width: auto" aria-label="scope">
			<option value={CertificateDomainScope.UNSPECIFIED}>all scopes</option>
			<option value={CertificateDomainScope.SYSTEM}>instance</option>
			<option value={CertificateDomainScope.ORG}>organizations</option>
		</select>
	{/snippet}

	{#if domains.loaded && domains.rows.length === 0}
		<p class="vacant">No hostnames registered.</p>
	{:else}
		<div class="ledger-scroll">
			<table class="ledger">
				<thead>
					<tr>
						<th>Hostname</th>
						<th>Owner</th>
						<th>Status</th>
						<th>Certificate</th>
						<th class="end">&nbsp;</th>
					</tr>
				</thead>
				<tbody>
					{#each domains.rows as d (d.id)}
						<tr>
							<td class="mono">{d.domain}</td>
							<td>{d.scope === CertificateDomainScope.SYSTEM ? 'instance' : d.orgName}</td>
							<td>
								{#if d.approved}
									<Mark kind="ok" label="approved" />
								{:else}
									<Mark kind="mid" label="pending" />
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
							<td class="end">
								{#if d.approved}
									<button class="rowact plain" disabled={domainBusy} onclick={() => issueFor(d)}
										>issue now</button>
								{:else}
									<button class="rowact" onclick={() => approve(d)}>approve</button>
								{/if}
								<Confirm label="remove" onconfirm={() => dropDomain(d)} />
							</td>
						</tr>
					{/each}
				</tbody>
			</table>
		</div>
		<Tally lister={domains} unit="hostnames" />
	{/if}

	<form class="row gap-top" onsubmit={addSystemDomain}>
		<input
			type="text"
			style="width: 18rem"
			placeholder="registry.example.com"
			bind:value={newDomain}
			aria-label="hostname"
		/>
		<button class="act" type="submit" disabled={domainBusy || !newDomain.trim()}>
			Register hostname
		</button>
	</form>
</Leaf>

<Leaf no="03" title="Instance root CA">
	<p class="note" style="margin-bottom: 0.9rem">
		The root of trust for this instance. It signs organization intermediate CAs and, when the
		primary certificate source is set to internal CA, the primary serving certificate. Distribute
		it to clients that should trust certificates issued here.
	</p>

	{#if material?.appCa}
		<MaterialDocket info={material.appCa} />
		<div class="row gap-top">
			{#if material.appCa.certPem}
				<button class="act" onclick={downloadRoot}>Download root.pem</button>
			{/if}
			<Confirm
				label="delete root CA"
				onconfirm={() => dropScoped(TLSScope.TLS_SCOPE_APP_CA, 'Instance root CA deleted.')}
			/>
		</div>
		<p class="note">
			Deleting or replacing the root CA orphans every intermediate and leaf certificate issued
			under it.
		</p>
	{:else}
		<p class="vacant">No instance root CA exists yet.</p>
	{/if}

	<div class="row foot gap-top">
		<label class="field" style="margin: 0; min-width: 16rem">
			<span>Common name</span>
			<input type="text" bind:value={rootName} placeholder={site.publicHostname} />
		</label>
		<button class="act" disabled={rootBusy} onclick={mintRoot}>Generate root CA</button>
	</div>
</Leaf>

{#if material?.appCa}
	<Leaf no="04" title="Trust distribution">
		<p class="note" style="margin-bottom: 0.9rem">
			Clients can fetch the trust bundle from the well-known URL, or install it with the CLI.
			Downstream instances and portals can also obtain certificates from the built-in ACME
			directory when it is enabled in <a href="/admin/settings">settings</a>.
		</p>
		<div class="stack">
			<div class="cmdline" style="white-space: normal; overflow-wrap: anywhere">
				{trustBundleURL}
				<Copy text={trustBundleURL} />
			</div>
			<div class="cmdline">
				{dfcliTrust}
				<Copy text={dfcliTrust} />
			</div>
			<div class="cmdline" style="white-space: normal; overflow-wrap: anywhere">
				{acmeDirectoryURL}
				<Copy text={acmeDirectoryURL} />
			</div>
		</div>
	</Leaf>
{/if}

<Leaf no={material?.appCa ? '05' : '04'} title="Primary certificate">
	{#if status}
		<dl class="docket" style="max-width: 40rem; margin-bottom: 1rem">
			<dt>Source</dt>
			<dd>
				<span class="caps soft">{certSourceLabel[status.source]}</span>
				<a href="/admin/settings" class="rowact" style="margin-left: 0.7rem">change in settings</a>
			</dd>
			<dt>State</dt>
			<dd>
				<Mark kind={certStateMark[status.state]} label={certStateLabel[status.state]} />
			</dd>
		</dl>

		{#if status.problems.length > 0}
			<div class="panel">
				<p class="panel-title">Problems</p>
				{#each status.problems as problem, i (i)}
					<p class="note wax-ink">† {problem}</p>
				{/each}
			</div>
		{:else if status.state === CertState.READY}
			<p class="note">Connections to the primary hostname serve a valid certificate.</p>
		{/if}

		{#if status.servingCert}
			<MaterialDocket info={status.servingCert} />
		{/if}
		{#if status.acmeCert?.issued}
			<dl class="docket" style="max-width: 40rem">
				<dt>Issuer</dt>
				<dd class="mono">{status.acmeCert.issuer}</dd>
				<dt>Valid</dt>
				<dd class="mono">{fmtDate(status.acmeCert.notBefore)} to {fmtDate(status.acmeCert.notAfter)}</dd>
			</dl>
		{/if}
	{/if}

	<div class="row gap-top">
		{#if canIssuePrimary}
			<button class="act" disabled={issueBusy} onclick={issuePrimary}>Issue certificate now</button>
		{/if}
		{#if material?.appCert}
			<Confirm
				label="remove uploaded certificate"
				onconfirm={() => dropScoped(TLSScope.TLS_SCOPE_APP, 'Uploaded primary certificate removed.')}
			/>
		{/if}
	</div>

	<details class="fold">
		<summary>Upload PEM material</summary>
		<form class="fold-body" onsubmit={uploadMaterial}>
			<label class="field" style="max-width: 22rem">
				<span>Destination</span>
				<select bind:value={upScope}>
					<option value={TLSScope.TLS_SCOPE_APP}>primary serving certificate</option>
					<option value={TLSScope.TLS_SCOPE_APP_CA}>instance root CA</option>
					<option value={TLSScope.TLS_SCOPE_ACME_CA}>built-in ACME intermediate CA</option>
				</select>
			</label>
			<label class="field" style="max-width: none">
				<span>Certificate chain</span>
				<textarea rows="5" bind:value={upCert} placeholder="-----BEGIN CERTIFICATE-----" required
				></textarea>
			</label>
			<label class="field" style="max-width: none">
				<span>Private key</span>
				<textarea rows="5" bind:value={upKey} placeholder="-----BEGIN PRIVATE KEY-----" required
				></textarea>
			</label>
			<button class="act wax" type="submit" disabled={upBusy}>Upload</button>
		</form>
	</details>
</Leaf>

<Leaf no={material?.appCa ? '06' : '05'} title="Sign a CSR">
	<p class="note" style="margin-bottom: 0.9rem">
		Paste a PEM certificate signing request to have it signed by the instance root CA. Requested
		names must satisfy the hostname policy.
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
		<button class="act wax" type="submit" disabled={csrBusy || !material?.appCa}>Sign</button>
		{#if !material?.appCa}
			<span class="note" style="margin-left: 0.8rem">Requires an instance root CA.</span>
		{/if}
	</form>

	{#if signedPem}
		<div class="panel">
			<p class="panel-title">Signed certificate</p>
			<pre class="tract" style="max-height: 16rem; overflow-y: auto">{signedPem}</pre>
			<div class="gap-top">
				<button class="act" onclick={() => saveBlob(signedPem, 'signed-cert.pem')}>Download PEM</button>
			</div>
		</div>
	{/if}
</Leaf>
