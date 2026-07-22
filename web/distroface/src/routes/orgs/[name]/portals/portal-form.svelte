<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import { Textarea } from '$lib/components/ui/textarea';
	import * as Popover from '$lib/components/ui/popover';
	import * as RadioGroup from '$lib/components/ui/radio-group';
	import * as Select from '$lib/components/ui/select';
	import FormField from '$lib/components/form-field.svelte';
	import FormCard from '$lib/components/form-card.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import { Info, Plus, X } from '@lucide/svelte';
	import { Act } from '$lib/act.svelte';
	import { effectiveAddress, placementError, portalScheme } from '$lib/portal-address';
	import { certDate, certSourceLabels, certStateBadge, isIssuableHostname } from '$lib/cert-utils';
	import { orgScope, patchSettings, portalScope } from '$lib/settings-utils';
	import { authStore } from '$lib/stores/auth.svelte';
	import type { RegistryPortal } from '$lib/proto/distroface/v1/portal_pb';
	import { CertSource, TLSScope, type TLSMaterialInfo } from '$lib/proto/distroface/v1/certificate_pb';
	import { MTLSMode } from '$lib/proto/distroface/v1/settings_pb';

	let {
		orgName,
		orgId,
		mainPort = 0,
		portal = null
	}: {
		orgName: string;
		orgId: string;
		mainPort?: number;
		portal?: RegistryPortal | null;
	} = $props();

	type RuleDraft = { pattern: string; replace: string };
	type PortalMode = 'registry' | 'proxy';

	// Draft snapshot on mount, parent keys this component per portal
	/* svelte-ignore state_referenced_locally */
	let mode = $state<PortalMode>(portal?.backendUrl ? 'proxy' : 'registry');
	/* svelte-ignore state_referenced_locally */
	let name = $state(portal?.name ?? '');
	/* svelte-ignore state_referenced_locally */
	let hostname = $state(portal?.hostname ?? '');
	/* svelte-ignore state_referenced_locally */
	let portText = $state(portal && portal.port > 0 ? String(portal.port) : '');
	/* svelte-ignore state_referenced_locally */
	let mapUnqualified = $state(portal?.mapUnqualified ?? true);
	/* svelte-ignore state_referenced_locally */
	let allowPush = $state(portal?.allowPush ?? true);
	/* svelte-ignore state_referenced_locally */
	let requireAuth = $state(portal?.requireAuth ?? false);
	/* svelte-ignore state_referenced_locally */
	let showExitLink = $state(!(portal?.hidePrimaryLink ?? false));
	/* svelte-ignore state_referenced_locally */
	let tls = $state(portal?.tls ?? false);
	/* svelte-ignore state_referenced_locally */
	let backendUrl = $state(portal?.backendUrl ?? '');
	/* svelte-ignore state_referenced_locally */
	let backendRewriteHost = $state(portal?.backendRewriteHost ?? false);
	/* svelte-ignore state_referenced_locally */
	let backendInsecure = $state(portal?.backendInsecure ?? false);
	/* svelte-ignore state_referenced_locally */
	let certSource = $state<CertSource>(portal?.certSource || CertSource.NONE);
	let acmeEmail = $state('');
	let acmeDirectory = $state('');
	let savedAcmeEmail = $state('');
	let savedAcmeDir = $state('');
	let provisionCert = $state(true);
	let certPem = $state('');
	let keyPem = $state('');
	let orgCA = $state<TLSMaterialInfo | null>(null);
	let orgCert = $state<TLSMaterialInfo | null>(null);
	let portalCert = $state<TLSMaterialInfo | null>(null);
	let acmeDirInherited = $state('');
	let acmeEmailInherited = $state('');
	let mtlsMode = $state<MTLSMode>(MTLSMode.MTLS_MODE_UNSPECIFIED);
	let savedMtlsMode = $state<MTLSMode>(MTLSMode.MTLS_MODE_UNSPECIFIED);
	/* svelte-ignore state_referenced_locally */
	let rules = $state<RuleDraft[]>(
		portal?.rules.map((r) => ({ pattern: r.pattern, replace: r.replace })) ?? []
	);
	const submitAct = new Act();

	const sourceOptions = [
		CertSource.NONE, CertSource.ACME, CertSource.ORG_CA, CertSource.ORG_CERT, CertSource.MANUAL
	];

	const mtlsOptions = [
		{ value: MTLSMode.MTLS_MODE_UNSPECIFIED, label: 'Inherit from instance' },
		{ value: MTLSMode.MTLS_MODE_OFF, label: 'Off' },
		{ value: MTLSMode.MTLS_MODE_OPTIONAL, label: 'Optional' },
		{ value: MTLSMode.MTLS_MODE_REQUIRED, label: 'Required' }
	];
	const mtlsLabels: Record<number, string> = Object.fromEntries(
		mtlsOptions.map((o) => [o.value, o.label])
	);

	async function loadMaterial() {
		try {
			const resp = await rpcClient.certificate.getTLSMaterial({
				orgId,
				portalId: portal?.id ?? ''
			}, silentCallOptions);
			orgCA = resp.orgCa ?? null;
			orgCert = resp.orgCert ?? null;
			portalCert = resp.portalCert ?? null;
		} catch {
			// Notes fall back to their generic wording
		}
	}

	async function loadInherited() {
		try {
			if (portal) {
				const stored = await rpcClient.settings.getSettings({ scope: portalScope(portal.id) }, silentCallOptions);
				acmeEmail = stored.settings?.acme?.email ?? '';
				acmeDirectory = stored.settings?.acme?.directoryUrl ?? '';
				savedAcmeEmail = acmeEmail;
				savedAcmeDir = acmeDirectory;
				mtlsMode = stored.settings?.tls?.mtlsMode ?? MTLSMode.MTLS_MODE_UNSPECIFIED;
				savedMtlsMode = mtlsMode;
			}
			const eff = await rpcClient.settings.getEffectiveSettings({ scope: orgScope(orgId) }, silentCallOptions);
			acmeDirInherited = eff.settings?.acme?.directoryUrl ?? '';
			acmeEmailInherited = eff.settings?.acme?.email ?? '';
		} catch {
			// Placeholders stay generic
		}
	}

	// Portal overrides live at the portal settings scope
	async function saveAcme(portalId: string) {
		const email = acmeEmail.trim();
		const dir = acmeDirectory.trim();
		if (email === savedAcmeEmail && dir === savedAcmeDir) return;
		await patchSettings(portalScope(portalId), {
			acme: { ...(email ? { email } : {}), ...(dir ? { directoryUrl: dir } : {}) }
		}, ['acme.email', 'acme.directory_url']);
		savedAcmeEmail = email;
		savedAcmeDir = dir;
	}

	// Portal mtls override, inherit clears it back to the instance policy
	async function saveMtls(portalId: string) {
		if (mtlsMode === savedMtlsMode) return;
		const inherit = mtlsMode === MTLSMode.MTLS_MODE_UNSPECIFIED;
		await patchSettings(portalScope(portalId), {
			tls: inherit ? {} : { mtlsMode }
		}, ['tls.mtls_mode']);
		savedMtlsMode = mtlsMode;
	}

	onMount(() => {
		loadMaterial();
		loadInherited();
	});

	function goBack() {
		goto(resolve('/orgs/[name]/portals', { name: orgName }));
	}

	// Mirrors the server's backend url shape check
	function backendUrlError(raw: string): string {
		const trimmed = raw.trim();
		if (trimmed === '') return '';
		let u: URL;
		try {
			u = new URL(trimmed);
		} catch {
			return 'Must look like http(s)://host[:port][/path]';
		}
		if ((u.protocol !== 'http:' && u.protocol !== 'https:') || u.username !== '' || u.search !== '' || u.hash !== '') {
			return 'Must look like http(s)://host[:port][/path]';
		}
		return '';
	}

	const addressError = $derived(
		hostname.trim() === '' && portText.trim() === '' ? '' : placementError(hostname, portText)
	);
	const isProxy = $derived(mode === 'proxy');
	const httpsBackend = $derived(backendUrl.trim().startsWith('https://'));
	const backendError = $derived(backendUrlError(backendUrl));
	// Same check the server runs before accepting a backend change
	const canEditBackend = $derived(authStore.canManageSettings);
	const formValid = $derived(
		name.trim() !== '' &&
		(hostname.trim() !== '' || portText.trim() !== '') &&
		placementError(hostname, portText) === '' &&
		(!isProxy || (backendUrl.trim() !== '' && backendError === ''))
	);
	const previewAddress = $derived(
		placementError(hostname, portText) === '' && (hostname.trim() !== '' || portText.trim() !== '')
			? effectiveAddress(hostname.trim().toLowerCase(), Number(portText.trim()) || 0)
			: ''
	);
	const previewImage = $derived(mapUnqualified ? 'myimage' : `${orgName}/myimage`);
	const canProvision = $derived(!portal && certSource === CertSource.ACME && isIssuableHostname(hostname));
	const pemsFilled = $derived(certPem.trim() !== '' && keyPem.trim() !== '');
	// Explicit source means https answers, the tls flag only forces redirects
	const httpsOn = $derived(certSource !== CertSource.NONE);
	const scheme = $derived(portalScheme(certSource));
	// Manual portals must not go live without material to serve
	const manualNeedsPems = $derived(certSource === CertSource.MANUAL && !portalCert && !pemsFilled);
	// Live serving state computed by the server, only exists after save
	const liveState = $derived(portal ? certStateBadge(portal.certState) : null);
	const ruleCount = $derived(
		rules.filter((r) => r.pattern.trim() !== '' || r.replace.trim() !== '').length
	);

	// Issuance failures surface as the portal's live cert state
	async function provisionCertificate(portalId: string) {
		try {
			await rpcClient.certificate.issueCertificate(
				{ target: { case: 'portalId', value: portalId } },
				silentCallOptions
			);
		} catch {
			// Portal list shows the pending or error state
		}
	}

	// Throws on failure so the caller never reports a broken portal as done
	async function uploadPortalCert(portalId: string) {
		const resp = await rpcClient.certificate.uploadTLSCertificate({
			scope: TLSScope.TLS_SCOPE_PORTAL,
			orgId,
			portalId,
			certPem,
			keyPem
		}, silentCallOptions);
		portalCert = resp.info ?? null;
		certPem = '';
		keyPem = '';
	}

	async function submit() {
		if (!formValid || manualNeedsPems) return;
		const cleanedRules = rules
			.map((r) => ({ pattern: r.pattern.trim(), replace: r.replace.trim() }))
			.filter((r) => r.pattern !== '' || r.replace !== '');
		// Modes are exclusive, the other mode's knobs reset on save
		const common = {
			orgId,
			name: name.trim(),
			hostname: hostname.trim().toLowerCase(),
			port: Number(portText.trim()) || 0,
			tls,
			certSource,
			...(isProxy
				? {
						mapUnqualified: false,
						allowPush: false,
						requireAuth: false,
						hidePrimaryLink: false,
						rules: [],
						backendUrl: backendUrl.trim(),
						backendRewriteHost,
						backendInsecure: httpsBackend && backendInsecure
					}
				: {
						mapUnqualified,
						allowPush,
						requireAuth,
						hidePrimaryLink: !showExitLink,
						rules: cleanedRules,
						backendUrl: '',
						backendRewriteHost: false,
						backendInsecure: false
					})
		};
		let redirected = false;
		const ok = await submitAct.run(async () => {
			if (portal) {
				await rpcClient.portal.updatePortal({ ...common, id: portal.id, setRules: true }, silentCallOptions);
				await saveAcme(portal.id);
				await saveMtls(portal.id);
				if (certSource === CertSource.MANUAL && pemsFilled) {
					await uploadPortalCert(portal.id);
				}
				return;
			}
			const resp = await rpcClient.portal.createPortal(common, silentCallOptions);
			const created = resp.portal;
			if (!created) return;
			await saveAcme(created.id);
			await saveMtls(created.id);
			if (certSource === CertSource.MANUAL && pemsFilled) {
				try {
					await uploadPortalCert(created.id);
				} catch {
					// Portal exists but serves nothing, its editor shows the broken state
					redirected = true;
					goto(resolve('/orgs/[name]/portals/[id]', { name: orgName, id: created.id }));
					return;
				}
			}
			if (canProvision && provisionCert) {
				await provisionCertificate(created.id);
			}
		});
		if (ok && !redirected) goBack();
	}
