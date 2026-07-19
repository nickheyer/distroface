<script lang="ts">
	import { getContext } from 'svelte';
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { rpc } from '$lib/rpc';
	import { CertSource, CertState, type GetCertStatusResponse } from '$lib/proto/distroface/v1/certificate_pb';
	import { TLSScope } from '$lib/proto/distroface/v1/certificate_pb';
	import type { RegistryPortal } from '$lib/proto/distroface/v1/portal_pb';
	import { certSourceLabel, certStateLabel, certStateMark, fmtDate } from '$lib/fmt';
	import { effectiveAddress, portalUrl } from '$lib/net';
	import { OrgCtx, ORG_CTX } from '$lib/state/orgctx.svelte';
	import { errata } from '$lib/state/errata.svelte';
	import Leaf from '$lib/bits/Leaf.svelte';
	import Mark from '$lib/bits/Mark.svelte';
	import Confirm from '$lib/bits/Confirm.svelte';
	import MaterialDocket from '$lib/bits/MaterialDocket.svelte';
	import PortalForm, { type PortalFields } from '$lib/bits/PortalForm.svelte';
	import SettingsDesk from '$lib/bits/SettingsDesk.svelte';
	import { SettingsScopeType } from '$lib/proto/distroface/v1/settings_pb';
	import { portalGroups } from '$lib/settings-specs';

	const ctx = getContext<OrgCtx>(ORG_CTX);
	const portalId = $derived(page.params.id!);

	let portal = $state<RegistryPortal | null>(null);
	let status = $state<GetCertStatusResponse | null>(null);
	let busy = $state(false);

	async function load() {
		if (!ctx.org) return;
		const r = await rpc.portal.getPortal({ orgId: ctx.org.id, id: portalId });
		portal = r.portal ?? null;
		await loadStatus();
	}

	async function loadStatus() {
		if (!ctx.org) return;
		try {
			const s = await rpc.certificate.getCertStatus({ orgId: ctx.org.id, portalId });
			status = s;
		} catch {
			status = null;
		}
	}

	$effect(() => {
		void portalId;
		if (ctx.org) load();
	});

	async function save(f: PortalFields) {
		if (!ctx.org) return;
		busy = true;
		try {
			const r = await rpc.portal.updatePortal({
				orgId: ctx.org.id,
				id: portalId,
				name: f.name,
				hostname: f.hostname,
				port: f.port,
				mapUnqualified: f.mapUnqualified,
				setRules: true,
				rules: f.rules,
				allowPush: f.allowPush,
				requireAuth: f.requireAuth,
				tls: f.tls,
				certSource: f.certSource
			});
			portal = r.portal ?? portal;
			errata.remark('Portal saved.');
			await loadStatus();
		} catch {
			// Interceptor reports
		} finally {
			busy = false;
		}
	}

	async function setEnabled(on: boolean) {
		if (!ctx.org) return;
		const r = await rpc.portal.updatePortal({ orgId: ctx.org.id, id: portalId, enabled: on });
		portal = r.portal ?? portal;
		errata.remark(on ? 'Portal enabled.' : 'Portal disabled.');
	}

	async function remove() {
		if (!ctx.org) return;
		await rpc.portal.deletePortal({ orgId: ctx.org.id, id: portalId });
		errata.remark('Portal deleted.');
		goto(`/orgs/${ctx.org.name}/portals`);
	}

	async function issueNow() {
		busy = true;
		try {
			const r = await rpc.certificate.issueCertificate({
				target: { case: 'portalId', value: portalId }
			});
			errata.remark(
				`Certificate issued, valid until ${fmtDate(r.cert?.notAfter)}.`
			);
			await loadStatus();
		} catch {
			// Interceptor reports
		} finally {
			busy = false;
		}
	}

	let certPem = $state('');
	let keyPem = $state('');
	let uploadBusy = $state(false);

	async function uploadCert(e: Event) {
		e.preventDefault();
		if (!ctx.org) return;
		uploadBusy = true;
		try {
			await rpc.certificate.uploadTLSCertificate({
				scope: TLSScope.TLS_SCOPE_PORTAL,
				orgId: ctx.org.id,
				portalId,
				certPem,
				keyPem
			});
			errata.remark('Certificate uploaded for this portal.');
			certPem = '';
			keyPem = '';
			await loadStatus();
		} catch {
			// Interceptor reports
		} finally {
			uploadBusy = false;
		}
	}

	async function dropCert() {
		if (!ctx.org) return;
		await rpc.certificate.deleteTLSCertificate({ scope: TLSScope.TLS_SCOPE_PORTAL, orgId: ctx.org.id, portalId });
		errata.remark('Uploaded certificate removed.');
		await loadStatus();
	}
