<script lang="ts">
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import { Input } from '$lib/components/ui/input';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import FormCard from '$lib/components/form-card.svelte';
	import FormPanel from '$lib/components/form-panel.svelte';
	import FormField from '$lib/components/form-field.svelte';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import BulkActionBar from '$lib/components/bulk-action-bar.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import { Lock, ShieldCheck, BadgeCheck, Plus, RefreshCw, Trash2, Loader2 } from '@lucide/svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { authStore } from '$lib/stores/auth.svelte';
	import { toast } from 'svelte-sonner';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { certHealth, certBadgeClass, certDate, isIssuableHostname } from '$lib/cert-utils';
	import type { GetTLSStatusResponse, CertificateDomain } from '$lib/proto/distroface/v1/certificate_pb';
	import type { BulkOperationError } from '$lib/proto/distroface/v1/types_pb';

	let status = $state<GetTLSStatusResponse | null>(null);
	let domains = $state<CertificateDomain[]>([]);
	let loading = $state(true);
	const pager = new Pager(20);
	const filter = new QueryFilter([{ key: 'domain', label: 'Domain' }]);

	const selected = new SvelteSet<string>();
	const pageIds = $derived(domains.map((d) => d.id));
	const allOnPageSelected = $derived(pageIds.length > 0 && pageIds.every((id) => selected.has(id)));
	const someOnPageSelected = $derived(pageIds.some((id) => selected.has(id)));

	let bulkRemoveDialogOpen = $state(false);
	let bulkWorking = $state(false);

	let addOpen = $state(false);
	let addDomain = $state('');
	let addIssueNow = $state(true);
	let addSaving = $state(false);

	let issuing = $state<string | null>(null);
	let approvingId = $state<string | null>(null);

	let removeOpen = $state(false);
	let removeTarget = $state<CertificateDomain | null>(null);
	let removing = $state(false);

	let addError = $derived.by(() => {
		const d = addDomain.trim().toLowerCase();
		if (d === '') return '';
		if (!isIssuableHostname(d)) return 'Must be a public DNS name like registry.example.com';
		return '';
	});

	let primaryHealth = $derived(certHealth(status?.primaryCert));

	async function load() {
		loading = true;
		try {
			const [statusResp, domainsResp] = await Promise.all([
				rpcClient.certificate.getTLSStatus({}),
				rpcClient.certificate.listCertificateDomains({ page: pager.request(filter.request()) })
			]);
			status = statusResp;
			domains = domainsResp.domains;
			pager.apply(domainsResp.page);
		} catch {
			// error interceptor
		} finally {
			loading = false;
		}
	}

	async function refreshDomains() {
		try {
			const resp = await rpcClient.certificate.listCertificateDomains({ page: pager.request(filter.request()) });
			domains = resp.domains;
			pager.apply(resp.page);
		} catch {
			// error interceptor
		}
	}

	function filterChanged() {
		pager.reset();
		refreshDomains();
	}

	function openAdd() {
		addDomain = '';
		addIssueNow = status?.acmeEnabled ?? false;
		addOpen = true;
	}

	async function submitAdd() {
		const domain = addDomain.trim().toLowerCase();
		if (!domain || addError) return;
		addSaving = true;
		try {
			await rpcClient.certificate.addCertificateDomain({ domain });
			toast.success(`Domain ${domain} registered`);
			addOpen = false;
			pager.reset();
			await refreshDomains();
			if (addIssueNow) await issue(domain);
		} catch {
			// error interceptor
		} finally {
			addSaving = false;
		}
	}

	async function issue(domain: string) {
		issuing = domain;
		toast.info(`Requesting certificate for ${domain} - this can take a minute`);
		try {
			const resp = await rpcClient.certificate.issueCertificate({ domain });
			toast.success(
				resp.cert?.notAfter
					? `Certificate issued for ${domain}, valid until ${certDate(resp.cert)}`
					: `Certificate issued for ${domain}`
			);
			await refreshDomains();
		} catch {
			// error interceptor
		} finally {
			issuing = null;
		}
	}

	async function approve(domain: CertificateDomain) {
		approvingId = domain.id;
		try {
			await rpcClient.certificate.approveCertificateDomain({ id: domain.id });
			toast.success(`${domain.domain} approved for issuance`);
			await refreshDomains();
		} catch {
			// error interceptor
		} finally {
			approvingId = null;
		}
	}

	function openRemove(domain: CertificateDomain) {
		removeTarget = domain;
		removeOpen = true;
	}

	async function confirmRemove() {
		if (!removeTarget) return;
		removing = true;
		try {
			await rpcClient.certificate.removeCertificateDomain({ id: removeTarget.id });
			toast.success('Domain removed');
			removeOpen = false;
			pager.reset();
			await refreshDomains();
		} catch {
			// error interceptor
		} finally {
			removing = false;
		}
	}

	function toggleSelectPage() {
		if (allOnPageSelected) {
			for (const id of pageIds) selected.delete(id);
		} else {
			for (const id of pageIds) selected.add(id);
		}
	}

	function toggleSelect(id: string) {
		if (selected.has(id)) selected.delete(id);
		else selected.add(id);
	}

	function reportBulkErrors(errors: BulkOperationError[]) {
		if (errors.length === 0) return;
		const lookup = new Map(domains.map((d) => [d.id, d.domain]));
		const first = errors[0];
		const who = lookup.get(first.id) ?? first.id;
		toast.error(
			errors.length === 1
				? `${who}: ${first.error}`
				: `${errors.length} failed (${who}: ${first.error}, ...)`
		);
	}

	async function confirmBulkRemove() {
		bulkWorking = true;
		try {
			const resp = await rpcClient.certificate.bulkRemoveCertificateDomains({ ids: [...selected] });
			toast.success(`${resp.removedCount} domain${resp.removedCount !== 1 ? 's' : ''} removed`);
			reportBulkErrors(resp.errors);
			selected.clear();
			bulkRemoveDialogOpen = false;
			pager.reset();
			await refreshDomains();
		} catch {
			// error interceptor
		} finally {
			bulkWorking = false;
		}
	}

	onMount(() => {
		if (!authStore.canManageSettings) { goto(resolve('/admin')); return; }
		load();
	});
