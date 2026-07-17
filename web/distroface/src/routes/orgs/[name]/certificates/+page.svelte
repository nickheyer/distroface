<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount, getContext } from 'svelte';
	import { SvelteSet } from 'svelte/reactivity';
	import { rpcClient } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Checkbox } from '$lib/components/ui/checkbox';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import BulkActionBar from '$lib/components/bulk-action-bar.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import { ShieldCheck, Lock, RefreshCw, Trash2, Loader2, Globe } from '@lucide/svelte';
	import { certHealth, certBadgeClass, certDate } from '$lib/cert-utils';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import type { CertificateDomain, CertificateHost } from '$lib/proto/distroface/v1/certificate_pb';
	import type { BulkOperationError } from '$lib/proto/distroface/v1/types_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');
	const orgId = $derived(ctx.org?.id ?? '');

	let hosts = $state<CertificateHost[]>([]);
	let loading = $state(true);
	let loaded = $state(false);
	let busy = $state<string | null>(null);
	const pager = new Pager(20);
	const filter = new QueryFilter([
		{ key: 'hostname', label: 'Hostname' },
		{ key: 'portal_name', label: 'Portal' }
	]);

	const selected = new SvelteSet<string>();

	let bulkRemoveDialogOpen = $state(false);
	let bulkWorking = $state(false);

	let removeOpen = $state(false);
	let removeTarget = $state<CertificateDomain | null>(null);
	let removing = $state(false);

	const pageIds = $derived(hosts.filter((h) => h.registration).map((h) => h.registration!.id));
	const allOnPageSelected = $derived(pageIds.length > 0 && pageIds.every((id) => selected.has(id)));
	const someOnPageSelected = $derived(pageIds.some((id) => selected.has(id)));

	$effect(() => {
		if (!ctx.loading && ctx.org && !ctx.canAdmin) {
			goto(resolve('/orgs/[name]', { name: orgName }));
		}
	});

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.certificate.listCertificateHosts({
				page: pager.request(filter.request()),
				orgId
			});
			hosts = resp.hosts;
			pager.apply(resp.page);
		} catch {
			// error interceptor
		} finally {
			loading = false;
			loaded = true;
		}
	}

	async function enableHTTPS(hostname: string) {
		busy = hostname;
		try {
			const resp = await rpcClient.certificate.addCertificateDomain({ domain: hostname, orgId });
			pager.reset();
			await load();
			if (resp.domain?.approved) {
				toast.success(`HTTPS enabled for ${hostname}`);
				await issue(hostname, true);
			} else {
				toast.success(`${hostname} registered - a system administrator must approve it before issuance`);
			}
		} catch {
			// error interceptor
		} finally {
			busy = null;
		}
	}

	async function issue(hostname: string, keepBusy = false) {
		if (!keepBusy) busy = hostname;
		toast.info(`Requesting certificate for ${hostname} - this can take a minute`);
		try {
			const resp = await rpcClient.certificate.issueCertificate({ domain: hostname, orgId });
			toast.success(
				resp.cert?.notAfter
					? `Certificate issued for ${hostname}, valid until ${certDate(resp.cert)}`
					: `Certificate issued for ${hostname}`
			);
			await load();
		} catch {
			// error interceptor
		} finally {
			if (!keepBusy) busy = null;
		}
	}

	function openRemove(registration: CertificateDomain) {
		removeTarget = registration;
		removeOpen = true;
	}

	async function confirmRemove() {
		if (!removeTarget) return;
		removing = true;
		try {
			await rpcClient.certificate.removeCertificateDomain({ id: removeTarget.id, orgId });
			toast.success('HTTPS disabled');
			removeOpen = false;
			pager.reset();
			await load();
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
		const lookup = new Map(hosts.filter((h) => h.registration).map((h) => [h.registration!.id, h.hostname]));
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
			const resp = await rpcClient.certificate.bulkRemoveCertificateDomains({ ids: [...selected], orgId });
			toast.success(`${resp.removedCount} domain${resp.removedCount !== 1 ? 's' : ''} removed`);
			reportBulkErrors(resp.errors);
			selected.clear();
			bulkRemoveDialogOpen = false;
			pager.reset();
			await load();
		} catch {
			// error interceptor
		} finally {
			bulkWorking = false;
		}
	}

	function filterChanged() {
		pager.reset();
		load();
	}

	onMount(load);
</script>

