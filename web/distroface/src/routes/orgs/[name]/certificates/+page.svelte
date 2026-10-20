<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount, getContext } from 'svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Alert, AlertDescription } from '$lib/components/ui/alert';
	import {
		Table,
		TableBody,
		TableCell,
		TableHead,
		TableHeader,
		TableRow
	} from '$lib/components/ui/table';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import FormPage from '$lib/components/form-page.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import FormSection from '$lib/components/form-section.svelte';
	import CertMaterialRow from '$lib/components/cert-material-row.svelte';
	import CertUploadPanel from '$lib/components/cert-upload-panel.svelte';
	import DurationInput from '$lib/components/duration-input.svelte';
	import { Input } from '$lib/components/ui/input';
	import { Textarea } from '$lib/components/ui/textarea';
	import PemInput from '$lib/components/pem-input.svelte';
	import { Lock, RefreshCw, Loader, Globe, Pencil } from '$lib/icons';
	import { certDate, certSourceLabels, certStateBadge } from '$lib/cert-utils';
	import { effectiveAddress } from '$lib/portal-address';
	import { Act, errText } from '$lib/act.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { downloadBlob } from '$lib/download';
	import CopyButton from '$lib/components/copy-button.svelte';
	import { orgScope, patchSettings, systemScope } from '$lib/settings-utils';
	import {
		CertSource,
		CertState,
		TLSScope,
		type CertificateInfo,
		type TLSMaterialInfo
	} from '$lib/proto/distroface/v1/certificate_pb';
	import type { RegistryPortal } from '$lib/proto/distroface/v1/portal_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');
	const orgId = $derived(ctx.org?.id ?? '');

	let orgCA = $state<TLSMaterialInfo | null>(null);
	let orgCert = $state<TLSMaterialInfo | null>(null);
	let appCaExists = $state(false);
	let materialLoaded = $state(false);

	const caAct = new Act();
	const certAct = new Act();
	const dirAct = new Act();
	const emailAct = new Act();
	const renewAct = new Act();
	const signAct = new Act();

	let csrPem = $state('');
	let validityDays = $state(90);
	let signedPem = $state('');
	let signedCert = $state<CertificateInfo | null>(null);

	let uploadOpen = $state(false);
	let uploadScope = $state<TLSScope>(TLSScope.TLS_SCOPE_ORG);

	let removeCAOpen = $state(false);

	let orgAcmeEmail = $state('');
	let orgAcmeDir = $state('');
	let savedAcmeEmail = $state('');
	let savedAcmeDir = $state('');
	let acmeEmailInherited = $state('');
	let acmeDirInherited = $state('');
	let sysAcmeEnabled = $state(true);

	let portals = $state<RegistryPortal[]>([]);
	let loading = $state(true);
	let loaded = $state(false);
	let loadError = $state('');
	let busy = $state<string | null>(null);
	const pager = new Pager(20);
	const filter = new QueryFilter([
		{ key: 'name', label: 'Portal' },
		{ key: 'hostname', label: 'Hostname' }
	]);

	async function loadMaterial() {
		try {
			const resp = await rpcClient.certificate.getTLSMaterial({ orgId }, silentCallOptions);
			orgCA = resp.orgCa ?? null;
			orgCert = resp.orgCert ?? null;
			appCaExists = resp.appCaExists;
		} catch {
			// Rows refresh on the next action
		}
	}

	async function loadOrgSettings() {
		try {
			const [stored, sys] = await Promise.all([
				rpcClient.settings.getSettings({ scope: orgScope(orgId) }, silentCallOptions),
				rpcClient.settings.getEffectiveSettings({ scope: systemScope }, silentCallOptions)
			]);
			orgAcmeEmail = stored.settings?.acme?.email ?? '';
			orgAcmeDir = stored.settings?.acme?.directoryUrl ?? '';
			savedAcmeEmail = orgAcmeEmail;
			savedAcmeDir = orgAcmeDir;
			acmeEmailInherited = sys.settings?.acme?.email ?? '';
			acmeDirInherited = sys.settings?.acme?.directoryUrl ?? '';
			sysAcmeEnabled = sys.settings?.acme?.enabled ?? false;
		} catch {
			// Fields keep their placeholders
		}
	}

	$effect(() => {
		if (orgId && ctx.canAdmin && !materialLoaded) {
			materialLoaded = true;
			loadMaterial();
			loadOrgSettings();
		}
	});

	$effect(() => {
		if (!ctx.loading && ctx.org && !ctx.canAdmin) {
			goto(resolve('/orgs/[name]', { name: orgName }));
		}
	});

	// Overrides apply on blur, empty resets to the inherited value
	function commitOrgSetting(
		act: Act,
		path: string,
		field: 'email' | 'directoryUrl',
		value: string,
		saved: string,
		onDone: (v: string) => void
	) {
		if (value === saved) return;
		act.run(async () => {
			await patchSettings(orgScope(orgId), value ? { acme: { [field]: value } } : {}, [path]);
			onDone(value);
		});
	}

	const commitAcmeDir = () =>
		commitOrgSetting(
			dirAct,
			'acme.directory_url',
			'directoryUrl',
			orgAcmeDir.trim(),
			savedAcmeDir,
			(v) => (savedAcmeDir = v)
		);
	const commitAcmeEmail = () =>
		commitOrgSetting(
			emailAct,
			'acme.email',
			'email',
			orgAcmeEmail.trim(),
			savedAcmeEmail,
			(v) => (savedAcmeEmail = v)
		);

	// Org gets a leaf signed without its private key leaving the client
	function signCsr() {
		signAct.run(async () => {
			const resp = await rpcClient.certificate.signCSR(
				{ orgId, csrPem: csrPem.trim(), validityDays },
				silentCallOptions
			);
			signedPem = resp.certPem;
			signedCert = resp.cert ?? null;
		});
	}

	function downloadSigned() {
		if (signedPem) downloadBlob(signedPem, `${orgName}-signed.pem`);
	}

	function issueICA() {
		caAct.run(async () => {
			const resp = await rpcClient.certificate.issueOrgICA({ orgId }, silentCallOptions);
			orgCA = resp.orgCa ?? null;
		});
	}

	function generateCA() {
		caAct.run(async () => {
			const resp = await rpcClient.certificate.generateOrgCA({ orgId }, silentCallOptions);
			orgCA = resp.orgCa ?? null;
		});
	}

	function downloadCA() {
		if (orgCA?.certPem) downloadBlob(orgCA.certPem, `${orgName}-ca.pem`);
	}

	async function confirmRemoveCA() {
		const ok = await caAct.run(async () => {
			await rpcClient.certificate.deleteTLSCertificate(
				{ scope: TLSScope.TLS_SCOPE_ORG_CA, orgId },
				silentCallOptions
			);
			orgCA = null;
		});
		if (ok) removeCAOpen = false;
	}

	function removeOrgCert() {
		certAct.run(async () => {
			await rpcClient.certificate.deleteTLSCertificate(
				{ scope: TLSScope.TLS_SCOPE_ORG, orgId },
				silentCallOptions
			);
			orgCert = null;
		});
	}

	function openUpload(scope: TLSScope) {
		uploadScope = scope;
		uploadOpen = true;
	}

	async function submitUpload(certPem: string, keyPem: string) {
		await rpcClient.certificate.uploadTLSCertificate(
			{ scope: uploadScope, orgId, certPem, keyPem },
			silentCallOptions
		);
		await loadMaterial();
	}

	async function load() {
		loading = true;
		loadError = '';
		try {
			const resp = await rpcClient.portal.listPortals(
				{
					page: pager.request(filter.request()),
					orgId
				},
				silentCallOptions
			);
			portals = resp.portals;
			pager.apply(resp.page);
		} catch (err) {
			loadError = errText(err);
		} finally {
			loading = false;
			loaded = true;
		}
	}

	async function renew(portal: RegistryPortal) {
		busy = portal.id;
		await renewAct.run(() =>
			rpcClient.certificate.issueCertificate(
				{ target: { case: 'portalId', value: portal.id } },
				silentCallOptions
			)
		);
		busy = null;
		await load();
	}

	function editPortal(portal: RegistryPortal) {
		goto(resolve('/orgs/[name]/portals/[id]', { name: orgName, id: portal.id }));
	}

	function filterChanged() {
		pager.reset();
		load();
	}

	onMount(load);
