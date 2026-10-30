<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import { Input } from '$lib/components/ui/input';
	import { Loader } from '$lib/icons';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import CertMaterialRow from '$lib/components/cert-material-row.svelte';
	import CertUploadPanel from '$lib/components/cert-upload-panel.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Act, errText } from '$lib/act.svelte';
	import { downloadBlob } from '$lib/download';
	import CopyButton from '$lib/components/copy-button.svelte';
	import {
		acmeDirectoryURL,
		isLocked,
		patchSettings,
		systemScope,
		type SettingsPatch
	} from '$lib/settings-utils';
	import type { FieldProvenance, Settings } from '$lib/proto/distroface/v1/settings_pb';
	import {
		CertificateDomainScope,
		TLSScope,
		type CertificateDomain,
		type GetTLSMaterialResponse
	} from '$lib/proto/distroface/v1/certificate_pb';

	let eff = $state<Settings | null>(null);
	let prov = $state<FieldProvenance[]>([]);
	let material = $state<GetTLSMaterialResponse | null>(null);
	let loading = $state(true);
	let loadError = $state('');

	let caAcme = $state(false);
	let domains = $state<CertificateDomain[]>([]);
	let newDomain = $state('');
	let domainBusy = $state<string | null>(null);

	const caAcmeAct = new Act();
	const appCaAct = new Act();
	const domainAct = new Act();

	let uploadOpen = $state(false);

	const primaryHostname = $derived(eff?.server?.publicHostname ?? '');
	const directoryURL = $derived(primaryHostname ? acmeDirectoryURL(primaryHostname) : '');
	const plainDirectoryURL = $derived(
		primaryHostname ? `http://${primaryHostname}/acme/directory` : ''
	);
	const caEndpoint = $derived(
		primaryHostname
			? `https://${primaryHostname}/.well-known/distroface/ca.pem`
			: '/.well-known/distroface/ca.pem'
	);
	const dfcliHint = $derived(
		primaryHostname
			? `dfcli trust install --server https://${primaryHostname}`
			: 'dfcli trust install'
	);

	// Pinned fields render disabled with a lock hint
	const locked = (path: string) => isLocked(prov, path);
	const lockHint = (path: string, help: string) =>
		locked(path) ? 'Pinned by the config file' : help;

	function seedForms(s: Settings) {
		caAcme = s.ca?.acmeEnabled ?? false;
	}

	// Instance scope registrations, the hostnames external clients may enroll
	async function loadDomains() {
		try {
			const resp = await rpcClient.certificate.listCertificateDomains(
				{ scope: CertificateDomainScope.SYSTEM, page: { pageSize: 200 } },
				silentCallOptions
			);
			domains = resp.domains;
		} catch {
			// List refreshes on the next action
		}
	}

	async function addDomain() {
		const domain = newDomain.trim().toLowerCase();
		if (!domain) return;
		const ok = await domainAct.run(() =>
			rpcClient.certificate.addCertificateDomain({ domain }, silentCallOptions)
		);
		if (ok) newDomain = '';
		await loadDomains();
	}

	async function removeDomain(domain: CertificateDomain) {
		domainBusy = domain.id;
		await domainAct.run(() =>
			rpcClient.certificate.removeCertificateDomain({ id: domain.id }, silentCallOptions)
		);
		domainBusy = null;
		await loadDomains();
	}

	async function load() {
		loading = true;
		loadError = '';
		try {
			const resp = await rpcClient.settings.getEffectiveSettings(
				{ scope: systemScope },
				silentCallOptions
			);
			eff = resp.settings ?? null;
			prov = resp.provenance;
			if (eff) seedForms(eff);
			material = await rpcClient.certificate.getTLSMaterial({}, silentCallOptions);
			await loadDomains();
		} catch (err) {
			loadError = errText(err);
		} finally {
			loading = false;
		}
	}

	// Settings apply on interaction, a failed patch reverts the control
	async function apply(act: Act, settings: SettingsPatch, paths: string[]) {
		const ok = await act.run(async () => {
			const res = await patchSettings(systemScope, settings, paths);
			if (res.effective) {
				eff = res.effective;
				prov = res.provenance;
				seedForms(res.effective);
			}
		});
		if (!ok && eff) seedForms(eff);
	}

	function setCaAcme(v: boolean) {
		caAcme = v;
		apply(caAcmeAct, { ca: { acmeEnabled: v } }, ['ca.acme_enabled']);
	}

	// Fetches the public trust anchor endpoint and saves it
	async function downloadTrustAnchor() {
		try {
			const resp = await fetch('/.well-known/distroface/ca.pem');
			if (!resp.ok) return;
			downloadBlob(await resp.text(), 'distroface-ca.pem');
		} catch {
			// Material download stays available as a fallback
		}
	}

	async function submitUpload(certPem: string, keyPem: string) {
		await rpcClient.certificate.uploadTLSCertificate(
			{ scope: TLSScope.TLS_SCOPE_APP_CA, certPem, keyPem },
			silentCallOptions
		);
		await load();
	}

	function generateAppCA() {
		appCaAct.run(async () => {
			await rpcClient.certificate.generateAppCA({}, silentCallOptions);
			await load();
		});
	}

	function downloadAppCA() {
		if (material?.appCa?.certPem) downloadBlob(material.appCa.certPem, 'distroface-root-ca.pem');
	}

	function removeAppCA() {
		appCaAct.run(async () => {
			await rpcClient.certificate.deleteTLSCertificate(
				{ scope: TLSScope.TLS_SCOPE_APP_CA },
				silentCallOptions
			);
			await load();
		});
	}

	onMount(() => {
		if (!authStore.canManageSettings) {
			goto(resolve('/admin'));
			return;
		}
		load();
	});
