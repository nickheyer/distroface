<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import * as RadioGroup from '$lib/components/ui/radio-group';
	import FormCard from '$lib/components/form-card.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import CertMaterialRow from '$lib/components/cert-material-row.svelte';
	import CertUploadPanel from '$lib/components/cert-upload-panel.svelte';
	import { Loader2 } from '@lucide/svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { Act, errText } from '$lib/act.svelte';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { downloadBlob } from '$lib/download';
	import { certBadgeClass, certDate, certHealth, certSourceLabels, certStateBadge } from '$lib/cert-utils';
	import { isLocked, patchSettings, systemScope, type SettingsPatch } from '$lib/settings-utils';
	import { TLSMode, type FieldProvenance, type Settings } from '$lib/proto/distroface/v1/settings_pb';
	import {
		CertSource, TLSScope,
		type CertificateDomain, type GetCertStatusResponse, type GetTLSMaterialResponse
	} from '$lib/proto/distroface/v1/certificate_pb';

	let eff = $state<Settings | null>(null);
	let prov = $state<FieldProvenance[]>([]);
	let certStatus = $state<GetCertStatusResponse | null>(null);
	let material = $state<GetTLSMaterialResponse | null>(null);
	let pending = $state<CertificateDomain[]>([]);
	let loading = $state(true);
	let loadError = $state('');

	let tlsMode = $state<TLSMode>(TLSMode.TLS_MODE_DUAL);
	let primarySource = $state<CertSource>(CertSource.CONFIG);
	let acmeEnabled = $state(false);
	let acmeEmail = $state('');
	let acmeDirectory = $state('');
	let challengePort = $state('');
	let requireApproval = $state(false);
	let blacklistText = $state('');

	const modeAct = new Act();
	const primaryAct = new Act();
	const acmeSwitchAct = new Act();
	const acmeDirAct = new Act();
	const acmeEmailAct = new Act();
	const acmePortAct = new Act();
	const approvalSwitchAct = new Act();
	const blacklistAct = new Act();
	const appCertAct = new Act();
	const appCaAct = new Act();
	const issueAppAct = new Act();
	const approvalsAct = new Act();

	let approvalBusy = $state<string | null>(null);

	let uploadOpen = $state(false);
	let uploadScope = $state<TLSScope>(TLSScope.TLS_SCOPE_APP);

	const sourceOptions = [CertSource.CONFIG, CertSource.MANUAL, CertSource.APP_CA, CertSource.ACME];
	const modeOptions: { value: TLSMode; label: string; help: string }[] = [
		{ value: TLSMode.TLS_MODE_DUAL, label: 'TLS and cleartext', help: 'Handshakes serve TLS, plain HTTP still answers' },
		{ value: TLSMode.TLS_MODE_HTTPS_ONLY, label: 'HTTPS only', help: 'Cleartext requests redirect to HTTPS' },
		{ value: TLSMode.TLS_MODE_CLEARTEXT, label: 'Cleartext only', help: 'Never terminate TLS in app' }
	];
	const blacklistPlaceholder = 'internal.corp\n*.example.org';

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
		primarySource = s.tls?.primarySource || CertSource.CONFIG;
		acmeEnabled = s.acme?.enabled ?? false;
		acmeEmail = s.acme?.email ?? '';
		acmeDirectory = s.acme?.directoryUrl ?? '';
		challengePort = s.acme?.challengePort ?? '';
		requireApproval = s.portals?.requireHostnameApproval ?? false;
		blacklistText = (s.portals?.hostnameBlacklist ?? []).join('\n');
	}

	async function loadPending() {
		try {
			const resp = await rpcClient.certificate.listCertificateDomains({
				pendingOnly: true,
				page: { pageSize: 200 }
			}, silentCallOptions);
			pending = resp.domains;
		} catch {
			// List refreshes on the next action
		}
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
			await Promise.all([loadStatus(), loadPending()]);
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

	function setRequireApproval(v: boolean) {
		requireApproval = v;
		apply(approvalSwitchAct, { portals: { requireHostnameApproval: v } }, ['portals.require_hostname_approval']);
	}

	function commitBlacklist() {
		const patterns = blacklistText.split('\n').map((s) => s.trim()).filter(Boolean);
		if (patterns.join('\n') === (eff?.portals?.hostnameBlacklist ?? []).join('\n')) return;
		apply(blacklistAct, { portals: { hostnameBlacklist: patterns } }, ['portals.hostname_blacklist']);
	}

	function issueApp() {
		issueAppAct.run(async () => {
			await rpcClient.certificate.issueCertificate({}, silentCallOptions);
			await load();
		});
	}

	async function approve(domain: CertificateDomain) {
		approvalBusy = domain.id;
		await approvalsAct.run(() =>
			rpcClient.certificate.approveCertificateDomain({ id: domain.id }, silentCallOptions)
		);
		approvalBusy = null;
		await loadPending();
	}

	async function deny(domain: CertificateDomain) {
		approvalBusy = domain.id;
		await approvalsAct.run(() =>
			rpcClient.certificate.removeCertificateDomain({ id: domain.id }, silentCallOptions)
		);
		approvalBusy = null;
		await loadPending();
	}

	function openUpload(scope: TLSScope) {
		uploadScope = scope;
		uploadOpen = true;
	}

	async function submitUpload(certPem: string, keyPem: string) {
		await rpcClient.certificate.uploadTLSCertificate(
			{ scope: uploadScope, certPem, keyPem },
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
		<Skeleton class="h-52 w-full rounded-xl" />
		<Skeleton class="h-40 w-full rounded-xl" />
	</div>
{:else if loadError}
	<div class="rounded-xl border border-destructive/40 bg-destructive/5 px-6 py-10 text-center space-y-3">
		<p class="text-sm text-destructive">{loadError}</p>
		<Button variant="outline" size="sm" onclick={load}>Retry</Button>
	</div>
{:else if eff}
	<div class="space-y-6">
		<!-- Root of trust first, everything below can chain to it -->
		<FormCard title="Instance CA" description="Signs server certificates and organization CAs">
			<CertMaterialRow
				title="Root certificate"
				empty="Generate or upload a root"
				material={material?.appCa}
				busy={appCaAct.busy}
				error={appCaAct.error}
				onGenerate={generateAppCA}
				onUpload={() => openUpload(TLSScope.TLS_SCOPE_APP_CA)}
				onDownload={material?.appCa ? downloadAppCA : undefined}
				onRemove={removeAppCA}
			/>
		</FormCard>

		<!-- ACME -->
		<FormCard title="ACME" description="Automatic certificates from a public CA">
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
					help={lockHint('tls.primary_source', 'Where the served certificate comes from')}
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
												onUpload={() => openUpload(TLSScope.TLS_SCOPE_APP)}
												onRemove={removeAppCert}
											/>
										{:else if option === CertSource.APP_CA && !material?.appCa}
											<p class="text-[13px] text-amber-600 dark:text-amber-400">Generate the instance CA above first</p>
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

		<!-- Portal hostnames -->
		<FormCard title="Portal Hostnames" description="Rules for hostnames portals can claim">
			<div class="space-y-3">
				<FormField
					label="Require approval"
					horizontal
					help={lockHint('portals.require_hostname_approval', 'New portal hostnames wait for an admin')}
					tag={approvalSwitchAct.tag}
					error={approvalSwitchAct.error}
				>
					<Switch
						checked={requireApproval}
						disabled={approvalSwitchAct.busy || locked('portals.require_hostname_approval')}
						onCheckedChange={setRequireApproval}
					/>
				</FormField>

				{#if pending.length > 0}
					<FormField
						label="Waiting for approval"
						help="Approve to allow issuance"
						tag={approvalsAct.tag}
						error={approvalsAct.error}
					>
						<div class="rounded-lg border border-border/60 divide-y divide-border/40">
							{#each pending as domain (domain.id)}
								<div class="flex items-center justify-between gap-3 px-3 py-2.5">
									<div class="min-w-0 flex items-center gap-2 flex-wrap">
										<span class="font-mono text-sm">{domain.domain}</span>
										{#if domain.orgName}
											<a href={resolve('/orgs/[name]', { name: domain.orgName })} class="text-xs text-muted-foreground hover:text-primary transition-colors">
												{domain.orgName}
											</a>
										{/if}
										{#if domain.createdAt}
											<span class="text-xs text-muted-foreground/70">{relativeTime(timestampDate(domain.createdAt))}</span>
										{/if}
									</div>
									<div class="flex gap-1 shrink-0">
										<Button
											variant="outline"
											size="sm"
											class="h-7"
											disabled={approvalBusy !== null}
											onclick={() => approve(domain)}
										>
											{#if approvalBusy === domain.id}
												<Loader2 class="h-3.5 w-3.5 animate-spin" />
											{:else}
												Approve
											{/if}
										</Button>
										<Button
											variant="ghost"
											size="sm"
											class="h-7 px-2 text-xs text-destructive hover:text-destructive"
											disabled={approvalBusy !== null}
											onclick={() => deny(domain)}
										>
											Deny
										</Button>
									</div>
								</div>
							{/each}
						</div>
					</FormField>
				{/if}

				<FormField
					label="Blocked patterns"
					id="hostname-blacklist"
					help={lockHint('portals.hostname_blacklist', 'One pattern per line')}
					tag={blacklistAct.tag}
					error={blacklistAct.error}
				>
					<Textarea
						id="hostname-blacklist"
						bind:value={blacklistText}
						class="font-mono text-xs"
						rows={3}
						placeholder={blacklistPlaceholder}
						disabled={blacklistAct.busy || locked('portals.hostname_blacklist')}
						onblur={commitBlacklist}
					/>
				</FormField>
			</div>
		</FormCard>
	</div>
{/if}

<CertUploadPanel
	bind:open={uploadOpen}
	title={uploadScope === TLSScope.TLS_SCOPE_APP_CA ? 'Upload Instance CA' : 'Upload Server Certificate'}
	onSubmit={submitUpload}
/>