</script>

<FormPage
	level="section"
	title="Certificates"
	description="PKI material and HTTPS state for this organization's portals."
>
	<FormSection
		title="Organization PKI"
		description="CA and certificate this org's portals can serve."
	>
		<CertMaterialRow
			title="Signing CA"
			empty="Issue, generate, or upload a CA that mints portal certificates."
			material={orgCA}
			busy={caAct.busy}
			error={caAct.error}
			issueLabel="Issue from instance CA"
			onIssue={appCaExists ? issueICA : undefined}
			onGenerate={generateCA}
			onUpload={() => openUpload(TLSScope.TLS_SCOPE_ORG_CA)}
			onDownload={orgCA ? downloadCA : undefined}
			onRemove={() => (removeCAOpen = true)}
		/>
		{#if uploadScope === TLSScope.TLS_SCOPE_ORG_CA}
			<CertUploadPanel
				bind:open={uploadOpen}
				title="Upload signing CA"
				description="CA pair that mints portal certificates."
				onSubmit={submitUpload}
			/>
		{/if}
		{#if orgCA?.orphaned}
			<p class="text-sm text-warning">
				This intermediate no longer chains to the instance root (the root was rotated or
				reimported). Re-issue it from the instance CA so its certificates validate again.
			</p>
		{/if}
		<CertMaterialRow
			title="Shared certificate"
			empty="Upload a certificate portals can serve directly."
			material={orgCert}
			busy={certAct.busy}
			error={certAct.error}
			onUpload={() => openUpload(TLSScope.TLS_SCOPE_ORG)}
			onRemove={removeOrgCert}
		/>
		{#if uploadScope === TLSScope.TLS_SCOPE_ORG}
			<CertUploadPanel
				bind:open={uploadOpen}
				title="Upload shared certificate"
				description="PEM pair covering this org's portal hostnames."
				onSubmit={submitUpload}
			/>
		{/if}
	</FormSection>

	<FormSection
		title="Sign a CSR"
		description="Sign a certificate request with this org's CA. Your private key never leaves your machine."
	>
		{#if !orgCA}
			<p class="text-sm text-warning">Add a signing CA above before signing requests.</p>
		{:else}
			<FormField label="Certificate request" id="csr-pem" tag={signAct.tag} error={signAct.error}>
				<PemInput
					id="csr-pem"
					bind:value={csrPem}
					rows={5}
					placeholder="-----BEGIN CERTIFICATE REQUEST-----"
					disabled={signAct.busy}
				/>
			</FormField>
			<FormField label="Validity" id="csr-days" span="third">
				<DurationInput
					id="csr-days"
					unit="days"
					min={1}
					bind:value={validityDays}
					disabled={signAct.busy}
					class="w-full"
				/>
			</FormField>
			{#if signedPem}
				<div class="space-y-2 rounded-lg border p-3">
					<div class="flex flex-wrap items-center justify-between gap-2">
						<div class="text-xs text-muted-foreground">
							{#if signedCert?.sans?.length}<span class="font-mono"
									>{signedCert.sans.join(', ')}</span
								>{/if}
							{#if signedCert?.notAfter}<span class="ml-2">expires {certDate(signedCert)}</span
								>{/if}
						</div>
						<div class="flex shrink-0 gap-1">
							<CopyButton text={signedPem} />
							<Button variant="ghost" size="sm" onclick={downloadSigned}>Download</Button>
						</div>
					</div>
					<Textarea class="font-mono" rows={5} readonly value={signedPem} />
				</div>
			{/if}
		{/if}
		{#snippet footer()}
			{#if orgCA}
				<Button variant="outline" disabled={signAct.busy || !csrPem.trim()} onclick={signCsr}>
					{#if signAct.busy}
						<Loader class="animate-spin" />
						Signing
					{:else}
						Sign request
					{/if}
				</Button>
			{/if}
		{/snippet}
	</FormSection>

	<FormSection
		title="ACME defaults"
		description={sysAcmeEnabled
			? "Overrides for this org's ACME portals, applied on blur."
			: 'ACME is turned off at the instance level, these values apply once an admin enables it.'}
	>
		<FormField
			label="Directory URL"
			id="org-acme-dir"
			help="Empty inherits the instance value"
			tag={dirAct.tag}
			error={dirAct.error}
			span="half"
		>
			<Input
				id="org-acme-dir"
				bind:value={orgAcmeDir}
				class="font-mono"
				placeholder={acmeDirInherited || 'inherited'}
				disabled={dirAct.busy}
				onblur={commitAcmeDir}
				onkeydown={(e) => {
					if (e.key === 'Enter') commitAcmeDir();
				}}
			/>
		</FormField>
		<FormField
			label="Account email"
			id="org-acme-email"
			help="Empty inherits the instance value"
			tag={emailAct.tag}
			error={emailAct.error}
			span="half"
		>
			<Input
				id="org-acme-email"
				bind:value={orgAcmeEmail}
				placeholder={acmeEmailInherited || 'inherited'}
				disabled={emailAct.busy}
				onblur={commitAcmeEmail}
				onkeydown={(e) => {
					if (e.key === 'Enter') commitAcmeEmail();
				}}
			/>
		</FormField>
	</FormSection>

	<div class="space-y-4">
		<div class="section-header">
			<div class="min-w-0">
				<h3 class="text-base leading-6 font-semibold">Portal HTTPS</h3>
				<p class="mt-1 text-sm text-muted-foreground">
					Certificate state per portal, set in the portal editor.
				</p>
			</div>
			<div class="w-full sm:max-w-md sm:flex-1">
				<QueryFilterBar {filter} placeholder="Search portals" onchange={filterChanged} />
			</div>
		</div>

		{#if !loaded || ctx.loading}
			<div class="box divide-y">
				{#each { length: 2 }, i (i)}
					<div class="flex items-center gap-4 px-4 py-3">
						<Skeleton class="h-4 w-32" />
						<Skeleton class="h-4 w-48" />
						<Skeleton class="h-5 w-16 rounded-full" />
					</div>
				{/each}
			</div>
		{:else if loadError}
			<Alert variant="destructive">
				<AlertDescription>
					<p>{loadError}</p>
					<Button variant="outline" size="sm" onclick={load}>Retry</Button>
				</AlertDescription>
			</Alert>
		{:else if portals.length === 0}
			<EmptyState
				message={filter.active ? 'No matching portals' : 'No portals yet'}
				description={filter.active
					? 'Try a different search.'
					: 'Create a portal and pick its certificate source there.'}
				icon={Lock}
			>
				{#snippet actions()}
					{#if !filter.active}
						<Button
							variant="outline"
							onclick={() => goto(resolve('/orgs/[name]/portals', { name: orgName }))}
						>
							<Globe />
							Go to portals
						</Button>
					{/if}
				{/snippet}
			</EmptyState>
		{:else}
			<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
				<Table>
					<TableHeader>
						<TableRow>
							<TableHead>Portal</TableHead>
							<TableHead>Address</TableHead>
							<TableHead>Source</TableHead>
							<TableHead>Certificate</TableHead>
							<TableHead class="w-24"></TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{#each portals as portal (portal.id)}
							{@const badge = certStateBadge(portal.certState)}
							<TableRow>
								<TableCell>
									<span class="text-sm font-medium {portal.enabled ? '' : 'text-muted-foreground'}"
										>{portal.name}</span
									>
									{#if !portal.enabled}
										<Badge variant="outline" class="ml-1.5">Stopped</Badge>
									{/if}
								</TableCell>
								<TableCell>
									<span class="font-mono text-sm"
										>{effectiveAddress(portal.hostname, portal.port)}</span
									>
								</TableCell>
								<TableCell>
									<span
										class="text-sm {portal.certSource === CertSource.NONE
											? 'text-muted-foreground'
											: ''}"
									>
										{certSourceLabels[portal.certSource] ?? certSourceLabels[CertSource.NONE]}
									</span>
								</TableCell>
								<TableCell>
									{#if badge}
										<div class="flex flex-wrap items-center gap-2">
											<Badge variant={badge.variant}>{badge.label}</Badge>
											{#if portal.certDetail}
												<span class="text-xs text-muted-foreground">{portal.certDetail}</span>
											{/if}
										</div>
									{:else}
										<span class="text-sm text-muted-foreground">Cleartext</span>
									{/if}
								</TableCell>
								<TableCell class="text-right">
									<div class="flex items-center justify-end gap-1">
										{#if portal.certSource === CertSource.ACME && portal.hostname !== ''}
											<Button
												variant="ghost-muted"
												size="icon-sm"
												title={portal.certState === CertState.READY
													? 'Renew certificate'
													: 'Issue certificate'}
												aria-label={portal.certState === CertState.READY
													? 'Renew certificate'
													: 'Issue certificate'}
												disabled={busy !== null}
												onclick={() => renew(portal)}
											>
												{#if busy === portal.id}
													<Loader class="animate-spin" />
												{:else}
													<RefreshCw />
												{/if}
											</Button>
										{/if}
										<Button
											variant="ghost-muted"
											size="icon-sm"
											title="Edit portal"
											aria-label="Edit portal"
											onclick={() => editPortal(portal)}
										>
											<Pencil />
										</Button>
									</div>
								</TableCell>
							</TableRow>
						{/each}
					</TableBody>
				</Table>
				<DataPagination attached {pager} onChange={load} />
			</div>

			{#if renewAct.error}
				<p class="text-sm text-destructive">{renewAct.error}</p>
			{/if}
		{/if}
	</div>
</FormPage>

<ConfirmDialog
	bind:open={removeCAOpen}
	title="Remove signing CA"
	confirmLabel="Remove"
	onConfirm={confirmRemoveCA}
	loading={caAct.busy}
>
	{#snippet description()}
		Portals using the organization CA will fail handshakes until another source is set.
		{#if caAct.error}
			<span class="mt-2 block text-destructive">{caAct.error}</span>
		{/if}
	{/snippet}
</ConfirmDialog>