</script>

{#if portal}
	<Leaf no="01" title={portal.name}>
		{#snippet aside()}
			{#if ctx.isAdmin}
				<button class="rowact plain" onclick={() => setEnabled(!portal?.enabled)}>
					{portal?.enabled ? 'disable portal' : 'enable portal'}
				</button>
			{/if}
		{/snippet}

		<dl class="docket" style="max-width: 44rem">
			<dt>Serves at</dt>
			<dd class="mono">
				<a href={portalUrl(portal.hostname, portal.port, portal.certSource)} rel="external"
					>{effectiveAddress(portal.hostname, portal.port)}</a>
			</dd>
			<dt>Status</dt>
			<dd>
				{#if !portal.enabled}
					<Mark kind="off" label="disabled" />
				{:else}
					<Mark kind={certStateMark[portal.certState]} label={certStateLabel[portal.certState]} />
				{/if}
				{#if portal.certDetail}
					<span class="note" style="margin-left: 0.6rem">{portal.certDetail}</span>
				{/if}
			</dd>
			<dt>Access</dt>
			<dd>
				<span class="caps soft">
					{portal.allowPush ? 'push + pull' : 'pull only'}{portal.requireAuth
						? ' · credentials required'
						: ''}
				</span>
			</dd>
			<dt>Certificate</dt>
			<dd><span class="caps soft">{certSourceLabel[portal.certSource]}</span></dd>
			<dt>Created</dt>
			<dd class="mono">{fmtDate(portal.createdAt)}</dd>
		</dl>
	</Leaf>

	{#if portal.certSource !== CertSource.NONE && portal.certSource !== CertSource.UNSPECIFIED}
		<Leaf no="02" title="Certificate">
			{#if status}
				{#if status.problems.length > 0}
					<div class="panel">
						<p class="panel-title">Problems</p>
						{#each status.problems as problem, i (i)}
							<p class="note wax-ink">† {problem}</p>
						{/each}
					</div>
				{:else if status.state === CertState.READY}
					<p class="note">Connections serve a valid certificate.</p>
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
						{#if status.acmeCert.sans.length}
							<dt>Names</dt>
							<dd class="mono">{status.acmeCert.sans.join(', ')}</dd>
						{/if}
					</dl>
				{/if}
			{/if}

			{#if ctx.isAdmin && (portal.certSource === CertSource.ACME || portal.certSource === CertSource.ORG_CA)}
				<div class="gap-top">
					<button class="act" disabled={busy} onclick={issueNow}>Issue certificate now</button>
				</div>
			{/if}

			{#if ctx.isAdmin && portal.certSource === CertSource.MANUAL}
				<form class="panel" onsubmit={uploadCert}>
					<p class="panel-title">Upload PEM material</p>
					<label class="field" style="max-width: none">
						<span>Certificate chain</span>
						<textarea rows="5" bind:value={certPem} placeholder="-----BEGIN CERTIFICATE-----" required
						></textarea>
					</label>
					<label class="field" style="max-width: none">
						<span>Private key</span>
						<textarea rows="5" bind:value={keyPem} placeholder="-----BEGIN PRIVATE KEY-----" required
						></textarea>
					</label>
					<div class="row">
						<button class="act wax" type="submit" disabled={uploadBusy}>Upload</button>
						{#if status?.servingCert}
							<Confirm label="remove uploaded certificate" onconfirm={dropCert} />
						{/if}
					</div>
				</form>
			{/if}
		</Leaf>
	{/if}

	{#if ctx.isAdmin}
		<Leaf no="03" title="Configuration">
			{#key portal.id + String(portal.updatedAt?.seconds ?? '')}
				<PortalForm {portal} submitLabel="Save portal" {busy} onsave={save} />
			{/key}
		</Leaf>

		<Leaf no="04" title="Settings">
			<p class="note" style="margin-bottom: 0.9rem">
				Portal-level overrides of the organization's settings. Unset fields inherit.
			</p>
			<SettingsDesk scopeType={SettingsScopeType.PORTAL} scopeId={portalId} groups={portalGroups} />
		</Leaf>

		<Leaf no="05" title="Delete portal">
			<p class="note">
				Deleting the portal stops its listener. Nothing in the organization's namespace is
				touched.
			</p>
			<div class="gap-top">
				<Confirm label="delete portal" onconfirm={remove} />
			</div>
		</Leaf>
	{/if}
{:else}
	<p class="working" style="margin-top: 2rem">loading</p>
{/if}
