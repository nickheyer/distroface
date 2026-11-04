<script lang="ts">
	import { page } from '$app/state';
	import { goto } from '$app/navigation';
	import { resolve } from '$app/paths';
	import { onMount, getContext } from 'svelte';
	import { rpcClient } from '$lib/api/rpc-client';
	import { Button } from '$lib/components/ui/button';
	import { Badge } from '$lib/components/ui/badge';
	import { Skeleton } from '$lib/components/ui/skeleton';
	import { Switch } from '$lib/components/ui/switch';
	import ConfirmDialog from '$lib/components/confirm-dialog.svelte';
	import CopyButton from '$lib/components/copy-button.svelte';
	import EmptyState from '$lib/components/empty-state.svelte';
	import DataPagination from '$lib/components/data-pagination.svelte';
	import QueryFilterBar from '$lib/components/query-filter.svelte';
	import { Globe, Plus, Pencil, Trash } from '$lib/icons';
	import { timestampDate } from '@bufbuild/protobuf/wkt';
	import { relativeTime } from '$lib/utils';
	import { Pager } from '$lib/pager.svelte';
	import { QueryFilter } from '$lib/query.svelte';
	import { effectiveAddress } from '$lib/portal-address';
	import { certStateBadge } from '$lib/cert-utils';
	import { CertState } from '$lib/proto/distroface/v1/certificate_pb';
	import type { RegistryPortal } from '$lib/proto/distroface/v1/portal_pb';
	import { ORG_CONTEXT_KEY, type OrgContext } from '$lib/org-context.svelte';
	import { configStore } from '$lib/stores/config.svelte';

	const ctx = getContext<OrgContext>(ORG_CONTEXT_KEY);
	const orgName = $derived(page.params.name ?? '');
	const orgId = $derived(ctx.org?.id ?? '');
	const mainPort = $derived(configStore.mainPort);

	let portals = $state<RegistryPortal[]>([]);
	let loading = $state(true);
	let loaded = $state(false);
	let toggling = $state<string | null>(null);
	const pager = new Pager(20);
	const filter = new QueryFilter([
		{ key: 'name', label: 'Name' },
		{ key: 'hostname', label: 'Hostname' }
	]);

	let deleteOpen = $state(false);
	let deleteTarget = $state<RegistryPortal | null>(null);
	let deleting = $state(false);

	$effect(() => {
		if (!ctx.loading && ctx.org && !ctx.canAdmin) {
			goto(resolve('/orgs/[name]', { name: orgName }));
		}
	});

	async function load() {
		loading = true;
		try {
			const resp = await rpcClient.portal.listPortals({
				page: pager.request(filter.request()),
				orgId
			});
			portals = resp.portals;
			pager.apply(resp.page);
		} catch {
			portals = [];
		} finally {
			loading = false;
			loaded = true;
		}
	}

	function accessBadges(portal: RegistryPortal): string[] {
		const badges = [];
		if (portal.tls) badges.push('HTTPS');
		if (portal.backendUrl) {
			badges.push(`Proxy to ${portal.backendUrl}`);
			return badges;
		}
		if (!portal.allowPush) badges.push('Pull only');
		if (!portal.anonymousAccess) badges.push('Sign-in required');
		if (portal.mapUnqualified) badges.push('Bare names');
		if (portal.rules.length > 0) {
			badges.push(`${portal.rules.length} rewrite${portal.rules.length !== 1 ? 's' : ''}`);
		}
		return badges;
	}

	async function setRunning(portal: RegistryPortal, enabled: boolean) {
		toggling = portal.id;
		try {
			const resp = await rpcClient.portal.updatePortal({ orgId, id: portal.id, enabled });
			const updated = resp.portal;
			if (updated) portals = portals.map((p) => (p.id === updated.id ? updated : p));
		} catch {
			load();
		} finally {
			toggling = null;
		}
	}

	function openEdit(portal: RegistryPortal) {
		goto(resolve('/orgs/[name]/portals/[id]', { name: orgName, id: portal.id }));
	}

	function confirmDelete(portal: RegistryPortal) {
		deleteTarget = portal;
		deleteOpen = true;
	}

	async function doDelete() {
		if (!deleteTarget) return;
		deleting = true;
		try {
			await rpcClient.portal.deletePortal({ orgId, id: deleteTarget.id });
			deleteOpen = false;
			await load();
			if (portals.length === 0 && pager.prev()) {
				await load();
			}
		} catch {
			/* error interceptor */
		} finally {
			deleting = false;
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
		<div class="min-w-0">
			<h2 class="section-title">Portals</h2>
			<p class="section-subtitle">Dedicated addresses that serve only this organization.</p>
		</div>
		<Button
			class="shrink-0"
			onclick={() => goto(resolve('/orgs/[name]/portals/new', { name: orgName }))}
		>
			<Plus />
			New portal
		</Button>
	</div>

	<div class="max-w-md">
		<QueryFilterBar {filter} placeholder="Search portals" onchange={filterChanged} />
	</div>

	{#if !loaded}
		<div class="box divide-y">
			{#each { length: 2 }, i (i)}
				<div class="flex items-center gap-3 px-4 py-3">
					<div class="flex-1 space-y-2">
						<Skeleton class="h-4 w-40" />
						<Skeleton class="h-3.5 w-64" />
						<Skeleton class="h-3 w-48" />
					</div>
					<Skeleton class="h-5 w-9 rounded-full" />
				</div>
			{/each}
		</div>
	{:else if portals.length === 0}
		<EmptyState
			icon={Globe}
			message={filter.active ? 'No matching portals' : 'No portals yet'}
			description={filter.active
				? 'Try a different search.'
				: `Give ${orgName} its own address like registry.example.com.`}
		>
			{#snippet actions()}
				{#if !filter.active}
					<Button
						variant="outline"
						onclick={() => goto(resolve('/orgs/[name]/portals/new', { name: orgName }))}
					>
						<Plus />
						New portal
					</Button>
				{/if}
			{/snippet}
		</EmptyState>
	{:else}
		<div class="box divide-y transition-opacity duration-200 {loading ? 'opacity-60' : ''}">
			{#each portals as portal (portal.id)}
				{@const stateBadge = certStateBadge(portal.certState)}
				{@const address = effectiveAddress(portal.hostname, portal.port)}
				<div class="flex items-start gap-3 px-4 py-3">
					<div class="min-w-0 flex-1">
						<div class="flex flex-wrap items-center gap-x-2 gap-y-1">
							<span
								class="truncate text-sm font-semibold {portal.enabled
									? ''
									: 'text-muted-foreground'}"
							>
								{portal.name}
							</span>
							{#if stateBadge}
								<Badge variant={stateBadge.variant} title={portal.certDetail}>
									{stateBadge.label}
								</Badge>
							{/if}
							{#if !portal.enabled}
								<Badge variant="outline">Stopped</Badge>
							{/if}
						</div>
						<div class="mt-0.5 flex min-w-0 items-center gap-1">
							<span
								class="truncate font-mono text-sm {portal.enabled ? '' : 'text-muted-foreground'}"
								>{address}</span
							>
							<CopyButton text={address} label="Address copied" />
						</div>
						{#if portal.hostname === ''}
							<p class="text-xs text-muted-foreground">Any hostname on port {portal.port}</p>
						{:else if portal.port === 0}
							<p class="text-xs text-muted-foreground">
								App port{mainPort ? ` (${mainPort})` : ''}
							</p>
						{/if}
						{#if portal.certState === CertState.ERROR || portal.certState === CertState.PENDING}
							<p
								class="text-xs {portal.certState === CertState.ERROR
									? 'text-destructive'
									: 'text-warning'}"
							>
								{portal.certDetail}
							</p>
						{/if}
						<div
							class="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-muted-foreground"
						>
							<div class="flex flex-wrap gap-1">
								{#each accessBadges(portal) as badge (badge)}
									<Badge variant="outline">{badge}</Badge>
								{:else}
									<span>Open push and pull</span>
								{/each}
							</div>
							{#if portal.createdAt}
								<span>Created {relativeTime(timestampDate(portal.createdAt))}</span>
							{/if}
						</div>
					</div>
					<div class="flex shrink-0 items-center gap-2">
						<div class="flex items-center gap-2">
							<Switch
								checked={portal.enabled}
								disabled={toggling === portal.id}
								onCheckedChange={(checked) => setRunning(portal, checked)}
								aria-label="{portal.enabled ? 'Stop' : 'Start'} portal {portal.name}"
							/>
							<span class="hidden text-xs text-muted-foreground sm:inline">
								{portal.enabled ? 'Running' : 'Stopped'}
							</span>
						</div>
						<Button
							variant="ghost-muted"
							size="icon-sm"
							onclick={() => openEdit(portal)}
							aria-label="Edit portal"
						>
							<Pencil />
						</Button>
						<Button
							variant="ghost"
							size="icon-sm"
							class="text-destructive hover:text-destructive"
							onclick={() => confirmDelete(portal)}
							aria-label="Delete portal"
						>
							<Trash />
						</Button>
					</div>
				</div>
			{/each}
		</div>
		<DataPagination {pager} onChange={load} />
	{/if}
</div>

<ConfirmDialog
	bind:open={deleteOpen}
	title="Delete portal"
	confirmLabel="Delete"
	onConfirm={doDelete}
	loading={deleting}
	icon={Trash}
>
	{#snippet description()}
		Delete <strong>{deleteTarget?.name}</strong> at
		<strong>{deleteTarget ? effectiveAddress(deleteTarget.hostname, deleteTarget.port) : ''}</strong
		>? Clients using this address will stop working immediately.
	{/snippet}
</ConfirmDialog>
