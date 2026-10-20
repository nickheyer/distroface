<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Input } from '$lib/components/ui/input';
	import { Switch } from '$lib/components/ui/switch';
	import PemInput from '$lib/components/pem-input.svelte';
	import * as Popover from '$lib/components/ui/popover';
	import * as RadioGroup from '$lib/components/ui/radio-group';
	import * as Select from '$lib/components/ui/select';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import FormActions from '$lib/components/form-actions.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import { Info, Plus, X, Loader2 } from '@lucide/svelte';
	import { Act } from '$lib/act.svelte';
	import { effectiveAddress, placementError, portalScheme } from '$lib/portal-address';
	import { certDate, certSourceLabels, certStateBadge, isIssuableHostname } from '$lib/cert-utils';
	import { orgScope, patchSettings, portalScope } from '$lib/settings-utils';
	import { authStore } from '$lib/stores/auth.svelte';
	import type { RegistryPortal } from '$lib/proto/distroface/v1/portal_pb';
	import {
		CertSource,
		TLSScope,
		type TLSMaterialInfo
	} from '$lib/proto/distroface/v1/certificate_pb';
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
	let mode = $derived<PortalMode>(portal?.backendUrl ? 'proxy' : 'registry');
	let name = $derived(portal?.name ?? '');
	let hostname = $derived(portal?.hostname ?? '');
	let portText = $derived(portal && portal.port > 0 ? String(portal.port) : '');
	let sshPortText = $derived(portal && portal.sshPort > 0 ? String(portal.sshPort) : '');
	let mapUnqualified = $derived(portal?.mapUnqualified ?? true);
	let allowPush = $derived(portal?.allowPush ?? true);
	let showExitLink = $derived(!(portal?.hidePrimaryLink ?? false));
	let tls = $derived(portal?.tls ?? false);
	let backendUrl = $derived(portal?.backendUrl ?? '');
	let backendRewriteHost = $derived(portal?.backendRewriteHost ?? false);
	let backendInsecure = $derived(portal?.backendInsecure ?? false);
	let certSource = $derived<CertSource>(portal?.certSource || CertSource.NONE);
	const rules = $derived.by(() => {
		const draft = $state<RuleDraft[]>(
			portal?.rules.map((r) => ({ pattern: r.pattern, replace: r.replace })) ?? []
		);
		return draft;
	});

	type AnonymousMode = 'inherit' | 'allow' | 'deny';
	let anonymousMode = $state<AnonymousMode>('inherit');
	let savedAnonymousMode = $state<AnonymousMode>('inherit');
	let orgAnonymous = $state(false);
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
	const submitAct = new Act();

	const sourceOptions = [
		CertSource.NONE,
		CertSource.ACME,
		CertSource.ORG_CA,
		CertSource.ORG_CERT,
		CertSource.MANUAL
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

	const anonymousOptions: { value: AnonymousMode; label: string }[] = [
		{ value: 'inherit', label: 'Inherit from organization' },
		{ value: 'allow', label: 'Allow' },
		{ value: 'deny', label: 'Deny' }
	];
	const anonymousLabel = $derived(
		anonymousMode === 'inherit'
			? `Inherit from organization (${orgAnonymous ? 'allowed' : 'denied'})`
			: anonymousMode === 'allow'
				? 'Allow'
				: 'Deny'
	);
	// What signed out visitors get once this form is saved
	const effectiveAnonymous = $derived(
		anonymousMode === 'inherit' ? orgAnonymous : anonymousMode === 'allow'
	);

	async function loadMaterial() {
		try {
			const resp = await rpcClient.certificate.getTLSMaterial(
				{
					orgId,
					portalId: portal?.id ?? ''
				},
				silentCallOptions
			);
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
				const stored = await rpcClient.settings.getSettings(
					{ scope: portalScope(portal.id) },
					silentCallOptions
				);
				acmeEmail = stored.settings?.acme?.email ?? '';
				acmeDirectory = stored.settings?.acme?.directoryUrl ?? '';
				savedAcmeEmail = acmeEmail;
				savedAcmeDir = acmeDirectory;
				mtlsMode = stored.settings?.tls?.mtlsMode ?? MTLSMode.MTLS_MODE_UNSPECIFIED;
				savedMtlsMode = mtlsMode;
				const anon = stored.settings?.auth?.anonymousAccess;
				anonymousMode = anon === undefined ? 'inherit' : anon ? 'allow' : 'deny';
				savedAnonymousMode = anonymousMode;
			}
			const eff = await rpcClient.settings.getEffectiveSettings(
				{ scope: orgScope(orgId) },
				silentCallOptions
			);
			acmeDirInherited = eff.settings?.acme?.directoryUrl ?? '';
			acmeEmailInherited = eff.settings?.acme?.email ?? '';
			orgAnonymous = eff.settings?.auth?.anonymousAccess ?? false;
		} catch {
			// Placeholders stay generic
		}
	}

	// Portal overrides live at the portal settings scope
	async function saveAcme(portalId: string) {
		const email = acmeEmail.trim();
		const dir = acmeDirectory.trim();
		if (email === savedAcmeEmail && dir === savedAcmeDir) return;
		await patchSettings(
			portalScope(portalId),
			{
				acme: { ...(email ? { email } : {}), ...(dir ? { directoryUrl: dir } : {}) }
			},
			['acme.email', 'acme.directory_url']
		);
		savedAcmeEmail = email;
		savedAcmeDir = dir;
	}

	// Portal anonymous override, inherit falls back to the org
	async function saveAnonymous(portalId: string) {
		if (anonymousMode === savedAnonymousMode) return;
		await patchSettings(
			portalScope(portalId),
			{
				auth: anonymousMode === 'inherit' ? {} : { anonymousAccess: anonymousMode === 'allow' }
			},
			['auth.anonymous_access']
		);
		savedAnonymousMode = anonymousMode;
	}

	// Portal mtls override, inherit clears it back to the instance policy
	async function saveMtls(portalId: string) {
		if (mtlsMode === savedMtlsMode) return;
		const inherit = mtlsMode === MTLSMode.MTLS_MODE_UNSPECIFIED;
		await patchSettings(
			portalScope(portalId),
			{
				tls: inherit ? {} : { mtlsMode }
			},
			['tls.mtls_mode']
		);
		savedMtlsMode = mtlsMode;
	}

	onMount(() => {
		loadMaterial();
		loadInherited();
	});

	const listHref = $derived(resolve('/orgs/[name]/portals', { name: orgName }));

	function goBack() {
		goto(resolve(listHref as `/${string}`));
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
		if (
			(u.protocol !== 'http:' && u.protocol !== 'https:') ||
			u.username !== '' ||
			u.search !== '' ||
			u.hash !== ''
		) {
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
	const canProvision = $derived(
		!portal && certSource === CertSource.ACME && isIssuableHostname(hostname)
	);
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
		const resp = await rpcClient.certificate.uploadTLSCertificate(
			{
				scope: TLSScope.TLS_SCOPE_PORTAL,
				orgId,
				portalId,
				certPem,
				keyPem
			},
			silentCallOptions
		);
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
			sshPort: Number(sshPortText.trim()) || 0,
			tls,
			certSource,
			...(isProxy
				? {
						mapUnqualified: false,
						allowPush: false,
						hidePrimaryLink: false,
						rules: [],
						backendUrl: backendUrl.trim(),
						backendRewriteHost,
						backendInsecure: httpsBackend && backendInsecure
					}
				: {
						mapUnqualified,
						allowPush,
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
				await rpcClient.portal.updatePortal(
					{ ...common, id: portal.id, setRules: true },
					silentCallOptions
				);
				await saveAcme(portal.id);
				await saveMtls(portal.id);
				await saveAnonymous(portal.id);
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
			await saveAnonymous(created.id);
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

<div class="box">
	<div class="box-header">This portal serves</div>
	<div class="grid gap-4 p-4 sm:grid-cols-2 lg:grid-cols-3">
		{#if previewAddress}
			<div class="min-w-0">
				<p class="eyebrow">Address</p>
				<div class="mt-1 flex min-w-0 items-center gap-1">
					<span class="truncate font-mono text-sm font-medium">{previewAddress}</span>
					<CopyButton text={previewAddress} label="Address copied" />
				</div>
				{#if portText.trim() === ''}
					<p class="mt-1 text-xs text-muted-foreground">
						On the app port{mainPort ? ` (${mainPort})` : ''}
					</p>
				{:else if hostname.trim() === ''}
					<p class="mt-1 text-xs text-muted-foreground">Any hostname reaching port {portText}</p>
				{/if}
			</div>
			<div class="min-w-0 space-y-3 text-sm">
				{#if isProxy}
					<div>
						<p class="eyebrow">Every request forwards to</p>
						{#if backendUrl.trim() !== '' && backendError === ''}
							<p class="mt-0.5 font-mono break-all">{backendUrl.trim()}</p>
						{:else}
							<p class="mt-0.5 text-muted-foreground">Set a backend URL</p>
						{/if}
					</div>
				{:else}
					<div>
						<p class="eyebrow">Web UI</p>
						<p class="mt-0.5 font-mono break-all">{scheme}://{previewAddress}</p>
					</div>
					<div>
						<p class="eyebrow">Pull</p>
						<p class="mt-0.5 font-mono break-all">docker pull {previewAddress}/{previewImage}</p>
					</div>
					{#if allowPush}
						<div>
							<p class="eyebrow">Push</p>
							<p class="mt-0.5 font-mono break-all">docker push {previewAddress}/{previewImage}</p>
						</div>
					{/if}
				{/if}
			</div>
		{:else}
			<p class="text-sm text-muted-foreground sm:col-span-2">
				Set a hostname or port to see the resulting address
			</p>
		{/if}
		<div class="flex flex-wrap content-start gap-1 sm:col-span-2 lg:col-span-1">
			{#if isProxy}
				<Badge variant="outline">Service proxy</Badge>
				{#if httpsOn}
					<Badge variant="outline">HTTPS</Badge>
				{/if}
				{#if backendRewriteHost}
					<Badge variant="outline">Host rewritten</Badge>
				{/if}
				{#if httpsBackend && backendInsecure}
					<Badge variant="warning">Backend TLS unverified</Badge>
				{/if}
			{:else}
				<Badge variant="outline">Scoped to {orgName}</Badge>
				{#if httpsOn}
					<Badge variant="outline">HTTPS</Badge>
				{/if}
				<Badge variant="outline">{allowPush ? 'Push enabled' : 'Pull only'}</Badge>
				{#if !effectiveAnonymous}
					<Badge variant="outline">Sign-in required</Badge>
				{/if}
				{#if mapUnqualified}
					<Badge variant="outline">Bare names</Badge>
				{/if}
				{#if ruleCount > 0}
					<Badge variant="outline">{ruleCount} rewrite{ruleCount !== 1 ? 's' : ''}</Badge>
				{/if}
			{/if}
		</div>
	</div>
</div>

<FormSection title="Portal" description="Label shown in the portal list.">
	<FormField label="Name" id="portal-name" required span="half">
		<Input id="portal-name" bind:value={name} placeholder="public-mirror" autocomplete="off" />
	</FormField>
</FormSection>

<FormSection
	title="Mode"
	description="What this portal serves. Everything below follows from this choice."
>
	<RadioGroup.Root
		value={mode}
		onValueChange={(v) => (mode = v as PortalMode)}
		disabled={!canEditBackend}
		class="grid gap-4 sm:grid-cols-2"
	>
		<label
			for="portal-mode-registry"
			class="flex flex-col gap-1 rounded-lg border p-3.5 transition-colors has-focus-visible:ring-2 has-focus-visible:ring-ring {mode ===
			'registry'
				? 'border-primary bg-primary/5'
				: 'hover:border-border-strong'} {canEditBackend
				? 'cursor-pointer'
				: 'cursor-not-allowed opacity-60'}"
		>
			<RadioGroup.Item value="registry" id="portal-mode-registry" class="sr-only" />
			<span class="text-sm font-medium">Org registry</span>
			<span class="text-xs text-muted-foreground">
				Provides a dedicated access point and web UI for organization resources
			</span>
		</label>
		<div class="relative">
			<label
				for="portal-mode-proxy"
				class="flex h-full flex-col gap-1 rounded-lg border p-3.5 transition-colors has-focus-visible:ring-2 has-focus-visible:ring-ring {mode ===
				'proxy'
					? 'border-primary bg-primary/5'
					: 'hover:border-border-strong'} {canEditBackend
					? 'cursor-pointer'
					: 'cursor-not-allowed opacity-60'}"
			>
				<RadioGroup.Item value="proxy" id="portal-mode-proxy" class="sr-only" />
				<span class="pr-6 text-sm font-medium">Service proxy</span>
				<span class="text-xs text-muted-foreground">
					Proxies a local service with access to portal edge servers and PKI
				</span>
			</label>
			<Popover.Root>
				<Popover.Trigger
					class="absolute top-2 right-2 inline-flex size-6 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-muted hover:text-foreground"
					aria-label="More about service proxies"
				>
					<Info class="size-3.5" />
				</Popover.Trigger>
				<Popover.Content side="top" align="end" class="w-72 p-3 text-xs text-muted-foreground">
					Registry services will not be accessible through this portal.
				</Popover.Content>
			</Popover.Root>
		</div>
	</RadioGroup.Root>
	{#if !canEditBackend}
		<p class="text-xs text-muted-foreground">Changing the mode requires instance admin rights</p>
	{/if}
</FormSection>

<FormSection title="Address" description="Where the portal answers.">
	<FormField
		label="Hostname"
		id="portal-hostname"
		help="Empty matches any hostname"
		error={addressError && !addressError.startsWith('Port') ? addressError : ''}
		span="two-thirds"
	>
		<Input
			id="portal-hostname"
			bind:value={hostname}
			class="font-mono"
			placeholder={isProxy ? 'app.example.com' : 'registry.example.com'}
			autocomplete="off"
			aria-invalid={!!addressError && !addressError.startsWith('Port')}
		/>
	</FormField>
	<FormField
		label="Port"
		id="portal-port"
		help="Empty uses the app port"
		error={addressError.startsWith('Port') ? addressError : ''}
		span="third"
	>
		<Input
			id="portal-port"
			bind:value={portText}
			class="font-mono"
			inputmode="numeric"
			placeholder={mainPort ? String(mainPort) : 'app port'}
			aria-invalid={addressError.startsWith('Port')}
		/>
	</FormField>
	<FormField
		label="Git SSH port"
		id="portal-ssh-port"
		help="Dedicated listener for git over ssh, unqualified names and this portal's rules apply there. Empty serves canonical paths on the primary ssh port"
		layout="inline"
	>
		<Input
			id="portal-ssh-port"
			bind:value={sshPortText}
			class="w-28 font-mono"
			inputmode="numeric"
			placeholder="none"
		/>
	</FormField>
</FormSection>

{#if isProxy}
	<FormSection title="Backend" description="Where every request on this address goes.">
		<FormField
			label="Backend URL"
			id="portal-backend"
			required
			help="A service reachable from this server"
			error={backendError}
			span="two-thirds"
		>
			<Input
				id="portal-backend"
				bind:value={backendUrl}
				class="font-mono"
				placeholder="http://127.0.0.1:3000"
				autocomplete="off"
				disabled={!canEditBackend}
				aria-invalid={!!backendError}
			/>
		</FormField>
		<FormField
			label="Rewrite Host header"
			layout="toggle"
			help="Send the backend's own hostname instead of the client's"
		>
			<Switch bind:checked={backendRewriteHost} disabled={!canEditBackend} />
		</FormField>
		{#if httpsBackend}
			<FormField
				label="Skip certificate verification"
				layout="toggle"
				help="Accept the backend's certificate without verifying it"
			>
				<Switch bind:checked={backendInsecure} disabled={!canEditBackend} />
			</FormField>
		{/if}
		<p class="text-sm text-muted-foreground">
			Requests forward unchanged with standard X-Forwarded headers. HTTPS still terminates at this
			portal with the certificate configured below.
		</p>
	</FormSection>
{/if}

<FormSection title="HTTPS" description="Certificate source for this portal.">
	{#if portal && liveState}
		<div class="flex flex-wrap items-center gap-2">
			<Badge variant={liveState.variant}>{liveState.label}</Badge>
			<span class="text-xs text-muted-foreground">
				{portal.certDetail || 'Serving a valid certificate.'}
			</span>
		</div>
	{/if}
	<FormField label="Certificate source" id="portal-cert-source" span="half">
		<Select.Root
			type="single"
			value={String(certSource)}
			onValueChange={(v) => (certSource = Number(v) as CertSource)}
		>
			<Select.Trigger id="portal-cert-source" class="w-full">
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
		<FormField
			label="Client certificates (mTLS)"
			id="portal-mtls"
			help={mtlsMode === MTLSMode.MTLS_MODE_REQUIRED
				? 'Only clients with a certificate issued by the organization CA can reach this portal'
				: undefined}
			span="half"
		>
			<Select.Root
				type="single"
				value={String(mtlsMode)}
				onValueChange={(v) => (mtlsMode = Number(v) as MTLSMode)}
			>
				<Select.Trigger id="portal-mtls" class="w-full">{mtlsLabels[mtlsMode]}</Select.Trigger>
				<Select.Content>
					{#each mtlsOptions as option (option.value)}
						<Select.Item value={String(option.value)} label={option.label} />
					{/each}
				</Select.Content>
			</Select.Root>
		</FormField>
		<FormField label="Require HTTPS" layout="toggle" help="Redirects cleartext requests to HTTPS">
			<Switch bind:checked={tls} />
		</FormField>
	{/if}

	{#if certSource === CertSource.ACME}
		{#if canProvision}
			<FormField label="Provision now" layout="toggle">
				<Switch bind:checked={provisionCert} />
			</FormField>
		{/if}
		<FormField
			label="Directory URL"
			id="portal-acme-dir"
			help="Empty inherits the org value"
			span="half"
		>
			<Input
				id="portal-acme-dir"
				bind:value={acmeDirectory}
				class="font-mono"
				placeholder={acmeDirInherited || 'inherited'}
				autocomplete="off"
			/>
		</FormField>
		<FormField
			label="Account email"
			id="portal-acme-email"
			help="Empty inherits the org value"
			span="half"
		>
			<Input
				id="portal-acme-email"
				bind:value={acmeEmail}
				placeholder={acmeEmailInherited || 'inherited'}
				autocomplete="off"
			/>
		</FormField>
		<p class="text-sm text-muted-foreground">
			Issuance needs the hostname reachable by the CA. For certificates issued by this instance's
			own CA, use the Organization CA source instead.
		</p>
	{:else if certSource === CertSource.ORG_CA}
		{#if orgCA}
			<p class="text-sm text-muted-foreground">
				Certificates mint from <span class="font-medium text-foreground">{orgCA.subject}</span>,
				clients must trust its chain.
			</p>
		{:else}
			<p class="text-sm text-warning">No signing CA yet, add one on the Certificates page.</p>
		{/if}
	{:else if certSource === CertSource.ORG_CERT}
		{#if orgCert}
			<p class="text-sm text-muted-foreground">
				Serves <span class="font-medium text-foreground">{orgCert.subject}</span>, its SANs must
				cover this hostname.
			</p>
		{:else}
			<p class="text-sm text-warning">
				No shared certificate yet, upload one on the Certificates page.
			</p>
		{/if}
	{:else if certSource === CertSource.MANUAL}
		{#if portalCert}
			<p class="text-sm text-muted-foreground">
				Uploaded <span class="font-medium text-foreground"
					>{portalCert.subject || 'certificate'}</span
				>, expires {certDate(portalCert)}. Paste or upload a new pair to replace it.
			</p>
		{/if}
		<FormField
			label="Certificate (PEM)"
			id="portal-cert-pem"
			required={!portalCert}
			help="Full chain, leaf first"
			span="half"
		>
			<PemInput
				id="portal-cert-pem"
				bind:value={certPem}
				rows={5}
				placeholder="-----BEGIN CERTIFICATE-----"
			/>
		</FormField>
		<FormField
			label="Private key (PEM)"
			id="portal-key-pem"
			required={!portalCert}
			help="Stored server side, never shown"
			span="half"
		>
			<PemInput
				id="portal-key-pem"
				bind:value={keyPem}
				rows={5}
				placeholder="-----BEGIN PRIVATE KEY-----"
				secret
			/>
		</FormField>
		{#if manualNeedsPems}
			<p class="text-sm text-warning">Add a certificate and key or handshakes fail.</p>
		{/if}
	{/if}
</FormSection>

{#if !isProxy}
	<FormSection title="Access" description="What clients on this address can do.">
		<FormField label="Allow push" layout="toggle" help="Off makes the portal pull only">
			<Switch bind:checked={allowPush} />
		</FormField>
		<FormField
			label="Anonymous access"
			id="portal-anonymous"
			help="Signed out visitors get the anonymous role grants, deny requires sign-in for every pull"
			span="half"
		>
			<Select.Root
				type="single"
				value={anonymousMode}
				onValueChange={(v) => (anonymousMode = v as AnonymousMode)}
			>
				<Select.Trigger id="portal-anonymous" class="w-full">{anonymousLabel}</Select.Trigger>
				<Select.Content>
					{#each anonymousOptions as option (option.value)}
						<Select.Item value={option.value} label={option.label} />
					{/each}
				</Select.Content>
			</Select.Root>
		</FormField>
		<FormField label="Exit link" layout="toggle" help="Off hides the link to the primary UI">
			<Switch bind:checked={showExitLink} />
		</FormField>
	</FormSection>

	<FormSection title="Image names" description="How requested names map into {orgName}.">
		<FormField
			label="Map bare names"
			layout="toggle"
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
							class="font-mono"
							placeholder="legacy/(.+)"
							aria-label="Rule pattern"
						/>
						<span class="shrink-0 text-xs text-muted-foreground">&rarr;</span>
						<Input
							bind:value={rule.replace}
							class="font-mono"
							placeholder="{orgName}/$1"
							aria-label="Rule replacement"
						/>
						<Button
							variant="ghost"
							size="icon"
							class="shrink-0 text-destructive hover:text-destructive"
							onclick={() => rules.splice(i, 1)}
							aria-label="Remove rule"
						>
							<X />
						</Button>
					</div>
				{/each}
				<Button
					variant="outline"
					size="sm"
					onclick={() => rules.push({ pattern: '', replace: '' })}
				>
					<Plus />
					Add rule
				</Button>
			</div>
		</FormField>
	</FormSection>
{/if}

<FormActions>
	<Button onclick={submit} disabled={submitAct.busy || !formValid || manualNeedsPems}>
		{#if submitAct.busy}
			<Loader2 class="animate-spin" />
		{/if}
		{#if portal}
			{submitAct.busy ? 'Saving' : 'Save changes'}
		{:else}
			{submitAct.busy ? 'Creating' : 'Create portal'}
		{/if}
	</Button>
	<Button variant="ghost-muted" href={listHref}>Cancel</Button>
	{#if submitAct.error}
		<p class="text-sm text-destructive">{submitAct.error}</p>
	{/if}
</FormActions>
