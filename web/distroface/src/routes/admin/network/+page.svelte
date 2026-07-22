<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import { Input } from '$lib/components/ui/input';
	import * as RadioGroup from '$lib/components/ui/radio-group';
	import FormCard from '$lib/components/form-card.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import CertMaterialRow from '$lib/components/cert-material-row.svelte';
	import CertUploadPanel from '$lib/components/cert-upload-panel.svelte';
	import { Loader2 } from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Act, errText } from '$lib/act.svelte';
	import { certBadgeClass, certDate, certHealth, certSourceLabels, certStateBadge } from '$lib/cert-utils';
	import { isLocked, patchSettings, systemScope, type SettingsPatch } from '$lib/settings-utils';
	import { MTLSMode, TLSMode, type FieldProvenance, type Settings } from '$lib/proto/distroface/v1/settings_pb';
	import {
		CertSource, TLSScope,
		type GetCertStatusResponse, type GetTLSMaterialResponse
	} from '$lib/proto/distroface/v1/certificate_pb';

	let eff = $state<Settings | null>(null);
	let prov = $state<FieldProvenance[]>([]);
	let certStatus = $state<GetCertStatusResponse | null>(null);
	let material = $state<GetTLSMaterialResponse | null>(null);
	let loading = $state(true);
	let loadError = $state('');

	let tlsMode = $state<TLSMode>(TLSMode.TLS_MODE_DUAL);
	let mtlsMode = $state<MTLSMode>(MTLSMode.MTLS_MODE_OFF);
	let primarySource = $state<CertSource>(CertSource.CONFIG);
	let acmeEnabled = $state(false);
	let acmeEmail = $state('');
	let acmeDirectory = $state('');
	let challengePort = $state('');

	const modeAct = new Act();
	const mtlsAct = new Act();
	const primaryAct = new Act();
	const acmeSwitchAct = new Act();
	const acmeDirAct = new Act();
	const acmeEmailAct = new Act();
	const acmePortAct = new Act();
	const appCertAct = new Act();
	const issueAppAct = new Act();

	let uploadOpen = $state(false);

	const sourceOptions = [CertSource.CONFIG, CertSource.MANUAL, CertSource.APP_CA, CertSource.ACME];
	const modeOptions: { value: TLSMode; label: string; help: string }[] = [
		{ value: TLSMode.TLS_MODE_DUAL, label: 'TLS and cleartext', help: 'Handshakes serve TLS, plain HTTP still answers' },
		{ value: TLSMode.TLS_MODE_HTTPS_ONLY, label: 'HTTPS only', help: 'Cleartext requests redirect to HTTPS' },
		{ value: TLSMode.TLS_MODE_CLEARTEXT, label: 'Cleartext only', help: 'Never terminate TLS in app' }
	];
	const mtlsOptions: { value: MTLSMode; label: string; help: string }[] = [
		{ value: MTLSMode.MTLS_MODE_OFF, label: 'Off', help: 'No client certificate requested' },
		{ value: MTLSMode.MTLS_MODE_OPTIONAL, label: 'Optional', help: 'Verified when presented, identity recorded' },
		{ value: MTLSMode.MTLS_MODE_REQUIRED, label: 'Required', help: 'Handshake fails without a trusted client cert' }
	];

	const primaryHostname = $derived(eff?.server?.publicHostname ?? '');
	const primaryHealth = $derived(certHealth(certStatus?.acmeCert));
	const primaryBadge = $derived(certStateBadge(certStatus?.state));

	const acmeSwitchHelp = $derived(
		challengePort
			? `Challenges answer on http port ${challengePort}`
			: 'Challenges use tls-alpn-01 on port 443'
	);

	// Pinned fields render disabled with a lock hint
	const locked = (path: string) => isLocked(prov, path);
	const lockHint = (path: string, help: string) =>
		locked(path) ? 'Pinned by the config file' : help;

	function seedForms(s: Settings) {
		tlsMode = s.tls?.mode ?? TLSMode.TLS_MODE_DUAL;
		mtlsMode = s.tls?.mtlsMode ?? MTLSMode.MTLS_MODE_OFF;
		primarySource = s.tls?.primarySource || CertSource.CONFIG;
		acmeEnabled = s.acme?.enabled ?? false;
		acmeEmail = s.acme?.email ?? '';
		acmeDirectory = s.acme?.directoryUrl ?? '';
		challengePort = s.acme?.challengePort ?? '';
	}

	async function loadStatus() {
		try {
			certStatus = await rpcClient.certificate.getCertStatus({}, silentCallOptions);
		} catch {
			// Badge hides without status
		}
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
			await loadStatus();
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
			await loadStatus();
		});
		if (!ok && eff) seedForms(eff);
	}

	function setTlsMode(v: TLSMode) {
		tlsMode = v;
		apply(modeAct, { tls: { mode: v } }, ['tls.mode']);
	}

	function setMtlsMode(v: MTLSMode) {
		mtlsMode = v;
		apply(mtlsAct, { tls: { mtlsMode: v } }, ['tls.mtls_mode']);
	}

	function setPrimarySource(v: CertSource) {
		primarySource = v;
		apply(primaryAct, { tls: { primarySource: v } }, ['tls.primary_source']);
	}

	function setAcmeEnabled(v: boolean) {
		acmeEnabled = v;
		apply(acmeSwitchAct, { acme: { enabled: v } }, ['acme.enabled']);
	}

	function commitAcmeDirectory() {
		if (acmeDirectory.trim() === (eff?.acme?.directoryUrl ?? '')) return;
		apply(acmeDirAct, { acme: { directoryUrl: acmeDirectory.trim() } }, ['acme.directory_url']);
	}

	function commitAcmeEmail() {
		if (acmeEmail.trim() === (eff?.acme?.email ?? '')) return;
		apply(acmeEmailAct, { acme: { email: acmeEmail.trim() } }, ['acme.email']);
	}

	function commitChallengePort() {
		if (challengePort.trim() === (eff?.acme?.challengePort ?? '')) return;
		apply(acmePortAct, { acme: { challengePort: challengePort.trim() } }, ['acme.challenge_port']);
	}

	function issueApp() {
		issueAppAct.run(async () => {
			await rpcClient.certificate.issueCertificate({}, silentCallOptions);
			await load();
		});
	}

	async function submitUpload(certPem: string, keyPem: string) {
		await rpcClient.certificate.uploadTLSCertificate(
			{ scope: TLSScope.TLS_SCOPE_APP, certPem, keyPem },
			silentCallOptions
		);
		await load();
	}

	function removeAppCert() {
		appCertAct.run(async () => {
			await rpcClient.certificate.deleteTLSCertificate({ scope: TLSScope.TLS_SCOPE_APP }, silentCallOptions);
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
		<Skeleton class="h-52 w-full rounded-xl" />
		<Skeleton class="h-32 w-full rounded-xl" />
		<Skeleton class="h-40 w-full rounded-xl" />
	</div>
{:else if loadError}
	<div class="rounded-xl border border-destructive/40 bg-destructive/5 px-6 py-10 text-center space-y-3">
		<p class="text-sm text-destructive">{loadError}</p>
		<Button variant="outline" size="sm" onclick={load}>Retry</Button>
	</div>
{:else if eff}
	<div class="space-y-6">
		<!-- Server certificate -->
		<FormCard title="Server Certificate" description="What the primary hostname serves">
			<div class="space-y-3">
				<div class="flex items-center justify-between gap-4 rounded-lg border border-border/60 px-4 py-3.5">
					<div class="flex items-center gap-2 min-w-0 flex-wrap">
						<code class="text-xs bg-muted px-2 py-1 rounded font-mono">{primaryHostname || '-'}</code>
						{#if certStatus?.problems?.length}
							<span class="text-xs text-destructive">{certStatus.problems[0]}</span>
						{/if}
					</div>
					{#if primaryHealth.issued}
						<Badge variant="outline" class="text-xs shrink-0 {certBadgeClass(primaryHealth.tone)}" title={certDate(certStatus?.acmeCert)}>
							{primaryHealth.label}
						</Badge>
					{:else if primaryBadge}
						<Badge variant="outline" class="text-xs shrink-0 {primaryBadge.cls}">{primaryBadge.label}</Badge>
					{/if}
				</div>

				<FormField
					label="Listener mode"
					help={lockHint('tls.mode', 'Applies live to the primary hostname')}
					tag={modeAct.tag}
					error={modeAct.error}
				>
					<RadioGroup.Root
						value={String(tlsMode)}
						onValueChange={(v) => setTlsMode(Number(v) as TLSMode)}
						disabled={modeAct.busy || locked('tls.mode')}
						class="gap-0 divide-y divide-border/40"
					>
						{#each modeOptions as option (option.value)}
							<div class="py-2.5 first:pt-1 last:pb-1">
								<label class="flex items-center gap-2.5 cursor-pointer" for="mode-{option.value}">
									<RadioGroup.Item value={String(option.value)} id="mode-{option.value}" />
									<span class="text-sm">{option.label}</span>
									<span class="text-xs text-muted-foreground">{option.help}</span>
								</label>
							</div>
						{/each}
					</RadioGroup.Root>
				</FormField>

				<FormField
					label="Certificate source"
					help={lockHint('tls.primary_source', '')}
					tag={primaryAct.tag}
					error={primaryAct.error}
				>
					<RadioGroup.Root
						value={String(primarySource)}
						onValueChange={(v) => setPrimarySource(Number(v) as CertSource)}
						disabled={primaryAct.busy || locked('tls.primary_source')}
						class="gap-0 divide-y divide-border/40"
					>
						{#each sourceOptions as option (option)}
							<div class="py-2.5 first:pt-1 last:pb-1">
								<label class="flex items-center gap-2.5 cursor-pointer" for="src-{option}">
									<RadioGroup.Item value={String(option)} id="src-{option}" />
									<span class="text-sm">{certSourceLabels[option]}</span>
								</label>
								{#if primarySource === option}
									<div class="mt-2 ml-7">
										{#if option === CertSource.MANUAL}
											<CertMaterialRow
												title="Server certificate"
												empty="PEM pair covering {primaryHostname || 'the primary hostname'}"
												material={material?.appCert}
												busy={appCertAct.busy}
												error={appCertAct.error}
												onUpload={() => (uploadOpen = true)}
												onRemove={removeAppCert}
											/>
										{:else if option === CertSource.APP_CA}
											{#if material?.appCa}
												<p class="text-[13px] text-muted-foreground">
													Serves leaves minted from the instance CA, clients must trust its chain.
												</p>
											{:else}
												<p class="text-[13px] text-amber-600 dark:text-amber-400">
													No instance CA yet, generate one on the
													<a href={resolve('/admin/pki')} class="underline hover:text-foreground">Certificate Authority</a> page.
												</p>
											{/if}
										{:else if option === CertSource.ACME}
											{#if !acmeEnabled}
												<div class="flex items-center gap-3">
													<p class="text-[13px] text-amber-600 dark:text-amber-400">ACME is off</p>
													<Button
														variant="outline"
														size="sm"
														class="h-7"
														disabled={acmeSwitchAct.busy || locked('acme.enabled')}
														onclick={() => setAcmeEnabled(true)}
													>
														{#if acmeSwitchAct.busy}
															<Loader2 class="h-3.5 w-3.5 animate-spin" />
														{:else}
															Turn on ACME
														{/if}
													</Button>
												</div>
											{:else}
												<div class="flex items-center gap-3">
													<Button
														variant="outline"
														size="sm"
														class="h-7"
														disabled={issueAppAct.busy}
														onclick={issueApp}
													>
														{#if issueAppAct.busy}
															<Loader2 class="h-3.5 w-3.5 animate-spin" />
														{:else}
															{primaryHealth.issued ? 'Renew certificate' : 'Issue certificate'}
														{/if}
													</Button>
													{#if issueAppAct.error}
														<p class="text-[13px] text-destructive">{issueAppAct.error}</p>
													{:else if issueAppAct.saved}
														<p class="text-[13px] text-primary">Issued</p>
													{:else if !primaryHealth.issued}
														<p class="text-[13px] text-muted-foreground">Also issues on the first handshake</p>
													{/if}
												</div>
											{/if}
										{/if}
									</div>
								{/if}
							</div>
						{/each}
					</RadioGroup.Root>
				</FormField>
			</div>
		</FormCard>

		<!-- ACME client -->
		<FormCard title="ACME Client" description="Automatic certificates from an external CA">
			<div class="space-y-3">
				<FormField
					label="Automatic issuance"
					horizontal
					help={lockHint('acme.enabled', acmeSwitchHelp)}
					tag={acmeSwitchAct.tag}
					error={acmeSwitchAct.error}
				>
					<Switch
						checked={acmeEnabled}
						disabled={acmeSwitchAct.busy || locked('acme.enabled')}
						onCheckedChange={setAcmeEnabled}
					/>
				</FormField>
				{#if acmeEnabled}
					<div class="grid grid-cols-1 sm:grid-cols-3 gap-3">
						<FormField
							label="Account email"
							id="acme-email"
							help={lockHint('acme.email', 'Receives expiry notices from the CA')}
							tag={acmeEmailAct.tag}
							error={acmeEmailAct.error}
						>
							<Input
								id="acme-email"
								bind:value={acmeEmail}
								placeholder="admin@example.com"
								disabled={acmeEmailAct.busy || locked('acme.email')}
								onblur={commitAcmeEmail}
								onkeydown={(e) => { if (e.key === 'Enter') commitAcmeEmail(); }}
							/>
						</FormField>
						<FormField
							label="Directory URL"
							id="acme-dir"
							help={lockHint('acme.directory_url', 'Empty uses Lets Encrypt production')}
							tag={acmeDirAct.tag}
							error={acmeDirAct.error}
						>
							<Input
								id="acme-dir"
								bind:value={acmeDirectory}
								class="font-mono text-xs"
								placeholder="https://acme-v02.api.letsencrypt.org/directory"
								disabled={acmeDirAct.busy || locked('acme.directory_url')}
								onblur={commitAcmeDirectory}
								onkeydown={(e) => { if (e.key === 'Enter') commitAcmeDirectory(); }}
							/>
						</FormField>
						<FormField
							label="Challenge port"
							id="acme-port"
							help={lockHint('acme.challenge_port', 'Cleartext http-01 listener, empty disables')}
							tag={acmePortAct.tag}
							error={acmePortAct.error}
						>
							<Input
								id="acme-port"
								bind:value={challengePort}
								class="font-mono text-xs w-28"
								placeholder="80"
								disabled={acmePortAct.busy || locked('acme.challenge_port')}
								onblur={commitChallengePort}
								onkeydown={(e) => { if (e.key === 'Enter') commitChallengePort(); }}
							/>
						</FormField>
					</div>
				{/if}
			</div>
		</FormCard>

		<!-- Mutual TLS -->
		<FormCard title="Mutual TLS" description="Require client certificates issued by this instance">
			<FormField
				label="Client certificate policy"
				help={lockHint('tls.mtls_mode', 'Applies to the primary hostname, portals can override')}
				tag={mtlsAct.tag}
				error={mtlsAct.error}
			>
				<RadioGroup.Root
					value={String(mtlsMode)}
					onValueChange={(v) => setMtlsMode(Number(v) as MTLSMode)}
					disabled={mtlsAct.busy || locked('tls.mtls_mode')}
					class="gap-0 divide-y divide-border/40"
				>
					{#each mtlsOptions as option (option.value)}
						<div class="py-2.5 first:pt-1 last:pb-1">
							<label class="flex items-center gap-2.5 cursor-pointer" for="mtls-{option.value}">
								<RadioGroup.Item value={String(option.value)} id="mtls-{option.value}" />
								<span class="text-sm">{option.label}</span>
								<span class="text-xs text-muted-foreground">{option.help}</span>
							</label>
						</div>
					{/each}
				</RadioGroup.Root>
				{#if mtlsMode !== MTLSMode.MTLS_MODE_OFF && !material?.appCa}
					<p class="text-[13px] text-amber-600 dark:text-amber-400">
						Generate the instance CA on the
						<a href={resolve('/admin/pki')} class="underline hover:text-foreground">Certificate Authority</a>
						page so client certificates can be verified.
					</p>
				{:else if mtlsMode === MTLSMode.MTLS_MODE_REQUIRED}
					<p class="text-[13px] text-muted-foreground">
						Clients need a certificate issued by this instance's CA — sign one from a CSR or an organization CA.
					</p>
				{/if}
			</FormField>
		</FormCard>
	</div>
{/if}

<CertUploadPanel bind:open={uploadOpen} title="Upload Server Certificate" onSubmit={submitUpload} />
