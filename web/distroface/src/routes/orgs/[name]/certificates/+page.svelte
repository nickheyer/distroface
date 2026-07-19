<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount, getContext } from 'svelte';
	import { rpcClient, silentCallOptions } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import FormCard from '$lib/components/form-card.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import CertMaterialRow from '$lib/components/cert-material-row.svelte';
	import CertUploadPanel from '$lib/components/cert-upload-panel.svelte';
	import { Input } from '$lib/components/ui/input';
	import { Lock, RefreshCw, Loader2, Globe, Pencil } from '@lucide/svelte';
	import { certSourceLabels, certStateBadge } from '$lib/cert-utils';
	import { effectiveAddress } from '$lib/portal-address';
	import { Act, errText } from '$lib/act.svelte';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { downloadBlob } from '$lib/download';
	import { orgScope, patchSettings, systemScope } from '$lib/settings-utils';
	import { CertSource, CertState, TLSScope, type TLSMaterialInfo } from '$lib/proto/distroface/v1/certificate_pb';
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

	let uploadOpen = $state(false);
	let uploadScope = $state<TLSScope>(TLSScope.TLS_SCOPE_ORG);

	let removeCAOpen = $state(false);

	let orgAcmeEmail = $state('');
	let orgAcmeDir = $state('');
	let savedAcmeEmail = $state('');
	let savedAcmeDir = $state('');
	let acmeEmailInherited = $state('');
	let acmeDirInherited = $state('');

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
	function commitOrgSetting(act: Act, path: string, field: 'email' | 'directoryUrl', value: string, saved: string, onDone: (v: string) => void) {
		if (value === saved) return;
		act.run(async () => {
			await patchSettings(orgScope(orgId), value ? { acme: { [field]: value } } : {}, [path]);
			onDone(value);
		});
	}

	const commitAcmeDir = () =>
		commitOrgSetting(dirAct, 'acme.directory_url', 'directoryUrl', orgAcmeDir.trim(), savedAcmeDir, (v) => (savedAcmeDir = v));
	const commitAcmeEmail = () =>
		commitOrgSetting(emailAct, 'acme.email', 'email', orgAcmeEmail.trim(), savedAcmeEmail, (v) => (savedAcmeEmail = v));

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
			await rpcClient.certificate.deleteTLSCertificate({ scope: TLSScope.TLS_SCOPE_ORG_CA, orgId }, silentCallOptions);
			orgCA = null;
		});
		if (ok) removeCAOpen = false;
	}

	function removeOrgCert() {
		certAct.run(async () => {
			await rpcClient.certificate.deleteTLSCertificate({ scope: TLSScope.TLS_SCOPE_ORG, orgId }, silentCallOptions);
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
			const resp = await rpcClient.portal.listPortals({
				page: pager.request(filter.request()),
				orgId
			}, silentCallOptions);
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

<div class="space-y-6">
	<!-- Org PKI material -->
	<FormCard title="Organization PKI" description="CA and certificate this org's portals can serve.">
		<div class="space-y-3">
			<CertMaterialRow
				title="Signing CA"
				empty="Issue, generate, or upload a CA that mints portal certificates."
				material={orgCA}
				busy={caAct.busy}
				error={caAct.error}
				issueLabel="Issue from Instance CA"
				onIssue={appCaExists ? issueICA : undefined}
				onGenerate={generateCA}
				onUpload={() => openUpload(TLSScope.TLS_SCOPE_ORG_CA)}
				onDownload={orgCA ? downloadCA : undefined}
				onRemove={() => (removeCAOpen = true)}
			/>
			<CertMaterialRow
				title="Shared certificate"
				empty="Upload a certificate portals can serve directly."
				material={orgCert}
				busy={certAct.busy}
				error={certAct.error}
				onUpload={() => openUpload(TLSScope.TLS_SCOPE_ORG)}
				onRemove={removeOrgCert}
			/>
		</div>
	</FormCard>

	<!-- ACME defaults -->
	<FormCard title="ACME Defaults" description="Overrides for this org's ACME portals, applied live.">
		<div class="grid grid-cols-1 sm:grid-cols-2 gap-3">
			<FormField
				label="Directory URL"
				id="org-acme-dir"
				help="Empty inherits the instance value."
				tag={dirAct.tag}
				error={dirAct.error}
			>
				<Input
					id="org-acme-dir"
					bind:value={orgAcmeDir}
					class="font-mono text-xs"
					placeholder={acmeDirInherited || 'inherited'}
					disabled={dirAct.busy}
					onblur={commitAcmeDir}
					onkeydown={(e) => { if (e.key === 'Enter') commitAcmeDir(); }}
				/>
			</FormField>
			<FormField
				label="Account email"
				id="org-acme-email"
				help="Empty inherits the instance value."
				tag={emailAct.tag}
				error={emailAct.error}
			>
				<Input
					id="org-acme-email"
					bind:value={orgAcmeEmail}
					placeholder={acmeEmailInherited || 'inherited'}
					disabled={emailAct.busy}
					onblur={commitAcmeEmail}
					onkeydown={(e) => { if (e.key === 'Enter') commitAcmeEmail(); }}
				/>
			</FormField>
		</div>
	</FormCard>

	<!-- Portal HTTPS health -->
	<div class="space-y-4">
		<div class="section-header">
			<div class="min-w-0 space-y-1">
				<h2 class="section-title">Portal HTTPS</h2>
				<p class="section-subtitle max-w-2xl">
					Certificate state per portal, set in the portal editor.
				</p>
			</div>
		</div>

		<div class="max-w-md">
			<QueryFilterBar {filter} placeholder="Search portals..." onchange={filterChanged} />
		</div>

		{#if !loaded || ctx.loading}
			<div class="space-y-2">
				{#each { length: 2 }, i (i)}
					<Skeleton class="h-14 w-full rounded-xl" />
				{/each}
			</div>
		{:else if loadError}
			<div class="rounded-xl border border-destructive/40 bg-destructive/5 px-6 py-8 text-center space-y-3">
				<p class="text-sm text-destructive">{loadError}</p>
				<Button variant="outline" size="sm" onclick={load}>Retry</Button>
			</div>
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
						<Button variant="outline" size="sm" onclick={() => goto(resolve('/orgs/[name]/portals', { name: orgName }))}>
							<Globe class="h-4 w-4 mr-1.5" />Go to Portals
						</Button>
					{/if}
				{/snippet}
			</EmptyState>
		{:else}
			<div class="data-table transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
				<Table>
					<TableHeader>
						<TableRow class="bg-muted/30 hover:bg-muted/30">
							<TableHead class="th">Portal</TableHead>
							<TableHead class="th">Address</TableHead>
							<TableHead class="th">Source</TableHead>
							<TableHead class="th">Certificate</TableHead>
							<TableHead class="th w-24"></TableHead>
						</TableRow>
					</TableHeader>
					<TableBody>
						{#each portals as portal (portal.id)}
							{@const badge = certStateBadge(portal.certState)}
							<TableRow>
								<TableCell class="py-3 px-3">
									<span class="font-medium text-sm {portal.enabled ? '' : 'text-muted-foreground'}">{portal.name}</span>
									{#if !portal.enabled}
										<Badge variant="outline" class="text-xs ml-1.5">stopped</Badge>
									{/if}
								</TableCell>
								<TableCell class="py-3 px-3">
									<span class="font-mono text-[13px]">{effectiveAddress(portal.hostname, portal.port)}</span>
								</TableCell>
								<TableCell class="py-3 px-3">
									<span class="text-sm {portal.certSource === CertSource.NONE ? 'text-muted-foreground' : ''}">
										{certSourceLabels[portal.certSource] ?? certSourceLabels[CertSource.NONE]}
									</span>
								</TableCell>
								<TableCell class="py-3 px-3">
									{#if badge}
										<div class="flex items-center gap-2 flex-wrap">
											<Badge variant="outline" class="text-xs {badge.cls}">{badge.label}</Badge>
											{#if portal.certDetail}
												<span class="text-xs text-muted-foreground">{portal.certDetail}</span>
											{/if}
										</div>
									{:else}
										<span class="text-sm text-muted-foreground">Cleartext</span>
									{/if}
								</TableCell>
								<TableCell class="text-right py-3 px-3">
									<div class="flex gap-1 justify-end items-center">
										{#if portal.certSource === CertSource.ACME && portal.hostname !== ''}
											<Button
												variant="ghost"
												size="icon"
												class="h-7 w-7"
												title={portal.certState === CertState.READY ? 'Renew certificate' : 'Issue certificate'}
												disabled={busy !== null}
												onclick={() => renew(portal)}
											>
												{#if busy === portal.id}
													<Loader2 class="h-3.5 w-3.5 animate-spin" />
												{:else}
													<RefreshCw class="h-3.5 w-3.5" />
												{/if}
											</Button>
										{/if}
										<Button
											variant="ghost"
											size="icon"
											class="h-7 w-7"
											title="Edit portal"
											onclick={() => editPortal(portal)}
										>
											<Pencil class="h-3.5 w-3.5" />
										</Button>
									</div>
								</TableCell>
							</TableRow>
						{/each}
					</TableBody>
				</Table>
			</div>

			{#if renewAct.error}
				<p class="text-[13px] text-destructive">{renewAct.error}</p>
			{/if}

			<DataPagination
				page={pager.page} pageSize={pager.pageSize} totalCount={pager.totalCount}
				onPrev={() => { if (pager.prev()) load(); }}
				onNext={() => { if (pager.next()) load(); }}
			/>
		{/if}
	</div>
</div>

<CertUploadPanel
	bind:open={uploadOpen}
	title={uploadScope === TLSScope.TLS_SCOPE_ORG_CA ? 'Upload Signing CA' : 'Upload Shared Certificate'}
	description={uploadScope === TLSScope.TLS_SCOPE_ORG_CA
		? 'CA pair that mints portal certificates.'
		: 'PEM pair covering this org\'s portal hostnames.'}
	onSubmit={submitUpload}
/>

<ConfirmDialog bind:open={removeCAOpen} title="Remove Signing CA" confirmLabel="Remove" onConfirm={confirmRemoveCA} loading={caAct.busy}>
	{#snippet description()}
		Portals using the organization CA will fail handshakes until another source is set.
		{#if caAct.error}
			<span class="block mt-2 text-destructive">{caAct.error}</span>
		{/if}
	{/snippet}
</ConfirmDialog>