<div class="space-y-4">
	<div class="section-header">
		<div class="min-w-0 space-y-1">
			<h2 class="section-title">HTTPS Certificates</h2>
			<p class="section-subtitle max-w-2xl">
				Portal hostnames can receive automatic TLS certificates when the server has ACME enabled.
				Enable HTTPS for a hostname to register it for issuance and renewal.
			</p>
		</div>
	</div>

	<div class="max-w-md">
		<QueryFilterBar {filter} placeholder="Search hostnames..." onchange={filterChanged} />
	</div>

	{#if !loaded || ctx.loading}
		<div class="space-y-2">
			{#each { length: 2 }, i (i)}
				<Skeleton class="h-14 w-full rounded-xl" />
			{/each}
		</div>
	{:else if hosts.length === 0}
		<EmptyState
			message={filter.active ? 'No matching hostnames' : 'No portal hostnames yet'}
			description={filter.active
				? 'Try a different search.'
				: 'Certificates attach to portal hostnames. Create a portal with a hostname first, then enable HTTPS for it here.'}
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
						<TableHead class="th w-10">
							<Checkbox
								checked={allOnPageSelected}
								indeterminate={someOnPageSelected && !allOnPageSelected}
								onCheckedChange={toggleSelectPage}
								aria-label="Select all on page"
							/>
						</TableHead>
						<TableHead class="th">Hostname</TableHead>
						<TableHead class="th">Portal</TableHead>
						<TableHead class="th">HTTPS</TableHead>
						<TableHead class="th">Certificate</TableHead>
						<TableHead class="th w-32"></TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each hosts as host (host.hostname)}
						{@const health = certHealth(host.registration?.cert)}
						<TableRow class={host.registration && selected.has(host.registration.id) ? 'bg-primary/5 hover:bg-primary/5' : ''}>
							<TableCell class="py-3 px-3">
								{#if host.registration}
									<Checkbox
										checked={selected.has(host.registration.id)}
										onCheckedChange={() => toggleSelect(host.registration!.id)}
										aria-label={`Select ${host.hostname}`}
									/>
								{/if}
							</TableCell>
							<TableCell class="py-3 px-3">
								<span class="font-medium text-sm font-mono">{host.hostname}</span>
							</TableCell>
							<TableCell class="py-3 px-3">
								{#if host.portalName}
									<span class="text-sm">{host.portalName}</span>
								{:else}
									<Badge variant="outline" class="text-xs border-amber-500/40 text-amber-600 dark:text-amber-400">
										No matching portal
									</Badge>
								{/if}
							</TableCell>
							<TableCell class="py-3 px-3">
								{#if host.registration && !host.registration.approved}
									<Badge variant="outline" class="text-xs border-amber-500/40 text-amber-600 dark:text-amber-400">
										Pending approval
									</Badge>
								{:else if host.registration}
									<div class="flex items-center gap-1.5">
										<span class="status-dot status-dot-active"></span>
										<span class="text-sm">Enabled</span>
									</div>
								{:else if host.eligible}
									<div class="flex items-center gap-1.5">
										<span class="status-dot status-dot-inactive"></span>
										<span class="text-sm text-muted-foreground">Not enabled</span>
									</div>
								{:else}
									<span class="text-sm text-muted-foreground" title="ACME requires a public DNS name - IPs, ports, and local names are not issuable">
										Needs a public DNS name
									</span>
								{/if}
							</TableCell>
							<TableCell class="py-3 px-3">
								{#if health.issued}
									<div class="flex items-center gap-2 flex-wrap">
										<Badge variant="outline" class="text-xs {certBadgeClass(health.tone)}" title={certDate(host.registration?.cert)}>
											{health.label}
										</Badge>
										{#if host.registration?.cert?.issuer}
											<span class="text-xs text-muted-foreground">by {host.registration.cert.issuer}</span>
										{/if}
									</div>
								{:else if host.registration}
									<span class="text-sm text-muted-foreground">Not issued yet</span>
								{:else}
									<span class="text-sm text-muted-foreground">-</span>
								{/if}
							</TableCell>
							<TableCell class="text-right py-3 px-3">
								<div class="flex gap-1 justify-end items-center">
									{#if host.registration}
										{#if host.portalName && host.registration.approved}
											<Button
												variant="ghost"
												size="icon"
												class="h-7 w-7"
												title={health.issued ? 'Renew certificate' : 'Issue certificate'}
												disabled={busy !== null}
												onclick={() => issue(host.hostname)}
											>
												{#if busy === host.hostname}
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
											title="Disable HTTPS"
											onclick={() => openRemove(host.registration!)}
										>
											<Trash2 class="h-3.5 w-3.5" />
										</Button>
									{:else if host.eligible}
										<Button
											variant="outline"
											size="sm"
											class="h-7"
											disabled={busy !== null}
											onclick={() => enableHTTPS(host.hostname)}
										>
											{#if busy === host.hostname}
												<Loader2 class="h-3.5 w-3.5 mr-1.5 animate-spin" />
											{:else}
												<ShieldCheck class="h-3.5 w-3.5 mr-1.5" />
											{/if}
											Enable HTTPS
										</Button>
									{/if}
								</div>
							</TableCell>
						</TableRow>
					{/each}
				</TableBody>
			</Table>
		</div>

		<DataPagination
			page={pager.page} pageSize={pager.pageSize} totalCount={pager.totalCount}
			onPrev={() => { if (pager.prev()) load(); }}
			onNext={() => { if (pager.next()) load(); }}
		/>

		<p class="text-[13px] text-muted-foreground">
			Newly registered hostnames need approval from a system administrator before certificates can
			be issued. Issuance requires the server to be reachable from the internet on the hostname,
			with ACME enabled in the server's TLS configuration. Certificates renew automatically before
			expiry.
		</p>
	{/if}
</div>

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

<!-- Disable Confirmation -->
<ConfirmDialog
	bind:open={removeOpen}
	title="Disable HTTPS"
	confirmLabel="Disable"
	onConfirm={confirmRemove}
	loading={removing}
	icon={Trash2}
>
	{#snippet description()}
		Are you sure you want to disable HTTPS for
		<strong class="font-mono">{removeTarget?.domain}</strong>? The hostname will no longer receive
		or renew certificates.
	{/snippet}
</ConfirmDialog>

<!-- Bulk Disable Confirmation -->
<ConfirmDialog
	bind:open={bulkRemoveDialogOpen}
	title="Disable HTTPS"
	confirmLabel="Disable"
	onConfirm={confirmBulkRemove}
	loading={bulkWorking}
	icon={Trash2}
>
	{#snippet description()}
		Are you sure you want to disable HTTPS for
		<strong>{selected.size} hostname{selected.size !== 1 ? 's' : ''}</strong>? They will no longer
		receive or renew certificates.
	{/snippet}
</ConfirmDialog>