</script>

{#if loading}
	<div class="space-y-6">
		<Skeleton class="h-28 w-full rounded-lg" />
		<Skeleton class="h-32 w-full rounded-lg" />
	</div>
{:else if loadError}
	<div class="flex flex-col items-center rounded-lg border bg-card px-6 py-10 text-center">
		<p class="text-sm text-destructive">{loadError}</p>
		<Button variant="outline" size="sm" class="mt-3" onclick={load}>Retry</Button>
	</div>
{:else if eff}
	<FormPage
		level="section"
		title="Certificate authority"
		description="The instance root that signs server certificates and organization CAs."
	>
		<FormSection
			title="Instance CA"
			description="The primary hostname's own certificate is configured under Network security."
		>
			<CertMaterialRow
				title="Root certificate"
				empty="Generate or upload a root"
				material={material?.appCa}
				busy={appCaAct.busy}
				error={appCaAct.error}
				onGenerate={generateAppCA}
				onUpload={() => (uploadOpen = true)}
				onDownload={material?.appCa ? downloadAppCA : undefined}
				onRemove={removeAppCA}
			/>
			<CertUploadPanel bind:open={uploadOpen} title="Upload instance CA" onSubmit={submitUpload} />
			{#if material?.appCa}
				<FormField label="Trust anchor" help="Clients fetch this to trust self-issued TLS">
					<div class="flex items-center gap-2">
						<code class="code-inline min-w-0 flex-1 truncate">{caEndpoint}</code>
						<CopyButton text={caEndpoint} />
						<Button variant="outline" size="sm" class="shrink-0" onclick={downloadTrustAnchor}
							>Download</Button
						>
					</div>
					<div class="mt-2 flex items-center gap-2">
						<code class="code-inline min-w-0 flex-1 truncate">{dfcliHint}</code>
						<CopyButton text={dfcliHint} />
					</div>
					<p class="text-sm text-muted-foreground">
						Run <code class="font-mono">dfcli trust install</code> on a client so docker pull trusts the
						registry.
					</p>
				</FormField>
			{/if}
		</FormSection>

		<FormSection
			title="ACME server"
			description="Let external machines request certificates from this CA."
		>
			<FormField
				label="Built-in ACME server"
				layout="toggle"
				help={lockHint('ca.acme_enabled', 'ACME directory backed by the instance CA')}
				tag={caAcmeAct.tag}
				error={caAcmeAct.error}
			>
				<Switch
					checked={caAcme}
					disabled={caAcmeAct.busy || locked('ca.acme_enabled')}
					onCheckedChange={setCaAcme}
				/>
			</FormField>
			{#if !caAcme && !material?.appCa}
				<p class="text-sm text-muted-foreground">Generate the instance CA above first</p>
			{/if}
			{#if caAcme}
				{#if !material?.appCa}
					<p class="text-sm text-warning">
						Generate the instance CA above so issued certificates chain to a root
					</p>
				{/if}
				<FormField label="Directory URL" help="Point certbot, caddy, lego, or step here">
					<div class="flex items-center gap-2">
						<code class="code-inline min-w-0 flex-1 truncate">{directoryURL}</code>
						{#if directoryURL}<CopyButton text={directoryURL} />{/if}
					</div>
					<p class="text-sm text-muted-foreground">
						Clients trust the directory once they hold the trust anchor above, until the primary
						hostname serves TLS the same directory answers at
						<code class="font-mono">{plainDirectoryURL}</code>. Issued certificates chain to the
						instance root CA. This instance and its portals cannot enroll against themselves,
						portals wanting instance-issued certificates use the Org CA source instead.
					</p>
				</FormField>
				<FormField
					label="Allowed hostnames"
					id="acme-domain"
					help="Clients may only enroll these, the primary hostname, and ACME portal hostnames. Validation reaches the client on port 80 or 443."
					tag={domainAct.tag}
					error={domainAct.error}
				>
					{#if domains.length > 0}
						<div class="box divide-y">
							{#each domains as domain (domain.id)}
								<div class="flex items-center justify-between gap-3 px-4 py-2">
									<span class="font-mono text-sm">{domain.domain}</span>
									<Button
										variant="ghost"
										size="sm"
										class="text-destructive hover:text-destructive"
										disabled={domainBusy !== null}
										onclick={() => removeDomain(domain)}
									>
										{#if domainBusy === domain.id}
											<Loader class="animate-spin" />
										{:else}
											Remove
										{/if}
									</Button>
								</div>
							{/each}
						</div>
					{/if}
					<div class="flex items-center gap-2">
						<Input
							id="acme-domain"
							bind:value={newDomain}
							class="font-mono"
							placeholder="runner1.internal"
							disabled={domainAct.busy}
							onkeydown={(e) => {
								if (e.key === 'Enter') addDomain();
							}}
						/>
						<Button
							variant="outline"
							size="sm"
							class="shrink-0"
							disabled={domainAct.busy || !newDomain.trim()}
							onclick={addDomain}
						>
							Add
						</Button>
					</div>
				</FormField>
			{/if}
		</FormSection>
	</FormPage>
{/if}