</script>

{#if loading}
	<div class="space-y-6">
		<Skeleton class="h-52 w-full rounded-xl" />
		<Skeleton class="h-40 w-full rounded-xl" />
	</div>
{:else if status}
	<div class="space-y-8">
		<!-- TLS / ACME Status -->
		<FormCard title="TLS / HTTPS" description="In-app TLS termination and automatic certificates" icon={Lock}>
			<div class="space-y-4">
				<div class="flex items-center gap-2">
					<span class="status-dot {status.tlsEnabled ? 'status-dot-active' : 'status-dot-inactive'}"></span>
					<span class="text-sm font-medium">
						{status.tlsEnabled ? 'TLS termination enabled' : 'TLS termination disabled'}
					</span>
				</div>

				{#if status.tlsEnabled}
					<div class="rounded-xl border border-border/60 overflow-hidden">
						<table class="w-full text-sm">
							<tbody>
								<tr class="border-b border-border/40">
									<td class="th text-left w-40">ACME issuance</td>
									<td class="px-3 py-2.5">
										<div class="flex items-center gap-2 flex-wrap">
											<Badge variant={status.acmeEnabled ? 'default' : 'secondary'} class="text-xs">
												{status.acmeEnabled ? 'Enabled' : 'Disabled'}
											</Badge>
											{#if status.acmeEnabled}
												<span class="text-xs text-muted-foreground">
													{status.acmeDirectory || "Let's Encrypt (production)"}
												</span>
											{/if}
										</div>
									</td>
								</tr>
								{#if status.acmeEnabled && status.acmeEmail}
									<tr class="border-b border-border/40">
										<td class="th text-left w-40">ACME email</td>
										<td class="px-3 py-2.5">
											<code class="text-xs bg-muted px-2 py-1 rounded font-mono">{status.acmeEmail}</code>
										</td>
									</tr>
								{/if}
								<tr class="border-b border-border/40">
									<td class="th text-left w-40">Manual certificate</td>
									<td class="px-3 py-2.5 text-sm">
										{status.manualCert ? 'Loaded (used as fallback)' : 'Not configured'}
									</td>
								</tr>
								<tr class={status.configDomains.length > 0 ? 'border-b border-border/40' : ''}>
									<td class="th text-left w-40">Primary hostname</td>
									<td class="px-3 py-2.5">
										<div class="flex items-center gap-2 flex-wrap">
											<code class="text-xs bg-muted px-2 py-1 rounded font-mono">{status.primaryHostname || '-'}</code>
											{#if status.primaryCert?.issued}
												<Badge variant="outline" class="text-xs {certBadgeClass(primaryHealth.tone)}" title={certDate(status.primaryCert)}>
													{primaryHealth.label}
												</Badge>
												<span class="text-xs text-muted-foreground">by {status.primaryCert.issuer}</span>
											{:else if status.acmeEnabled}
												<span class="text-xs text-muted-foreground">No certificate cached yet</span>
											{/if}
										</div>
									</td>
								</tr>
								{#if status.configDomains.length > 0}
									<tr>
										<td class="th text-left w-40">Config domains</td>
										<td class="px-3 py-2.5">
											<div class="flex gap-1 flex-wrap">
												{#each status.configDomains as domain (domain)}
													<Badge variant="outline" class="text-xs font-mono">{domain}</Badge>
												{/each}
											</div>
										</td>
									</tr>
								{/if}
							</tbody>
						</table>
					</div>
					<p class="text-[13px] text-muted-foreground">
						TLS termination is set in the server config at startup and cannot be toggled at
						runtime.
						{#if status.acmeEnabled}
							Certificates can - the challenge listener answers ACME challenges live, so issuing
							or renewing a certificate from this page never needs a restart.
						{/if}
						The main server and every portal listener share one certificate store - SNI picks the
						right certificate per hostname.
					</p>
				{:else if status.acmeEnabled}
					<p class="text-[13px] text-muted-foreground">
						The main server is serving cleartext, and switching it to TLS requires enabling
						<code class="text-xs bg-muted px-1.5 py-0.5 rounded font-mono">tls</code> in the server
						config and a restart. ACME is active though - the challenge listener answers challenges
						at runtime, so certificates for registered domains can be issued and renewed from this
						page now and are ready the moment TLS termination is turned on.
					</p>
				{:else}
					<p class="text-[13px] text-muted-foreground">
						Connections are served in cleartext - terminate HTTPS at a reverse proxy, or enable
						<code class="text-xs bg-muted px-1.5 py-0.5 rounded font-mono">tls</code> and
						<code class="text-xs bg-muted px-1.5 py-0.5 rounded font-mono">acme</code> in the
						server config for in-app termination with automatic certificates. Domains registered
						below are kept until issuance is possible.
					</p>
				{/if}
			</div>
		</FormCard>

		<!-- Certificate Domains -->
		<div class="space-y-4">
			<div class="section-header">
				<div>
					<h2 class="section-title">Certificate Domains</h2>
					<p class="section-subtitle max-w-2xl">
						Hostnames allowed to receive ACME certificates. The primary hostname and config
						domains are allowed automatically - register extra hostnames here, including
						organization portal domains.
					</p>
				</div>
				<div class="flex items-center gap-2">
					<div class="w-80">
						<QueryFilterBar {filter} placeholder="Search domains..." onchange={filterChanged} />
					</div>
					<Button size="sm" class="shrink-0" onclick={openAdd}>
						<Plus class="h-4 w-4 mr-1.5" />Add Domain
					</Button>
				</div>
			</div>

			{#if domains.length === 0}
				<EmptyState
					message={filter.active ? 'No domains match the current filter' : 'No certificate domains registered'}
					description={filter.active
						? 'Search matches the domain name.'
						: "Register a hostname to allow ACME issuance for it. Organization admins can also register their portal hostnames from the org's Certificates page."}
					icon={ShieldCheck}
				/>
			{:else}
				<div class="data-table">
					<Table>
						<TableHeader>
							<TableRow class="bg-muted/30 hover:bg-muted/30">
								<TableHead class="th w-10">
									<Checkbox
										checked={allOnPageSelected}
										indeterminate={someOnPageSelected && !allOnPageSelected}
										onCheckedChange={toggleSelectPage}
										aria-label="Select all on page"
									/>
								</TableHead>
								<TableHead class="th">Domain</TableHead>
								<TableHead class="th">Scope</TableHead>
								<TableHead class="th">Certificate</TableHead>
								<TableHead class="th">Created</TableHead>
								<TableHead class="th w-24"></TableHead>
							</TableRow>
						</TableHeader>
						<TableBody>
							{#each domains as domain (domain.id)}
								{@const health = certHealth(domain.cert)}
								<TableRow class={selected.has(domain.id) ? 'bg-primary/5 hover:bg-primary/5' : ''}>
									<TableCell class="py-3 px-3">
										<Checkbox
											checked={selected.has(domain.id)}
											onCheckedChange={() => toggleSelect(domain.id)}
											aria-label={`Select ${domain.domain}`}
										/>
									</TableCell>
									<TableCell class="py-3 px-3">
										<span class="font-medium text-sm font-mono">{domain.domain}</span>
									</TableCell>
									<TableCell class="py-3 px-3">
										{#if domain.scope === 'org'}
											<div class="flex items-center gap-1.5">
												<Badge variant="outline" class="text-xs">org</Badge>
												{#if domain.orgName}
													<a href={resolve('/orgs/[name]', { name: domain.orgName })} class="text-sm hover:text-primary transition-colors">
														{domain.orgName}
													</a>
												{/if}
											</div>
										{:else}
											<Badge variant="secondary" class="text-xs">system</Badge>
										{/if}
									</TableCell>
									<TableCell class="py-3 px-3">
										{#if health.issued}
											<div class="flex items-center gap-2 flex-wrap">
												<Badge variant="outline" class="text-xs {certBadgeClass(health.tone)}" title={certDate(domain.cert)}>
													{health.label}
												</Badge>
												{#if domain.cert?.issuer}
													<span class="text-xs text-muted-foreground">by {domain.cert.issuer}</span>
												{/if}
											</div>
										{:else if !domain.approved}
											<Badge variant="outline" class="text-xs border-amber-500/40 text-amber-600 dark:text-amber-400">
												Pending approval
											</Badge>
										{:else}
											<span class="text-sm text-muted-foreground">Not issued</span>
										{/if}
									</TableCell>
									<TableCell class="text-sm text-muted-foreground py-3 px-3">
										{domain.createdBy || '-'}
										{#if domain.createdAt}
											&middot; {relativeTime(timestampDate(domain.createdAt))}
										{/if}
									</TableCell>
									<TableCell class="text-right py-3 px-3">
										<div class="flex gap-1 justify-end">
											{#if !domain.approved}
												<Button
													variant="outline"
													size="sm"
													class="h-7"
													disabled={approvingId !== null}
													onclick={() => approve(domain)}
												>
													{#if approvingId === domain.id}
														<Loader2 class="h-3.5 w-3.5 mr-1.5 animate-spin" />
													{:else}
														<BadgeCheck class="h-3.5 w-3.5 mr-1.5" />
													{/if}
													Approve
												</Button>
											{:else if status.acmeEnabled}
												<Button
													variant="ghost"
													size="icon"
													class="h-7 w-7"
													title={health.issued ? 'Renew certificate' : 'Issue certificate'}
													disabled={issuing !== null}
													onclick={() => issue(domain.domain)}
												>
													{#if issuing === domain.domain}
														<Loader2 class="h-3.5 w-3.5 animate-spin" />
													{:else}
														<RefreshCw class="h-3.5 w-3.5" />
													{/if}
												</Button>
											{/if}
											<Button
												variant="ghost"
												size="icon"
												class="h-7 w-7 text-destructive hover:text-destructive"
												title="Remove domain"
												onclick={() => openRemove(domain)}
											>
												<Trash2 class="h-3.5 w-3.5" />
											</Button>
										</div>
									</TableCell>
								</TableRow>
							{/each}
						</TableBody>
					</Table>
				</div>

				<DataPagination
					page={pager.page} pageSize={pager.pageSize} totalCount={pager.totalCount}
					onPrev={() => { if (pager.prev()) refreshDomains(); }}
					onNext={() => { if (pager.next()) refreshDomains(); }}
				/>
			{/if}
		</div>
	</div>
{/if}

<!-- Bulk Actions -->
<BulkActionBar count={selected.size} onClear={() => selected.clear()}>
	<Button
		variant="ghost"
		size="sm"
		class="h-7 text-destructive hover:text-destructive"
		disabled={bulkWorking}
		onclick={() => (bulkRemoveDialogOpen = true)}
	>
		<Trash2 class="h-3.5 w-3.5 mr-1" />
		Remove
	</Button>
</BulkActionBar>

<!-- Add Domain Panel -->
<FormPanel
	bind:open={addOpen}
	title="Add Certificate Domain"
	description="Allow a hostname to receive ACME certificates."
	icon={ShieldCheck}
>
	<div class="space-y-4">
		<FormField
			label="Domain"
			id="cert-domain"
			required
			error={addError}
			help="A public DNS name that resolves to this server. The CA must be able to reach it on port 80 or 443."
		>
			<Input
				id="cert-domain"
				bind:value={addDomain}
				placeholder="registry.example.com"
				autocomplete="off"
				spellcheck={false}
			/>
		</FormField>
		<FormField
			label="Issue certificate now"
			help={status?.acmeEnabled
				? 'Request a certificate immediately after registering. Issuance can take up to a minute.'
				: 'ACME is not enabled - the domain is stored for later issuance.'}
			horizontal
		>
			<Switch bind:checked={addIssueNow} disabled={!status?.acmeEnabled} />
		</FormField>
	</div>

	{#snippet footer()}
		<Button variant="outline" onclick={() => (addOpen = false)}>Cancel</Button>
		<Button onclick={submitAdd} disabled={addSaving || !addDomain.trim() || !!addError}>
			{addSaving ? 'Registering...' : 'Register Domain'}
		</Button>
	{/snippet}
</FormPanel>

<!-- Remove Confirmation -->
<ConfirmDialog
	bind:open={removeOpen}
	title="Remove Domain"
	confirmLabel="Remove"
	onConfirm={confirmRemove}
	loading={removing}
	icon={Trash2}
>
	{#snippet description()}
		Are you sure you want to remove <strong class="font-mono">{removeTarget?.domain}</strong>?
		It will no longer receive or renew certificates.
	{/snippet}
</ConfirmDialog>

<!-- Bulk Remove Confirmation -->
<ConfirmDialog
	bind:open={bulkRemoveDialogOpen}
	title="Remove Domains"
	confirmLabel="Remove"
	onConfirm={confirmBulkRemove}
	loading={bulkWorking}
	icon={Trash2}
>
	{#snippet description()}
		Are you sure you want to remove <strong>{selected.size} domain{selected.size !== 1 ? 's' : ''}</strong>?
		They will no longer receive or renew certificates.
	{/snippet}
</ConfirmDialog>
