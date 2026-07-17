<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount, getContext } from 'svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { toast } from 'svelte-sonner';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import {
		Table, TableBody, TableCell, TableHead, TableHeader, TableRow
	} from '$lib/components/ui/table';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import { ShieldCheck, Lock, RefreshCw, Trash2, Loader2, Globe } from '@lucide/svelte';
	import { certHealth, certBadgeClass, certDate, isIssuableHostname } from '$lib/cert-utils';
	import type { CertificateDomain } from '$lib/proto/distroface/v1/certificate_pb';
	import type { RegistryPortal } from '$lib/proto/distroface/v1/portal_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');

	let portals = $state<RegistryPortal[]>([]);
	let domains = $state<CertificateDomain[]>([]);
	let loading = $state(true);
	let busy = $state<string | null>(null);

	let removeOpen = $state(false);
	let removeTarget = $state<CertificateDomain | null>(null);
	let removing = $state(false);

	type HostRow = {
		hostname: string;
		portalName: string;
		eligible: boolean;
		registration: CertificateDomain | null;
	};

	// Portal hostnames first, then registrations without a portal
	const rows = $derived.by(() => {
		const byDomain = new Map(domains.map((d) => [d.domain, d]));
		const out: HostRow[] = portals
			.map((portal) => ({ hostname: portal.hostname.trim().toLowerCase(), name: portal.name }))
			.filter((p, i, all) => p.hostname !== '' && all.findIndex((q) => q.hostname === p.hostname) === i)
			.map((p) => ({
				hostname: p.hostname,
				portalName: p.name,
				eligible: isIssuableHostname(p.hostname),
				registration: byDomain.get(p.hostname) ?? null
			}));
		const covered = out.map((r) => r.hostname);
		for (const domain of domains) {
			if (!covered.includes(domain.domain)) {
				out.push({ hostname: domain.domain, portalName: '', eligible: true, registration: domain });
			}
		}
		return out;
	});

	$effect(() => {
		if (!ctx.loading && ctx.org && !ctx.canAdmin) {
			goto(resolve('/orgs/[name]', { name: orgName }));
		}
	});

	async function load() {
		loading = true;
		try {
			const [portalsResp, domainsResp] = await Promise.all([
				rpcClient.portal.listPortals({ orgName }),
				rpcClient.certificate.listCertificateDomains({ orgName })
			]);
			portals = portalsResp.portals;
			domains = domainsResp.domains;
		} catch {
			// error interceptor
		} finally {
			loading = false;
		}
	}

	async function refreshDomains() {
		try {
			const resp = await rpcClient.certificate.listCertificateDomains({ orgName });
			domains = resp.domains;
		} catch {
			// error interceptor
		}
	}

	async function enableHTTPS(hostname: string) {
		busy = hostname;
		try {
			const resp = await rpcClient.certificate.addCertificateDomain({ domain: hostname, orgName });
			await refreshDomains();
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
			const resp = await rpcClient.certificate.issueCertificate({ domain: hostname, orgName });
			toast.success(
				resp.cert?.notAfter
					? `Certificate issued for ${hostname}, valid until ${certDate(resp.cert)}`
					: `Certificate issued for ${hostname}`
			);
			await refreshDomains();
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
			await rpcClient.certificate.removeCertificateDomain({ id: removeTarget.id, orgName });
			toast.success('HTTPS disabled');
			removeOpen = false;
			await refreshDomains();
		} catch {
			// error interceptor
		} finally {
			removing = false;
		}
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

	{#if loading || ctx.loading}
		<div class="space-y-2">
			{#each { length: 2 }, i (i)}
				<Skeleton class="h-14 w-full rounded-xl" />
			{/each}
		</div>
	{:else if rows.length === 0}
		<EmptyState
			message="No portal hostnames yet"
			description="Certificates attach to portal hostnames. Create a portal with a hostname first, then enable HTTPS for it here."
			icon={Lock}
		>
			{#snippet actions()}
				<Button variant="outline" size="sm" onclick={() => goto(resolve('/orgs/[name]/portals', { name: orgName }))}>
					<Globe class="h-4 w-4 mr-1.5" />Go to Portals
				</Button>
			{/snippet}
		</EmptyState>
	{:else}
		<div class="data-table">
			<Table>
				<TableHeader>
					<TableRow class="bg-muted/30 hover:bg-muted/30">
						<TableHead class="th">Hostname</TableHead>
						<TableHead class="th">Portal</TableHead>
						<TableHead class="th">HTTPS</TableHead>
						<TableHead class="th">Certificate</TableHead>
						<TableHead class="th w-32"></TableHead>
					</TableRow>
				</TableHeader>
				<TableBody>
					{#each rows as row (row.hostname)}
						{@const health = certHealth(row.registration?.cert)}
						<TableRow>
							<TableCell class="py-3 px-3">
								<span class="font-medium text-sm font-mono">{row.hostname}</span>
							</TableCell>
							<TableCell class="py-3 px-3">
								{#if row.portalName}
									<span class="text-sm">{row.portalName}</span>
								{:else}
									<Badge variant="outline" class="text-xs border-amber-500/40 text-amber-600 dark:text-amber-400">
										No matching portal
									</Badge>
								{/if}
							</TableCell>
							<TableCell class="py-3 px-3">
								{#if row.registration && !row.registration.approved}
									<Badge variant="outline" class="text-xs border-amber-500/40 text-amber-600 dark:text-amber-400">
										Pending approval
									</Badge>
								{:else if row.registration}
									<div class="flex items-center gap-1.5">
										<span class="status-dot status-dot-active"></span>
										<span class="text-sm">Enabled</span>
									</div>
								{:else if row.eligible}
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
										<Badge variant="outline" class="text-xs {certBadgeClass(health.tone)}" title={certDate(row.registration?.cert)}>
											{health.label}
										</Badge>
										{#if row.registration?.cert?.issuer}
											<span class="text-xs text-muted-foreground">by {row.registration.cert.issuer}</span>
										{/if}
									</div>
								{:else if row.registration}
									<span class="text-sm text-muted-foreground">Not issued yet</span>
								{:else}
									<span class="text-sm text-muted-foreground">-</span>
								{/if}
							</TableCell>
							<TableCell class="text-right py-3 px-3">
								<div class="flex gap-1 justify-end items-center">
									{#if row.registration}
										{#if row.portalName && row.registration.approved}
											<Button
												variant="ghost"
												size="icon"
												class="h-7 w-7"
												title={health.issued ? 'Renew certificate' : 'Issue certificate'}
												disabled={busy !== null}
												onclick={() => issue(row.hostname)}
											>
												{#if busy === row.hostname}
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
											onclick={() => openRemove(row.registration!)}
										>
											<Trash2 class="h-3.5 w-3.5" />
										</Button>
									{:else if row.eligible}
										<Button
											variant="outline"
											size="sm"
											class="h-7"
											disabled={busy !== null}
											onclick={() => enableHTTPS(row.hostname)}
										>
											{#if busy === row.hostname}
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

		<p class="text-[13px] text-muted-foreground">
			Newly registered hostnames need approval from a system administrator before certificates can
			be issued. Issuance requires the server to be reachable from the internet on the hostname,
			with ACME enabled in the server's TLS configuration. Certificates renew automatically before
			expiry.
		</p>
	{/if}
</div>

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