</script>

<div class="grid gap-6 lg:grid-cols-[1fr_19rem] items-start">
	<div class="space-y-4 min-w-0">
		<FormCard title="Portal" description="Label shown in the portal list.">
			<FormField label="Name" id="portal-name" required>
				<Input id="portal-name" bind:value={name} placeholder="public-mirror" class="max-w-sm" autocomplete="off" />
			</FormField>
		</FormCard>

		<FormCard title="Mode" description="What this portal serves. Everything below follows from this choice.">
			<RadioGroup.Root
				value={mode}
				onValueChange={(v) => (mode = v as PortalMode)}
				disabled={!canEditBackend}
				class="grid grid-cols-1 sm:grid-cols-2 gap-3"
			>
				<label
					for="portal-mode-registry"
					class="flex flex-col gap-1 rounded-lg border p-3.5 transition-colors has-focus-visible:ring-2 has-focus-visible:ring-ring
						{mode === 'registry' ? 'border-primary bg-primary/5' : 'border-border/60'}
						{canEditBackend ? 'cursor-pointer hover:border-border' : 'cursor-not-allowed opacity-60'}"
				>
					<RadioGroup.Item value="registry" id="portal-mode-registry" class="sr-only" />
					<span class="text-sm font-medium">Org Registry</span>
					<span class="text-xs text-muted-foreground">
						Provides a dedicated access point & web-ui for organization resources
					</span>
				</label>
				<div class="relative">
					<label
						for="portal-mode-proxy"
						class="flex h-full flex-col gap-1 rounded-lg border p-3.5 transition-colors has-focus-visible:ring-2 has-focus-visible:ring-ring
							{mode === 'proxy' ? 'border-primary bg-primary/5' : 'border-border/60'}
							{canEditBackend ? 'cursor-pointer hover:border-border' : 'cursor-not-allowed opacity-60'}"
					>
						<RadioGroup.Item value="proxy" id="portal-mode-proxy" class="sr-only" />
						<span class="pr-6 text-sm font-medium">Service Proxy</span>
						<span class="text-xs text-muted-foreground">
							Proxies a local service with access to portal edge-server(s) and PKI
						</span>
					</label>
					<Popover.Root>
						<Popover.Trigger
							class="absolute right-2 top-2 inline-flex h-6 w-6 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
							aria-label="More about service proxies"
						>
							<Info class="h-3.5 w-3.5" />
						</Popover.Trigger>
						<Popover.Content side="top" align="end" class="w-72 p-3 text-xs text-muted-foreground">
							Registry services will not be accessible through this portal.
						</Popover.Content>
					</Popover.Root>
				</div>
			</RadioGroup.Root>
			{#if !canEditBackend}
				<p class="text-[13px] text-muted-foreground mt-2">
					Changing the mode requires instance admin rights.
				</p>
			{/if}
		</FormCard>

		<FormCard title="Address" description="Where the portal answers.">
			<div class="grid grid-cols-1 sm:grid-cols-[1fr_9rem] gap-3">
				<FormField
					label="Hostname"
					id="portal-hostname"
					help="Empty matches any hostname"
					error={addressError && !addressError.startsWith('Port') ? addressError : ''}
				>
					<Input
						id="portal-hostname"
						bind:value={hostname}
						class="font-mono"
						placeholder={isProxy ? 'app.example.com' : 'registry.example.com'}
						autocomplete="off"
					/>
				</FormField>
				<FormField
					label="Port"
					id="portal-port"
					help="Empty uses the app port"
					error={addressError.startsWith('Port') ? addressError : ''}
				>
					<Input
						id="portal-port"
						bind:value={portText}
						class="font-mono"
						inputmode="numeric"
						placeholder={mainPort ? String(mainPort) : 'app port'}
					/>
				</FormField>
			</div>
		</FormCard>

		{#if isProxy}
			<FormCard title="Backend" description="Where every request on this address goes.">
				<div class="space-y-3">
					<FormField
						label="Backend URL"
						id="portal-backend"
						required
						help="A service reachable from this server"
						error={backendError}
					>
						<Input
							id="portal-backend"
							bind:value={backendUrl}
							class="font-mono max-w-sm"
							placeholder="http://127.0.0.1:3000"
							autocomplete="off"
							disabled={!canEditBackend}
						/>
					</FormField>
					<FormField label="Rewrite Host header" horizontal help="Send the backend's own hostname instead of the client's">
						<Switch bind:checked={backendRewriteHost} disabled={!canEditBackend} />
					</FormField>
					{#if httpsBackend}
						<FormField label="Skip certificate verification" horizontal help="Accept the backend's certificate without verifying it">
							<Switch bind:checked={backendInsecure} disabled={!canEditBackend} />
						</FormField>
					{/if}
					<p class="text-[13px] text-muted-foreground">
						Requests forward unchanged with standard X-Forwarded headers. HTTPS still terminates
						at this portal with the certificate configured below.
					</p>
				</div>
			</FormCard>
		{/if}

		<FormCard title="HTTPS" description="Certificate source for this portal.">
			<div class="space-y-3">
				{#if portal && liveState}
					<div class="flex items-center gap-2 flex-wrap">
						<Badge variant="outline" class="text-xs {liveState.cls}">{liveState.label}</Badge>
						<span class="text-xs text-muted-foreground">
							{portal.certDetail || 'Serving a valid certificate.'}
						</span>
					</div>
				{/if}
				<FormField label="Certificate source" id="portal-cert-source">
					<Select.Root
						type="single"
						value={String(certSource)}
						onValueChange={(v) => (certSource = Number(v) as CertSource)}
					>
						<Select.Trigger id="portal-cert-source" class="w-64">
							{certSourceLabels[certSource]}
						</Select.Trigger>
						<Select.Content>
							{#each sourceOptions as option (option)}
								<Select.Item value={String(option)} label={certSourceLabels[option]} />
							{/each}
						</Select.Content>
					</Select.Root>
				</FormField>

				{#if certSource !== CertSource.NONE}
					<FormField label="Require HTTPS" horizontal help="Redirects cleartext requests to HTTPS">
						<Switch bind:checked={tls} />
					</FormField>
					<FormField label="Client certificates (mTLS)" id="portal-mtls">
						<Select.Root type="single" value={String(mtlsMode)} onValueChange={(v) => (mtlsMode = Number(v) as MTLSMode)}>
							<Select.Trigger id="portal-mtls" class="w-64">{mtlsLabels[mtlsMode]}</Select.Trigger>
							<Select.Content>
								{#each mtlsOptions as option (option.value)}
									<Select.Item value={String(option.value)} label={option.label} />
								{/each}
							</Select.Content>
						</Select.Root>
						{#if mtlsMode === MTLSMode.MTLS_MODE_REQUIRED}
							<p class="text-[13px] text-muted-foreground">
								Only clients with a certificate issued by the organization CA can reach this portal.
							</p>
						{/if}
					</FormField>
				{/if}

				{#if certSource === CertSource.ACME}
					{#if canProvision}
						<FormField label="Provision now" horizontal>
							<Switch bind:checked={provisionCert} />
						</FormField>
					{/if}
					<div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
						<FormField label="Directory URL" id="portal-acme-dir" help="Empty inherits the org value">
							<Input id="portal-acme-dir" bind:value={acmeDirectory} class="font-mono text-xs" placeholder={acmeDirInherited || 'inherited'} autocomplete="off" />
						</FormField>
						<FormField label="Account email" id="portal-acme-email" help="Empty inherits the org value">
							<Input id="portal-acme-email" bind:value={acmeEmail} placeholder={acmeEmailInherited || 'inherited'} autocomplete="off" />
						</FormField>
					</div>
					<p class="text-[13px] text-muted-foreground">
						Issuance needs the hostname reachable by the CA. For certificates issued by this
						instance's own CA, use the Organization CA source instead.
					</p>
				{:else if certSource === CertSource.ORG_CA}
					{#if orgCA}
						<p class="text-[13px] text-muted-foreground">
							Certificates mint from <span class="font-medium">{orgCA.subject}</span>, clients must
							trust its chain.
						</p>
					{:else}
						<p class="text-[13px] text-amber-600 dark:text-amber-400">
							No signing CA yet, add one on the Certificates page.
						</p>
					{/if}
				{:else if certSource === CertSource.ORG_CERT}
					{#if orgCert}
						<p class="text-[13px] text-muted-foreground">
							Serves <span class="font-medium">{orgCert.subject}</span>, its SANs must cover this
							hostname.
						</p>
					{:else}
						<p class="text-[13px] text-amber-600 dark:text-amber-400">
							No shared certificate yet, upload one on the Certificates page.
						</p>
					{/if}
				{:else if certSource === CertSource.MANUAL}
					{#if portalCert}
						<p class="text-[13px] text-muted-foreground">
							Uploaded <span class="font-medium">{portalCert.subject || 'certificate'}</span>,
							expires {certDate(portalCert)}. Paste a new pair to replace it.
						</p>
					{/if}
					<div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
						<FormField label="Certificate (PEM)" id="portal-cert-pem" required={!portalCert} help="Full chain, leaf first">
							<Textarea id="portal-cert-pem" bind:value={certPem} class="font-mono text-xs" rows={5} placeholder="-----BEGIN CERTIFICATE-----" />
						</FormField>
						<FormField label="Private key (PEM)" id="portal-key-pem" required={!portalCert} help="Stored server side, never shown">
							<Textarea id="portal-key-pem" bind:value={keyPem} class="font-mono text-xs" rows={5} placeholder="-----BEGIN PRIVATE KEY-----" autocomplete="new-password" data-1p-ignore data-lpignore="true" data-bwignore />
						</FormField>
					</div>
					{#if manualNeedsPems}
						<p class="text-[13px] text-amber-600 dark:text-amber-400">
							Paste a certificate and key or handshakes fail.
						</p>
					{/if}
				{/if}
			</div>
		</FormCard>

		{#if !isProxy}
			<FormCard title="Access" description="What clients on this address can do.">
				<div class="space-y-3">
					<FormField label="Allow push" horizontal help="Off makes the portal pull only">
						<Switch bind:checked={allowPush} />
					</FormField>

					<FormField label="Require sign-in" horizontal help="On refuses anonymous pulls">
						<Switch bind:checked={requireAuth} />
					</FormField>

					<FormField label="Exit link" horizontal help="Off hides the link to the primary UI">
						<Switch bind:checked={showExitLink} />
					</FormField>
				</div>
			</FormCard>

			<FormCard title="Image names" description="How requested names map into {orgName}.">
				<div class="space-y-3">
					<FormField
						label="Map bare names"
						horizontal
						help="Resolves {previewAddress || 'portal-host'}/myimage to {orgName}/myimage"
					>
						<Switch bind:checked={mapUnqualified} />
					</FormField>

					<FormField label="Rewrite rules" help="Regex rewrites before bare name mapping">
						<div class="space-y-2">
							{#each rules as rule, i (i)}
								<div class="flex items-center gap-2">
									<Input
										bind:value={rule.pattern}
										class="font-mono text-xs"
										placeholder="legacy/(.+)"
										aria-label="Rule pattern"
									/>
									<span class="text-xs text-muted-foreground shrink-0">&rarr;</span>
									<Input
										bind:value={rule.replace}
										class="font-mono text-xs"
										placeholder="{orgName}/$1"
										aria-label="Rule replacement"
									/>
									<Button
										variant="ghost"
										size="icon"
										class="h-8 w-8 shrink-0 text-destructive hover:text-destructive"
										onclick={() => (rules = rules.filter((_, idx) => idx !== i))}
									>
										<X class="h-3.5 w-3.5" />
									</Button>
								</div>
							{/each}
							<Button variant="outline" size="sm" onclick={() => (rules = [...rules, { pattern: '', replace: '' }])}>
								<Plus class="h-3.5 w-3.5 mr-1.5" />Add Rule
							</Button>
						</div>
					</FormField>
				</div>
			</FormCard>
		{/if}

		<div class="flex items-center justify-end gap-3 pt-1">
			{#if submitAct.error}
				<p class="text-[13px] text-destructive mr-auto">{submitAct.error}</p>
			{/if}
			<Button variant="outline" onclick={goBack}>Cancel</Button>
			<Button onclick={submit} disabled={submitAct.busy || !formValid || manualNeedsPems}>
				{#if portal}
					{submitAct.busy ? 'Saving...' : 'Save Changes'}
				{:else}
					{submitAct.busy ? 'Creating...' : 'Create Portal'}
				{/if}
			</Button>
		</div>
	</div>

	<aside class="lg:sticky lg:top-20 rounded-xl border border-border/60 bg-card overflow-hidden">
		<div class="px-5 py-3.5 border-b border-border/40 bg-muted/20">
			<h3 class="text-sm font-semibold">This portal serves</h3>
		</div>
		<div class="p-5 space-y-4">
			{#if previewAddress}
				<div>
					<p class="detail-label mb-1">Address</p>
					<div class="flex items-center gap-1 min-w-0">
						<span class="font-mono text-sm font-medium truncate">{previewAddress}</span>
						<CopyButton text={previewAddress} label="Address copied" />
					</div>
					{#if portText.trim() === ''}
						<p class="text-xs text-muted-foreground/70 mt-1">
							On the app port{mainPort ? ` (${mainPort})` : ''}.
						</p>
					{:else if hostname.trim() === ''}
						<p class="text-xs text-muted-foreground/70 mt-1">Any hostname reaching port {portText}.</p>
					{/if}
				</div>

				<div class="space-y-2.5 text-[13px]">
					{#if isProxy}
						<div>
							<p class="detail-label mb-0.5">Every request forwards to</p>
							{#if backendUrl.trim() !== '' && backendError === ''}
								<p class="font-mono break-all">{backendUrl.trim()}</p>
							{:else}
								<p class="text-muted-foreground">Set a backend URL.</p>
							{/if}
						</div>
					{:else}
						<div>
							<p class="detail-label mb-0.5">Web UI</p>
							<p class="font-mono break-all">{scheme}://{previewAddress}</p>
						</div>
						<div>
							<p class="detail-label mb-0.5">Pull</p>
							<p class="font-mono break-all">docker pull {previewAddress}/{previewImage}</p>
						</div>
						{#if allowPush}
							<div>
								<p class="detail-label mb-0.5">Push</p>
								<p class="font-mono break-all">docker push {previewAddress}/{previewImage}</p>
							</div>
						{/if}
					{/if}
				</div>
			{:else}
				<p class="text-[13px] text-muted-foreground">
					Set a hostname or port to see the resulting address.
				</p>
			{/if}

			<div class="flex flex-wrap gap-1 pt-1 border-t border-border/40">
				{#if isProxy}
					<Badge variant="outline" class="text-xs font-normal mt-2">Service proxy</Badge>
					{#if httpsOn}
						<Badge variant="outline" class="text-xs font-normal mt-2">HTTPS</Badge>
					{/if}
					{#if backendRewriteHost}
						<Badge variant="outline" class="text-xs font-normal mt-2">Host rewritten</Badge>
					{/if}
					{#if httpsBackend && backendInsecure}
						<Badge variant="outline" class="text-xs font-normal mt-2 text-amber-600 dark:text-amber-400">Backend TLS unverified</Badge>
					{/if}
				{:else}
					<Badge variant="outline" class="text-xs font-normal mt-2">Scoped to {orgName}</Badge>
					{#if httpsOn}
						<Badge variant="outline" class="text-xs font-normal mt-2">HTTPS</Badge>
					{/if}
					<Badge variant="outline" class="text-xs font-normal mt-2">{allowPush ? 'Push enabled' : 'Pull only'}</Badge>
					{#if requireAuth}
						<Badge variant="outline" class="text-xs font-normal mt-2">Sign-in required</Badge>
					{/if}
					{#if mapUnqualified}
						<Badge variant="outline" class="text-xs font-normal mt-2">Bare names</Badge>
					{/if}
					{#if ruleCount > 0}
						<Badge variant="outline" class="text-xs font-normal mt-2">{ruleCount} rewrite{ruleCount !== 1 ? 's' : ''}</Badge>
					{/if}
				{/if}
			</div>
		</div>
	</aside>
</div>
