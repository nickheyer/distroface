<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import FormCard from '$lib/components/form-card.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import CertMaterialRow from '$lib/components/cert-material-row.svelte';
	import CertUploadPanel from '$lib/components/cert-upload-panel.svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Act, errText } from '$lib/act.svelte';
	import { downloadBlob } from '$lib/download';
	import CopyButton from '$lib/components/copy-button.svelte';
	import { acmeDirectoryURL, isLocked, patchSettings, systemScope, type SettingsPatch } from '$lib/settings-utils';
	import type { FieldProvenance, Settings } from '$lib/proto/distroface/v1/settings_pb';
	import { TLSScope, type GetTLSMaterialResponse } from '$lib/proto/distroface/v1/certificate_pb';

	let eff = $state<Settings | null>(null);
	let prov = $state<FieldProvenance[]>([]);
	let material = $state<GetTLSMaterialResponse | null>(null);
	let loading = $state(true);
	let loadError = $state('');

	let caAcme = $state(false);

	const caAcmeAct = new Act();
	const appCaAct = new Act();

	let uploadOpen = $state(false);

	const primaryHostname = $derived(eff?.server?.publicHostname ?? '');
	const directoryURL = $derived(primaryHostname ? acmeDirectoryURL(primaryHostname) : '');
	const caEndpoint = $derived(
		primaryHostname
			? `https://${primaryHostname}/.well-known/distroface/ca.pem`
			: '/.well-known/distroface/ca.pem'
	);
	const dfcliHint = $derived(
		primaryHostname ? `dfcli trust install --server https://${primaryHostname}` : 'dfcli trust install'
	);

	// Pinned fields render disabled with a lock hint
	const locked = (path: string) => isLocked(prov, path);
	const lockHint = (path: string, help: string) =>
		locked(path) ? 'Pinned by the config file' : help;

	function seedForms(s: Settings) {
		caAcme = s.ca?.acmeEnabled ?? false;
	}

	async function load() {
		loading = true;
		loadError = '';
		try {
			const resp = await rpcClient.settings.getEffectiveSettings({ scope: systemScope }, silentCallOptions);
			eff = resp.settings ?? null;
			prov = resp.provenance;
			if (eff) seedForms(eff);
			material = await rpcClient.certificate.getTLSMaterial({}, silentCallOptions);
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
			await rpcClient.certificate.deleteTLSCertificate({ scope: TLSScope.TLS_SCOPE_APP_CA }, silentCallOptions);
			await load();
		});
	}

	onMount(() => {
		if (!authStore.canManageSettings) { goto(resolve('/admin')); return; }
		load();
	});
</script>

{#if loading}
	<div class="space-y-6">
		<Skeleton class="h-28 w-full rounded-xl" />
		<Skeleton class="h-32 w-full rounded-xl" />
	</div>
{:else if loadError}
	<div class="rounded-xl border border-destructive/40 bg-destructive/5 px-6 py-10 text-center space-y-3">
		<p class="text-sm text-destructive">{loadError}</p>
		<Button variant="outline" size="sm" onclick={load}>Retry</Button>
	</div>
{:else if eff}
	<div class="space-y-6">
		<!-- Root of trust first, everything below chains to it -->
		<FormCard title="Instance CA" description="Signs server certificates and organization CAs">
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
			{#if material?.appCa}
				<div class="mt-3 pt-3 border-t border-border/40">
					<FormField label="Trust anchor" help="Clients fetch this to trust self-issued TLS">
						<div class="flex items-center gap-2">
							<code class="text-xs bg-muted px-2 py-1 rounded font-mono min-w-0 flex-1 truncate">{caEndpoint}</code>
							<CopyButton text={caEndpoint} />
							<Button variant="outline" size="sm" class="h-7 shrink-0" onclick={downloadTrustAnchor}>Download</Button>
						</div>
						<div class="flex items-center gap-2 mt-1.5">
							<code class="text-xs bg-muted px-2 py-1 rounded font-mono min-w-0 flex-1 truncate">{dfcliHint}</code>
							<CopyButton text={dfcliHint} />
						</div>
						<p class="text-[13px] text-muted-foreground">
							Run <code class="font-mono">dfcli trust install</code> on a client so docker pull trusts the registry.
						</p>
					</FormField>
				</div>
			{/if}
			<p class="mt-3 text-[13px] text-muted-foreground">
				The primary hostname's own certificate is configured under
				<a href={resolve('/admin/network')} class="underline hover:text-foreground">Network Security</a>.
			</p>
		</FormCard>

		<!-- Acme server issuing from the instance root to other machines -->
		<FormCard title="ACME Server" description="Let external machines request certificates from this CA">
			<div class="space-y-3">
				<FormField
					label="Built-in ACME server"
					horizontal
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
				{#if caAcme}
					{#if !material?.appCa}
						<p class="text-[13px] text-amber-600 dark:text-amber-400">Generate the instance CA above so issued certificates chain to a root</p>
					{/if}
					<FormField label="Directory URL" help="Point certbot, caddy, lego, or step here">
						<div class="flex items-center gap-2">
							<code class="text-xs bg-muted px-2 py-1 rounded font-mono min-w-0 flex-1 truncate">{directoryURL}</code>
							{#if directoryURL}<CopyButton text={directoryURL} />{/if}
						</div>
						<p class="text-[13px] text-muted-foreground">
							External clients enrolled here receive certificates that chain to the instance root CA.
							This instance and its portals cannot enroll against themselves — portals wanting
							instance-issued certificates use the Org CA source instead.
						</p>
					</FormField>
				{/if}
			</div>
		</FormCard>
	</div>
{/if}

<CertUploadPanel bind:open={uploadOpen} title="Upload Instance CA" onSubmit={submitUpload} />
