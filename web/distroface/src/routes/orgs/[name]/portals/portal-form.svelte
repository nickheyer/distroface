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
	import * as Select from '$lib/components/ui/select';
	import FormField from '$lib/components/form-field.svelte';
	import FormCard from '$lib/components/form-card.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import { Plus, X } from '@lucide/svelte';
	import { Act } from '$lib/act.svelte';
	import { effectiveAddress, placementError, portalScheme } from '$lib/portal-address';
	import { certDate, certSourceLabels, certStateBadge, isIssuableHostname } from '$lib/cert-utils';
	import { acmeDirectoryURL, orgScope, patchSettings, portalScope } from '$lib/settings-utils';
	import { configStore } from '$lib/stores/config.svelte';
	import type { RegistryPortal } from '$lib/proto/distroface/v1/portal_pb';
	import { CertSource, TLSScope, type TLSMaterialInfo } from '$lib/proto/distroface/v1/certificate_pb';

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

	// Draft snapshot on mount, parent keys this component per portal
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
	let tls = $state(portal?.tls ?? false);
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
	let builtinAcme = $state(false);
	const builtinDirectory = $derived(acmeDirectoryURL(configStore.publicHostname));
	/* svelte-ignore state_referenced_locally */
	let rules = $state<RuleDraft[]>(
		portal?.rules.map((r) => ({ pattern: r.pattern, replace: r.replace })) ?? []
	);
	const submitAct = new Act();

	const sourceOptions = [
		CertSource.NONE, CertSource.ACME, CertSource.ORG_CA, CertSource.ORG_CERT, CertSource.MANUAL
	];

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
			}
			const eff = await rpcClient.settings.getEffectiveSettings({ scope: orgScope(orgId) }, silentCallOptions);
			acmeDirInherited = eff.settings?.acme?.directoryUrl ?? '';
			acmeEmailInherited = eff.settings?.acme?.email ?? '';
			builtinAcme = eff.settings?.acme?.internalEnabled ?? false;
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

	onMount(() => {
		loadMaterial();
		loadInherited();
	});

	function goBack() {
		goto(resolve('/orgs/[name]/portals', { name: orgName }));
	}

	const addressError = $derived(
		hostname.trim() === '' && portText.trim() === '' ? '' : placementError(hostname, portText)
	);
	const formValid = $derived(
		name.trim() !== '' &&
		(hostname.trim() !== '' || portText.trim() !== '') &&
		placementError(hostname, portText) === ''
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
		const common = {
			orgId,
			name: name.trim(),
			hostname: hostname.trim().toLowerCase(),
			port: Number(portText.trim()) || 0,
			mapUnqualified,
			allowPush,
			requireAuth,
			tls,
			certSource,
			rules: cleanedRules
		};
		let redirected = false;
		const ok = await submitAct.run(async () => {
			if (portal) {
				await rpcClient.portal.updatePortal({ ...common, id: portal.id, setRules: true }, silentCallOptions);
				await saveAcme(portal.id);
				if (certSource === CertSource.MANUAL && pemsFilled) {
					await uploadPortalCert(portal.id);
				}
				return;
			}
			const resp = await rpcClient.portal.createPortal(common, silentCallOptions);
			const created = resp.portal;
			if (!created) return;
			await saveAcme(created.id);
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
				<Input id="portal-name" bind:value={name} placeholder="e.g. public-mirror" class="max-w-sm" />
			</FormField>
		</FormCard>

		<FormCard title="Address" description="Where the portal answers.">
			<div class="grid grid-cols-1 sm:grid-cols-[1fr_9rem] gap-3">
				<FormField
					label="Hostname"
					id="portal-hostname"
					help="DNS name for this portal, empty matches any."
					error={addressError && !addressError.startsWith('Port') ? addressError : ''}
				>
					<Input
						id="portal-hostname"
						bind:value={hostname}
						class="font-mono"
						placeholder="registry.example.com"
					/>
				</FormField>
				<FormField
					label="Port"
					id="portal-port"
					help="Empty uses the app port{mainPort ? ` (${mainPort})` : ''}."
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
				<FormField
					label="Certificate source"
					id="portal-cert-source"
					help="Picks what certificate this portal serves."
				>
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
					<FormField label="Require HTTPS" horizontal help="Redirects cleartext requests to HTTPS.">
						<Switch bind:checked={tls} />
					</FormField>
				{/if}

				{#if certSource === CertSource.ACME}
					{#if canProvision}
						<FormField
							label="Provision now"
							horizontal
							help="Requests the certificate right after creation."
						>
							<Switch bind:checked={provisionCert} />
						</FormField>
					{/if}
					<div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
						<FormField label="Directory URL" id="portal-acme-dir" help="Empty inherits the organization value.">
							<Input id="portal-acme-dir" bind:value={acmeDirectory} class="font-mono text-xs" placeholder={acmeDirInherited || 'inherited'} />
						</FormField>
						<FormField label="Account email" id="portal-acme-email" help="Empty inherits the organization value.">
							<Input id="portal-acme-email" bind:value={acmeEmail} placeholder={acmeEmailInherited || 'inherited'} />
						</FormField>
					</div>
					{#if builtinAcme}
						<p class="text-[13px] text-muted-foreground">
							The built-in CA at <span class="font-mono">{builtinDirectory}</span> issues this portal's certificate, chaining to the instance root.
						</p>
					{:else}
						<p class="text-[13px] text-muted-foreground">
							Issuance needs the hostname reachable from the internet.
						</p>
					{/if}
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
						<FormField label="Certificate (PEM)" id="portal-cert-pem" required={!portalCert} help="Full chain, leaf first.">
							<Textarea id="portal-cert-pem" bind:value={certPem} class="font-mono text-xs" rows={5} placeholder="-----BEGIN CERTIFICATE-----" />
						</FormField>
						<FormField label="Private key (PEM)" id="portal-key-pem" required={!portalCert} help="Stored server side, never shown.">
							<Textarea id="portal-key-pem" bind:value={keyPem} class="font-mono text-xs" rows={5} placeholder="-----BEGIN PRIVATE KEY-----" />
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

		<FormCard title="Access" description="What clients on this address can do.">
			<div class="space-y-3">
				<FormField label="Allow push" horizontal help="Off makes the portal pull only.">
					<Switch bind:checked={allowPush} />
				</FormField>

				<FormField label="Require sign-in" horizontal help="On refuses anonymous pulls.">
					<Switch bind:checked={requireAuth} />
				</FormField>
			</div>
		</FormCard>

		<FormCard title="Image names" description="How requested names map into {orgName}.">
			<div class="space-y-3">
				<FormField
					label="Map bare names"
					horizontal
					help="Resolves {previewAddress || 'portal-host'}/myimage to {orgName}/myimage."
				>
					<Switch bind:checked={mapUnqualified} />
				</FormField>

				<FormField label="Rewrite rules" help="Regex rewrites applied before bare name mapping.">
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
				</div>
			{:else}
				<p class="text-[13px] text-muted-foreground">
					Set a hostname or port to see the resulting address.
				</p>
			{/if}

			<div class="flex flex-wrap gap-1 pt-1 border-t border-border/40">
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
			</div>
		</div>
	</aside>
</div>
